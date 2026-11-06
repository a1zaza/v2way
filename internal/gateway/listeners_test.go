package gateway

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"v2way/internal/certificate"
	"v2way/internal/config"
)

func freePorts(t *testing.T, n int) []int {
	t.Helper()
	all := []net.Listener{}
	ports := []int{}
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range all {
		l.Close()
	}
	return ports
}
func applyListeners(t *testing.T, m *Listeners, c config.Config, version int64) {
	t.Helper()
	s, err := m.engine.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	s.Version = version
	p, err := m.Prepare(s)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	p.Activate(s)
}
func listenerRequest(t *testing.T, port int, domain string, secure bool) (int, string) {
	t.Helper()
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: domain}, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	scheme := "http"
	if secure {
		scheme = "https"
	}
	r, _ := http.NewRequest("GET", fmt.Sprintf("%s://127.0.0.1:%d/", scheme, port), nil)
	r.Host = domain
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}
func TestListenerSharingIsolationAndRelease(t *testing.T) {
	ports := freePorts(t, 2)
	e := New(nil, nil)
	defer e.Close()
	m := NewListeners(e, "127.0.0.1")
	defer m.Close()
	applyListeners(t, m, config.Empty(), 0)
	if len(m.Info()) != 0 {
		t.Fatal("空实例预设了业务端口")
	}
	c := configuration(response("first"))
	c.Sites[0].HTTPPort = ports[0]
	other := c.Sites[0]
	other.ID = "other"
	other.Name = "其他"
	other.Domains = []string{"other.example.com"}
	other.Routes = []config.Route{response("shared")}
	other.Routes[0].ID = "other-route"
	isolated := c.Sites[0]
	isolated.ID = "isolated"
	isolated.HTTPPort = ports[1]
	isolated.Routes = []config.Route{response("isolated")}
	isolated.Routes[0].ID = "isolated-route"
	c.Sites = append(c.Sites, other, isolated)
	applyListeners(t, m, c, 1)
	if len(m.Info()) != 2 || len(m.Info()[0].Sites)+len(m.Info()[1].Sites) != 3 {
		t.Fatal("同协议端口没有被复用")
	}
	for _, tt := range []struct {
		port       int
		host, body string
	}{{ports[0], "app.example.com", "first"}, {ports[0], "other.example.com", "shared"}, {ports[1], "app.example.com", "isolated"}} {
		status, body := listenerRequest(t, tt.port, tt.host, false)
		if status != 200 || body != tt.body {
			t.Fatalf("端口匹配错误 %d %s", status, body)
		}
	}
	if status, _ := listenerRequest(t, ports[1], "other.example.com", false); status != 404 {
		t.Fatal("跨端口访问未隔离")
	}
	c.Sites = []config.Site{isolated}
	applyListeners(t, m, c, 2)
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ports[0]), time.Second); err == nil {
		conn.Close()
		t.Fatal("无引用端口未关闭")
	}
	if status, body := listenerRequest(t, ports[1], "app.example.com", false); status != 200 || body != "isolated" {
		t.Fatal("删除其他端口影响现有服务")
	}
	applyListeners(t, m, config.Empty(), 3)
	if len(m.Info()) != 0 {
		t.Fatal("删除站点后仍有监听")
	}
}
func TestListenerReservationFailureAndAbort(t *testing.T) {
	ports := freePorts(t, 2)
	e := New(nil, nil)
	defer e.Close()
	m := NewListeners(e, "127.0.0.1")
	defer m.Close()
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ports[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	c := configuration(response("candidate"))
	c.Sites[0].HTTPPort = ports[1]
	s, err := e.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p, err := m.Prepare(s); err == nil {
		p.Abort()
		t.Fatal("占用端口被接受")
	}
	if e.Current().Version != 0 || len(m.Info()) != 0 {
		t.Fatal("预占失败修改了运行状态")
	}
	c.Sites[0].HTTPPort = ports[0]
	s2, err := e.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	p, err := m.Prepare(s2)
	if err != nil {
		t.Fatal(err)
	}
	p.Abort()
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ports[0]))
	if err != nil {
		t.Fatal("撤销未释放预占端口")
	}
	l.Close()
}
func TestHTTPSRejectHandshakeOnEveryPort(t *testing.T) {
	ports := freePorts(t, 3)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	names := []string{"app.example.com", "other.example.com", "isolated.example.com", "http.example.com", "disabled.example.com"}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	private, _ := x509.MarshalPKCS8PrivateKey(key)
	certs, err := certificate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer certs.Automation().Close()
	info, err := certs.Upload("TLS", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})))
	if err != nil {
		t.Fatal(err)
	}
	e := New(nil, certs)
	defer e.Close()
	m := NewListeners(e, "127.0.0.1")
	defer m.Close()
	c := configuration(response("secure"))
	c.Sites[0].HTTPPort = 0
	c.Sites[0].HTTPS = true
	c.Sites[0].HTTPSPort = ports[0]
	c.Sites[0].CertificateID = info.ID
	for i, name := range names[1:] {
		s := c.Sites[0]
		s.ID = fmt.Sprintf("site%d", i)
		s.Domains = []string{name}
		s.Routes = []config.Route{response(name)}
		s.Routes[0].ID = fmt.Sprintf("route%d", i)
		if i == 1 {
			s.HTTPSPort = ports[1]
		}
		if i == 2 {
			s.HTTPPort = ports[2]
			s.HTTPS = false
			s.HTTPSPort = 0
		}
		if i == 3 {
			s.Enabled = false
		}
		c.Sites = append(c.Sites, s)
	}
	applyListeners(t, m, c, 1)
	if status, body := listenerRequest(t, ports[0], names[0], true); status != 200 || body != "secure" {
		t.Fatal("有效 TLS 请求失败")
	}
	if status, body := listenerRequest(t, ports[0], names[1], true); status != 200 || body != names[1] {
		t.Fatal("HTTPS 共享端口失败")
	}
	if status, _ := listenerRequest(t, ports[1], names[2], true); status != 200 {
		t.Fatal("第二个 TLS 端口失败")
	}
	for _, host := range []string{"", "unknown.example.com", names[2], names[3], names[4]} {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", fmt.Sprintf("127.0.0.1:%d", ports[0]), &tls.Config{InsecureSkipVerify: true, ServerName: host, ClientSessionCache: tls.NewLRUClientSessionCache(2)})
		if err == nil {
			conn.Close()
			t.Fatalf("不应允许的 SNI %q 握手成功", host)
		}
	}
	// Maintenance retains TLS listeners and valid SNI, while normal peers stay live.
	c.Sites[0].Maintenance = true
	c.Sites[0].HTTPPort = ports[2]
	c.Sites[0].RedirectHTTPS = true
	applyListeners(t, m, c, 2)
	if status, _ := listenerRequest(t, ports[2], names[0], false); status != 503 {
		t.Fatal("维护中的 HTTP 请求不应跳转 HTTPS", status)
	}
	if status, body := listenerRequest(t, ports[0], names[0], true); status != 503 || !strings.Contains(body, "站点维护中") {
		t.Fatal("HTTPS 维护页未生效", status, body)
	}
	if status, _ := listenerRequest(t, ports[0], names[1], true); status != 200 {
		t.Fatal("维护影响共享端口的其他站点", status)
	}
	c.Sites[0].Maintenance = false
	c.Sites[0].HTTPPort = 0
	c.Sites[0].RedirectHTTPS = false
	applyListeners(t, m, c, 3)
	if status, body := listenerRequest(t, ports[0], names[0], true); status != 200 || body != "secure" {
		t.Fatal("HTTPS 站点未恢复原配置", status, body)
	}
	// A valid SNI may not route another site's HTTP Host.
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: names[0]}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: time.Second}
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d/", ports[0]), nil)
	req.Host = names[1]
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 421 {
		t.Fatal("跨 SNI Host 未拒绝")
	}
	// Protocol switching needs a separate removal publication, so it cannot steal an active socket.
	switched := config.Config{Sites: []config.Site{c.Sites[0]}}
	switched.Sites[0].HTTPS = false
	switched.Sites[0].HTTPSPort = 0
	switched.Sites[0].HTTPPort = ports[0]
	s, err := e.Compile(switched)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p, err := m.Prepare(s); err == nil {
		p.Abort()
		t.Fatal("在用端口协议被直接切换")
	}
}

func TestRemovedListenerKeepsExistingSSE(t *testing.T) {
	next := make(chan struct{})
	finished := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-next:
			io.WriteString(w, "data: after-removal\n\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
		}
		close(finished)
	}))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	m := NewListeners(e, "127.0.0.1")
	defer m.Close()
	port := freePorts(t, 1)[0]
	route := response("")
	route.Action = "proxy"
	route.UpstreamID = "u"
	route.Streaming = true
	c := configuration(route)
	c.Sites[0].HTTPPort = port
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	applyListeners(t, m, c, 1)
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req.Host = "app.example.com"
	r, err := client.Do(req)
	if err != nil {
		close(next)
		t.Fatal(err)
	}
	defer r.Body.Close()
	reader := bufio.NewReader(r.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: first\n" {
		close(next)
		t.Fatal("SSE 首条数据不可用")
	}
	reader.ReadString('\n')
	applyListeners(t, m, config.Empty(), 2)
	close(next)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: after-removal\n" {
		t.Fatalf("删除监听中断已有 SSE: %q %v", line, err)
	}
	<-finished
}
