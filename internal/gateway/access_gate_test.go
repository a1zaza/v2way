package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"golang.org/x/net/websocket"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
	"v2way/internal/accessgate"
	"v2way/internal/certificate"
	"v2way/internal/config"
	"v2way/internal/password"
	"v2way/internal/store"
)

const testLoginPath = "/access"

func accessEngine(t *testing.T) (*Engine, *store.Store, config.Config) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	hash, err := password.Hash("01234567")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccessAccount("site", 0, "visitor", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	certs, err := certificate.Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(certs.Automation().Close)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.com"}, DNSNames: []string{"app.example.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := x509.MarshalPKCS8PrivateKey(key)
	info, err := certs.Upload("entry", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})))
	if err != nil {
		t.Fatal(err)
	}
	gate := accessgate.New(s)
	t.Cleanup(gate.Close)
	e := New(nil, certs)
	e.Access = gate
	t.Cleanup(e.Close)
	c := configuration(response("private content"))
	c.Sites[0].HTTPS = true
	c.Sites[0].HTTPSPort = 18443
	c.Sites[0].CertificateID = info.ID
	c.Sites[0].RequireIPLogin = true
	login := response("")
	login.ID = "login-route"
	login.Path = testLoginPath
	login.Match = "exact"
	login.Action = config.LoginAction
	login.Methods = []string{"GET", "POST"}
	c.Sites[0].Routes = append(c.Sites[0].Routes, login)
	c.Sites[0].Routes[0].Path = "/good"
	c.Sites[0].Routes[0].Match = "exact"
	bad := response("application error")
	bad.ID = "bad"
	bad.Path = "/bad"
	bad.Match = "exact"
	bad.Status = 404
	c.Sites[0].Routes = append(c.Sites[0].Routes, bad)
	activate(t, e, c)
	return e, s, c
}
func TestIPGateRequiresAuthorizationAndRenewsOnlySuccessfulBusiness(t *testing.T) {
	e, s, c := accessEngine(t)
	call := func(path, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://app.example.com"+path, nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "192.0.2.10")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	if w := call("/good", "192.0.2.10:100"); w.Code != 404 || strings.Contains(w.Body.String(), "private content") || strings.Contains(w.Body.String(), testLoginPath) {
		t.Fatal("未授权被放行或入口泄漏", w.Code, w.Body.String())
	}
	grant, err := s.AuthorizeIP("site", "192.0.2.10", 1, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/bad", "/missing"} {
		if w := call(path, "192.0.2.10:200"); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	before, _ := e.Access.Check("site", grant.IP)
	if before.LastAccessAt != grant.LastAccessAt {
		t.Fatal("错误请求续期")
	}
	if w := call("/good", "192.0.2.11:100"); w.Code != 404 {
		t.Fatal("使用了伪造来源头")
	}
	c.Sites[0].Routes[0].ResponseHeaders = map[string]string{"Cache-Control": "public, max-age=3600"}
	activate(t, e, c)
	if w := call("/good", "192.0.2.10:999"); w.Code != 200 || w.Body.String() != "private content" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("正常请求或缓存策略错误", w.Code, w.Header())
	}
	after, _ := e.Access.Check("site", grant.IP)
	if !after.LastAccessAt.After(before.LastAccessAt) || after.ExpiresAt.Sub(after.LastAccessAt) != 48*time.Hour {
		t.Fatal("未按48小时续期", after)
	}
	if err := s.RevokeAccess(grant.ID, -1); err != nil {
		t.Fatal(err)
	}
	if w := call("/good", "192.0.2.10:999"); w.Code != 404 {
		t.Fatal("撤销未生效")
	}
	expired, err := s.AuthorizeIP("site", grant.IP, 1, time.Now().Add(-49*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if w := call("/good", "192.0.2.10:999"); w.Code != 404 {
		t.Fatal("到期请求续期复活")
	}
	e.Access.Touch("site", expired.IP, expired.ID)
	if _, ok := e.Access.Check("site", expired.IP); ok {
		t.Fatal("过期流量复活")
	}
}
func TestDisabledIPGatePreservesExistingBusinessNamespace(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, configuration(response("existing application")))
	if w := request(e, "GET", "/__access/existing-business-route"); w.Code != 200 || w.Body.String() != "existing application" {
		t.Fatal("未启用功能却拦截了原有应用", w.Code, w.Body.String())
	}
}
func TestHiddenEndpointBindingRedactionAndMaintenance(t *testing.T) {
	e, s, c := accessEngine(t)
	r := httptest.NewRequest("GET", "https://app.example.com:18443"+testLoginPath, nil)
	r.RemoteAddr = "192.0.2.20:10"
	r.TLS = &tls.ConnectionState{ServerName: "app.example.com"}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "登录并授权当前 IP") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, entry := range e.Logs() {
		if strings.Contains(entry.Path, strings.Repeat("a", 64)) {
			t.Fatal("隐藏路径进入日志")
		}
	}
	// Visiting the login form alone does not add the source IP.
	if _, ok := s.CheckAccessIP("site", "192.0.2.20", time.Now()); ok {
		t.Fatal("GET 直接授权")
	}
	r = httptest.NewRequest("GET", "http://app.example.com"+testLoginPath, nil)
	r.RemoteAddr = "192.0.2.20:10"
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("HTTP 暴露登录表单", w.Code)
	}
	// A protected non-entry site can enter maintenance; authorization never overrides it.
	other := c.Sites[0]
	other.ID = "other"
	other.Domains = []string{"other.example.com"}
	other.Routes = []config.Route{response("other")}
	other.Routes[0].ID = "other-route"
	other.HTTPS = false
	other.HTTPSPort = 0
	other.CertificateID = ""
	other.RequireIPLogin = false
	other.Maintenance = true
	c.Sites = append(c.Sites, other)
	activate(t, e, c)
	r = httptest.NewRequest("GET", "http://other.example.com/", nil)
	r.RemoteAddr = "192.0.2.20:10"
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("维护优先级错误", w.Code)
	}
}

func TestAuthorizedSSEKeepsConnectionButCannotRestoreRevokedGrant(t *testing.T) {
	e, s, c := accessEngine(t)
	next := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-next:
			io.WriteString(w, "data: second\n\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
		<-r.Context().Done()
	}))
	defer backend.Close()
	c.Upstreams = []config.Upstream{{ID: "pool", Name: "events", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	c.Sites[0].Routes[0].Action = "proxy"
	c.Sites[0].Routes[0].UpstreamID = "pool"
	c.Sites[0].Routes[0].Streaming = true
	activate(t, e, c)
	grant, err := s.AuthorizeIP("site", "127.0.0.1", 1, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	req, _ := http.NewRequest("GET", server.URL+"/good", nil)
	req.Host = "app.example.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err = io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	updated, ok := e.Access.Check("site", grant.IP)
	if !ok || !updated.LastAccessAt.After(grant.LastAccessAt) {
		t.Fatal("SSE未续期")
	}
	if err = s.RevokeAccess(grant.ID, -1); err != nil {
		t.Fatal(err)
	}
	close(next)
	second := make([]byte, len("data: second\n\n"))
	if _, err = io.ReadFull(resp.Body, second); err != nil {
		t.Fatal("撤销中断了存量SSE", err)
	}
	if _, ok = e.Access.Check("site", grant.IP); ok {
		t.Fatal("SSE复活了撤销记录")
	}
}
func TestAuthorizedWebSocketDoesNotResurrectRevokedIP(t *testing.T) {
	e, s, c := accessEngine(t)
	backend := httptest.NewServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: websocket.Handler(func(ws *websocket.Conn) { defer ws.Close(); io.Copy(ws, ws) })})
	defer backend.Close()
	c.Upstreams = []config.Upstream{{ID: "pool", Name: "socket", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	c.Sites[0].Routes[0].Action = "proxy"
	c.Sites[0].Routes[0].UpstreamID = "pool"
	activate(t, e, c)
	grant, err := s.AuthorizeIP("site", "127.0.0.1", 1, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(e)
	defer server.Close()
	cfg, _ := websocket.NewConfig("ws://app.example.com/good", "http://app.example.com")
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := websocket.NewClient(cfg, conn)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(3 * time.Second))
	echo := func(value string) {
		if err := websocket.Message.Send(ws, value); err != nil {
			t.Fatal(err)
		}
		var result string
		if err := websocket.Message.Receive(ws, &result); err != nil || result != value {
			t.Fatal(err, result)
		}
	}
	echo("before")
	updated, ok := e.Access.Check("site", grant.IP)
	if !ok || !updated.LastAccessAt.After(grant.LastAccessAt) {
		t.Fatal("WS未续期")
	}
	if err = s.RevokeAccess(grant.ID, -1); err != nil {
		t.Fatal(err)
	}
	echo("after")
	if _, ok = e.Access.Check("site", grant.IP); ok {
		t.Fatal("WS复活了撤销授权")
	}
}

func TestHiddenLoginThroughActualHTTPSListener(t *testing.T) {
	e, s, c := accessEngine(t)
	port := freePorts(t, 1)[0]
	c.Sites[0].HTTPPort = 0
	c.Sites[0].HTTPSPort = port
	listeners := NewListeners(e, "127.0.0.1")
	defer listeners.Close()
	applyListeners(t, listeners, c, 1)
	// Dial only rewrites the destination socket, preserving HTTPS, SNI and Host,
	// just as published bridge ports do; no Docker daemon or privileged NAT is used.
	for _, origin := range []string{fmt.Sprintf("https://app.example.com:%d", port), "https://app.example.com", "https://app.example.com:9443"} {
		t.Run(origin, func(t *testing.T) {
			jar, _ := cookiejar.New(nil)
			transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Jar: jar, Timeout: 3 * time.Second}
			resp, err := client.Get(origin + testLoginPath)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || resp.Header.Get("Referrer-Policy") != "same-origin" {
				t.Fatal(resp.StatusCode, string(body))
			}
			match := regexp.MustCompile(`name="csrf"\s+value="([^"]+)"`).FindStringSubmatch(string(body))
			if len(match) != 2 {
				t.Fatal("真实入口缺少 CSRF")
			}
			values := url.Values{"code": {"01234567"}, "csrf": {match[1]}}
			req, _ := http.NewRequest("POST", origin+testLoginPath+"/login", strings.NewReader(values.Encode()))
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			// Changing the public port or omitting Origin must still fail after NAT.
			for _, source := range []string{"", "null", "https://evil.example", "https://app.example.com:12345"} {
				bad, _ := http.NewRequest("POST", origin+testLoginPath+"/login", strings.NewReader(values.Encode()))
				bad.Header.Set("Origin", source)
				bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				bad.Header.Set("X-Forwarded-Host", "app.example.com")
				bad.Header.Set("X-Forwarded-Proto", "https")
				response, err := client.Do(bad)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != 403 {
					t.Fatal("端口映射放宽了来源校验", source, response.StatusCode)
				}
			}
			req.Header.Set("X-Forwarded-For", "198.51.100.99")
			req.Header.Set("Forwarded", "for=198.51.100.99;host=evil.example;proto=http")
			req.Header.Set("X-Forwarded-Host", "evil.example")
			req.Header.Set("X-Forwarded-Port", "1234")
			req.Header.Set("X-Forwarded-Proto", "http")
			resp, err = client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || !strings.Contains(string(body), "当前 IP 已授权") {
				t.Fatal(resp.StatusCode, string(body))
			}
			match = regexp.MustCompile(`name="csrf"\s+value="([^"]+)"`).FindStringSubmatch(string(body))
			if len(match) != 2 {
				t.Fatal("授权后缺少表单上下文")
			}
			values.Set("csrf", match[1])
			resp, err = client.Get(origin + "/good")
			if err != nil {
				t.Fatal(err)
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || string(body) != "private content" {
				t.Fatal("真实HTTPS授权后未放行", resp.StatusCode, string(body))
			}
			if _, ok := s.CheckAccessIP("site", "127.0.0.1", time.Now()); !ok {
				t.Fatal("未记录连接的实际来源 IP")
			}
			if _, ok := s.CheckAccessIP("site", "198.51.100.99", time.Now()); ok {
				t.Fatal("错误信任了转发头")
			}
			// A different public authority cannot reuse this form context.
			otherOrigin := "https://app.example.com:12345"
			other, _ := http.NewRequest("POST", otherOrigin+testLoginPath+"/login", strings.NewReader(values.Encode()))
			other.Header.Set("Origin", otherOrigin)
			other.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			otherResp, err := client.Do(other)
			if err != nil {
				t.Fatal(err)
			}
			otherResp.Body.Close()
			if otherResp.StatusCode != 403 {
				t.Fatal("跨端口复用了登录表单", otherResp.StatusCode)
			}
			logout, _ := http.NewRequest("POST", origin+testLoginPath+"/logout", strings.NewReader(values.Encode()))
			logout.Header.Set("Origin", origin)
			logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			noRedirect := *client
			noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			logoutResp, err := noRedirect.Do(logout)
			if err != nil {
				t.Fatal(err)
			}
			logoutResp.Body.Close()
			if logoutResp.StatusCode != 303 || logoutResp.Header.Get("Location") != testLoginPath {
				t.Fatal("端口映射后的撤销失败", logoutResp.StatusCode)
			}
			if _, ok := s.CheckAccessIP("site", "127.0.0.1", time.Now()); ok {
				t.Fatal("撤销后仍有 IP 授权")
			}
		})
	}
}

func TestSameIPNeedsSeparateAuthorizationOnEachSite(t *testing.T) {
	e, s, c := accessEngine(t)
	other := c.Sites[0]
	other.ID = "site-b"
	other.Name = "站点 B"
	other.HTTPPort = 18081
	other.HTTPSPort = 18444
	other.Routes = append([]config.Route{}, other.Routes...)
	for i := range other.Routes {
		other.Routes[i].ID += "-b"
	}
	c.Sites = append(c.Sites, other)
	activate(t, e, c)
	call := func(port int) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://app.example.com/good", nil)
		r.RemoteAddr = "192.0.2.80:100"
		r = r.WithContext(context.WithValue(r.Context(), listenerContextKey{}, listenerIdentity{Port: port, Protocol: "http"}))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	a, err := s.AuthorizeIP("site", "192.0.2.80", 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w := call(18080); w.Code != 200 {
		t.Fatal("本站未放行", w.Code)
	}
	if w := call(18081); w.Code != 404 {
		t.Fatal("A 站授权串用到 B 站", w.Code)
	}
	account, _ := s.AccessAccount("site")
	if err = s.UpdateAccessAccount("site-b", 0, account.Username, account.PasswordHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeIP("site-b", a.IP, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := call(18081); w.Code != 200 {
		t.Fatal("B 站独立授权失败", w.Code)
	}
	if err = s.RevokeAccess(a.ID, -1); err != nil {
		t.Fatal(err)
	}
	if w := call(18080); w.Code != 404 {
		t.Fatal("A 站撤销失效", w.Code)
	}
	if w := call(18081); w.Code != 200 {
		t.Fatal("撤销 A 站影响 B 站", w.Code)
	}
}
func TestRetiredLoginSubmissionNeverFallsThroughToBusiness(t *testing.T) {
	e, _, c := accessEngine(t)
	c.Sites[0].RequireIPLogin = false
	c.Sites[0].Routes = []config.Route{response("public app")}
	activate(t, e, c)
	r := httptest.NewRequest("POST", "http://app.example.com/old-login/login?_v2way_access=login", strings.NewReader("password=private"))
	r.RemoteAddr = "192.0.2.1:100"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 404 || strings.Contains(w.Body.String(), "public app") {
		t.Fatal("旧登录提交落入业务路由", w.Code, w.Body.String())
	}
}
