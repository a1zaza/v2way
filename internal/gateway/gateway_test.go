package gateway

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"v2way/internal/certificate"
	"v2way/internal/config"
	"v2way/internal/store"
)

func configuration(route config.Route) config.Config {
	return config.Config{Sites: []config.Site{{ID: "site", Name: "测试站点", Domains: []string{"app.example.com"}, Enabled: true, HTTPPort: 18080, MaxBodyMiB: 1, Routes: []config.Route{route}}}, Upstreams: []config.Upstream{}}
}
func response(body string) config.Route {
	return config.Route{ID: "route", Name: "测试响应", Path: "/", Match: "prefix", Action: "response", Status: 200, Body: body}
}
func activate(t *testing.T, e *Engine, c config.Config) {
	t.Helper()
	s, err := e.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	e.Activate(s)
}
func request(e *Engine, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://app.example.com"+path, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}
func TestRoutingHeadersAndRedaction(t *testing.T) {
	var headers http.Header
	var host, uri string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		host = r.Host
		uri = r.URL.RequestURI()
		io.WriteString(w, "backend")
	}))
	defer backend.Close()
	route := response("")
	route.Action = "proxy"
	route.Path = "/api"
	route.UpstreamID = "u"
	route.StripPrefix = true
	route.PreserveHost = true
	c := configuration(route)
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	r := httptest.NewRequest("GET", "http://app.example.com/api/users?a=1&a=2&token=secret", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "spoofed")
	r.Header.Set("Forwarded", "for=spoofed")
	r.Header.Set("X-Real-IP", "spoofed")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "backend" || host != "app.example.com" || uri != "/users?a=1&a=2&token=secret" {
		t.Fatalf("代理改写错误: %d %q %q %q", w.Code, w.Body.String(), host, uri)
	}
	if headers.Get("X-Forwarded-For") != "127.0.0.1" || headers.Get("Forwarded") != "" || headers.Get("X-Real-IP") != "127.0.0.1" {
		t.Fatalf("来源头未清理: %v", headers)
	}
	logs, _ := json.Marshal(e.Logs())
	if strings.Contains(string(logs), "secret") {
		t.Fatal("查询令牌泄露")
	}
	if got := request(e, "GET", "/apix"); got.Code != 404 {
		t.Fatalf("前缀越界命中: %d", got.Code)
	}
	for _, p := range []string{"/api/%2e%2e/admin", "/api/%252e%252e/admin", "/api/a%2fb", "/api/../admin"} {
		if got := request(e, "GET", p); got.Code != 400 {
			t.Errorf("危险路径 %s 返回 %d", p, got.Code)
		}
	}
	wrong := httptest.NewRequest("GET", "http://unknown.example/api", nil)
	ww := httptest.NewRecorder()
	e.ServeHTTP(ww, wrong)
	if ww.Code != 404 {
		t.Fatal("未知域名未拒绝")
	}
	if got := request(e, "GET", "/api/admin/v1/overview"); got.Body.String() != "backend" {
		t.Fatal("业务请求进入管理处理器")
	}
}
func TestStaticIsolationSPAAndRange(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "public")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>首页</html>"), 0600)
	os.WriteFile(filepath.Join(root, "asset.txt"), []byte("0123456789"), 0600)
	os.WriteFile(filepath.Join(parent, "secret"), []byte("VERY_SECRET"), 0600)
	os.Symlink(filepath.Join(parent, "secret"), filepath.Join(root, "leak.txt"))
	route := response("")
	route.Action = "static"
	route.Root = root
	route.SPA = true
	e := New([]string{root}, nil)
	defer e.Close()
	activate(t, e, configuration(route))
	if w := request(e, "GET", "/deep/page"); w.Code != 200 || !strings.Contains(w.Body.String(), "首页") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA 回退失败: %d %s", w.Code, w.Body.String())
	}
	for _, p := range []string{"/missing.js", "/leak.txt"} {
		if w := request(e, "GET", p); w.Code != 404 || strings.Contains(w.Body.String(), "VERY_SECRET") {
			t.Fatalf("静态隔离失败 %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "http://app.example.com/asset.txt", nil)
	r.Header.Set("Range", "bytes=2-4")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "234" {
		t.Fatalf("Range 错误: %d %s", w.Code, w.Body.String())
	}
	route.Root = parent
	if snap, err := e.Compile(configuration(route)); err == nil {
		snap.Close()
		t.Fatal("允许未批准的静态目录")
	}
}
func TestStaticRootCannotBeReplacedWithOutsideSymlink(t *testing.T) {
	parent := t.TempDir()
	approved := filepath.Join(parent, "public")
	root := filepath.Join(approved, "app")
	outside := filepath.Join(parent, "secret")
	os.MkdirAll(root, 0700)
	os.Mkdir(outside, 0700)
	os.WriteFile(filepath.Join(root, "index.html"), []byte("safe"), 0600)
	os.WriteFile(filepath.Join(outside, "index.html"), []byte("SECRET"), 0600)
	route := response("")
	route.Action = "static"
	route.Root = root
	e := New([]string{approved}, nil)
	defer e.Close()
	activate(t, e, configuration(route))
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	w := request(e, "GET", "/")
	if w.Code != 503 || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("根目录切换越界: %d %s", w.Code, w.Body.String())
	}
}
func TestIPAndRateLimits(t *testing.T) {
	c := configuration(response("allowed"))
	c.Sites[0].AllowCIDRs = []string{"127.0.0.0/8"}
	c.Sites[0].DenyCIDRs = []string{"127.0.0.1/32"}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	if w := request(e, "GET", "/"); w.Code != 403 {
		t.Fatal("黑名单未优先拒绝")
	}
	c.Sites[0].DenyCIDRs = nil
	c.Sites[0].RatePerSecond = 1
	activate(t, e, c)
	if w := request(e, "GET", "/"); w.Code != 200 {
		t.Fatal("允许请求被拒绝")
	}
	if w := request(e, "GET", "/"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("速率限制未生效")
	}
}
func TestTLSCertificateRoutingAndHostIsolation(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"app.example.com", "other.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := certificate.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Upload("测试 TLS", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})))
	if err != nil {
		t.Fatal(err)
	}
	e := New(nil, m)
	defer e.Close()
	c := configuration(response("encrypted"))
	c.Sites[0].HTTPS = true
	c.Sites[0].HTTPSPort = 8443
	c.Sites[0].CertificateID = info.ID
	c.Sites[0].RedirectHTTPS = true
	other := c.Sites[0]
	other.ID = "other"
	other.Domains = []string{"other.example.com"}
	other.Routes = []config.Route{response("other")}
	other.Routes[0].ID = "other-route"
	c.Sites = append(c.Sites, other)
	activate(t, e, c)
	if w := request(e, "GET", "/page?x=1"); w.Code != 308 || w.Header().Get("Location") != "https://app.example.com:8443/page?x=1" {
		t.Fatalf("HTTPS 跳转错误: %d %s", w.Code, w.Header().Get("Location"))
	}
	srv := httptest.NewUnstartedServer(e)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = e.TLSConfig(8443)
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(parsed)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "app.example.com", MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Host = "app.example.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "encrypted" {
		t.Fatalf("TLS 代理失败: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest("GET", srv.URL+"/", nil)
	req.Host = "other.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 421 {
		t.Fatalf("跨 SNI Host 未拒绝: %d", resp.StatusCode)
	}
	if _, err = e.certificateForPort(&tls.ClientHelloInfo{ServerName: "unknown.example.com"}, 8443); err == nil {
		t.Fatal("未知 SNI 获得证书")
	}
}
func TestSSEFlushAndCancellation(t *testing.T) {
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	route := response("")
	route.Action = "proxy"
	route.UpstreamID = "u"
	route.Streaming = true
	c := configuration(route)
	c.Upstreams = []config.Upstream{{ID: "u", Name: "SSE", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	server := httptest.NewServer(e)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	req.Host = "app.example.com"
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" || time.Since(start) > time.Second {
		t.Fatalf("SSE 未及时刷新: %q %v", line, err)
	}
	resp.Body.Close()
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("取消未传播到后端")
	}
}
func TestWebSocketSurvivesPublication(t *testing.T) {
	backend := httptest.NewServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: websocket.Handler(func(ws *websocket.Conn) { defer ws.Close(); io.Copy(ws, ws) })})
	defer backend.Close()
	route := response("")
	route.Action = "proxy"
	route.UpstreamID = "u"
	c := configuration(route)
	c.Upstreams = []config.Upstream{{ID: "u", Name: "WS", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	server := httptest.NewServer(e)
	defer server.Close()
	wsConfig, err := websocket.NewConfig("ws"+strings.TrimPrefix(server.URL, "http")+"/", "http://app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	wsConfig.Header.Set("Host", "app.example.com")
	// websocket.DialConfig derives Host from Location. Connect to the test listener
	// using a custom dialer while preserving the configured virtual host.
	wsConfig.Location.Host = "app.example.com"
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := websocket.NewClient(wsConfig, conn)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(3 * time.Second))
	if err = websocket.Message.Send(ws, "before"); err != nil {
		t.Fatal(err)
	}
	var msg string
	if err = websocket.Message.Receive(ws, &msg); err != nil || msg != "before" {
		t.Fatalf("WebSocket 握手/转发失败: %q %v", msg, err)
	}
	activate(t, e, configuration(response("new configuration")))
	if err = websocket.Message.Send(ws, "after"); err != nil {
		t.Fatal(err)
	}
	if err = websocket.Message.Receive(ws, &msg); err != nil || msg != "after" {
		t.Fatalf("发布中断已有连接: %q %v", msg, err)
	}
	if w := request(e, "GET", "/"); w.Body.String() != "new configuration" {
		t.Fatal("新请求没有使用新配置")
	}
}

func TestAccessLogsRecordConnectionIP(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, configuration(response("ok")))
	for _, tt := range []struct {
		remote, host, want string
		status             int
	}{
		{"218.71.138.178:51234", "app.example.com", "218.71.138.178", 200},
		{"[2001:db8::1234]:51234", "app.example.com", "2001:db8::1234", 200},
		{"[::ffff:192.0.2.1]:51234", "app.example.com", "192.0.2.1", 200},
		{"192.0.2.2:51234", "unknown.example.com", "192.0.2.2", 404},
		{"invalid", "app.example.com", "", 200},
	} {
		r := httptest.NewRequest("GET", "http://"+tt.host+"/", nil)
		r.RemoteAddr = tt.remote
		r.Header.Set("Forwarded", "for=198.51.100.99")
		r.Header.Set("X-Forwarded-For", "198.51.100.99")
		r.Header.Set("X-Real-IP", "198.51.100.99")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		logs := e.Logs()
		if w.Code != tt.status || logs[0].IP != tt.want {
			t.Fatal(tt, w.Code, logs[0])
		}
	}
}
