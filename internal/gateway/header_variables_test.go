package gateway

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"v2way/internal/config"
)

func TestHeaderVariablesStreamingResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Trace", "backend")
		io.WriteString(w, "data: "+r.Header.Get("X-IP")+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	c.Sites[0].Routes[0].HeaderVariables = true
	c.Sites[0].Routes[0].Streaming = true
	c.Sites[0].Routes[0].RequestHeaders = map[string]string{"X-IP": "${remote_addr}"}
	c.Sites[0].Routes[0].ResponseHeaders = map[string]string{"X-Trace": "${request_id}"}
	activate(t, e, c)
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	r, _ := http.NewRequest("GET", server.URL, nil)
	r.Host = "app.example.com"
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: 127.0.0.1\n" || len(resp.Header.Values("X-Trace")) != 1 || resp.Header.Get("X-Trace") != resp.Header.Get("X-Request-ID") {
		t.Fatal("SSE 被缓冲或响应模板未生效", line, err, resp.Header)
	}
}

func TestHeaderVariablesWebSocketHandshakeAndPublication(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "ws"
		if secure {
			name = "wss"
		}
		t.Run(name, func(t *testing.T) {
			backend := httptest.NewUnstartedServer(websocket.Server{
				Handshake: func(c *websocket.Config, r *http.Request) error {
					c.Header = http.Header{"X-Trace": []string{"backend"}}
					wantScheme := "http"
					if secure {
						wantScheme = "https"
					}
					if r.Header.Get("X-Mode") != "original" || r.Header.Get("X-Scheme") != wantScheme {
						t.Error("WS 请求模板未生效", r.Header)
					}
					return nil
				}, Handler: websocket.Handler(func(ws *websocket.Conn) { defer ws.Close(); io.Copy(ws, ws) }),
			})
			if secure {
				backend.StartTLS()
			} else {
				backend.Start()
			}
			defer backend.Close()
			e, _, c := accessEngine(t)
			c.Sites[0].RequireIPLogin = false
			route := response("")
			route.Action = "proxy"
			route.UpstreamID = "u"
			route.HeaderVariables = true
			route.RequestHeaders = map[string]string{"X-Mode": "original", "X-Scheme": "${scheme}"}
			route.ResponseHeaders = map[string]string{"X-Trace": "${request_id}", "X-Scheme": "${scheme}"}
			c.Sites[0].Routes = []config.Route{route}
			c.Upstreams = []config.Upstream{{ID: "u", Name: "WS", SkipTLSVerify: secure, Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
			activate(t, e, c)
			server := httptest.NewUnstartedServer(e)
			if secure {
				server.TLS = e.TLSConfig(18443)
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			var conn net.Conn
			var err error
			if secure {
				conn, err = tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", server.Listener.Addr().String(), &tls.Config{ServerName: "app.example.com", InsecureSkipVerify: true})
			} else {
				conn, err = net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			req, _ := http.NewRequest("GET", "http://app.example.com/", nil)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			req.Header.Set("Origin", "http://app.example.com")
			if err = req.Write(conn); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			resp, err := http.ReadResponse(reader, req)
			if err != nil {
				t.Fatal(err)
			}
			wantScheme := "http"
			if secure {
				wantScheme = "https"
			}
			if resp.StatusCode != 101 || len(resp.Header.Values("X-Trace")) != 1 || resp.Header.Get("X-Trace") != resp.Header.Get("X-Request-ID") || resp.Header.Get("X-Scheme") != wantScheme {
				t.Fatal("WS 握手响应变量错误", resp.Status, resp.Header)
			}
			for i := 0; i < 2; i++ {
				// One masked client text frame; the server returns an unmasked echo.
				if _, err = conn.Write([]byte{0x81, 0x81, 1, 2, 3, 4, 'x' ^ 1}); err != nil {
					t.Fatal(err)
				}
				var frame [3]byte
				if _, err = io.ReadFull(reader, frame[:]); err != nil || frame != [3]byte{0x81, 1, 'x'} {
					t.Fatal("升级流量中断", frame, err)
				}
				c.Sites[0].Routes[0].RequestHeaders = map[string]string{"X-Mode": "new"}
				activate(t, e, c)
			}
		})
	}
}

func TestHeaderVariablesPreserveAccessCachePolicyAndErrors(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public")
		io.WriteString(w, "ok")
	}))
	defer backend.Close()
	e, s, c := accessEngine(t)
	account, err := s.AccessAccount("site")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeIP("site", "192.0.2.10", account.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	route := &c.Sites[0].Routes[0]
	route.Action = "proxy"
	route.UpstreamID = "u"
	route.HeaderVariables = true
	route.ResponseHeaders = map[string]string{"Cache-Control": "public, max-age=3600", "X-Trace": "${request_id}"}
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	activate(t, e, c)
	r := httptest.NewRequest("GET", "http://app.example.com/good", nil)
	r.RemoteAddr = "192.0.2.10:42000"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("覆盖了强制缓存规则", w.Code, w.Header())
	}
	e.Current().pools["u"].targets[0].healthy.Store(false)
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("X-Trace") != w.Header().Get("X-Request-ID") || len(w.Header().Values("X-Trace")) != 1 {
		t.Fatal("代理错误响应丢失展开 Header", w.Code, w.Header())
	}
	backend.Close()
	activate(t, e, c)
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 502 || w.Header().Get("X-Trace") != w.Header().Get("X-Request-ID") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("后端连接失败时 Header 或强制缓存策略错误", w.Code, w.Header())
	}
}

func TestHeaderContextUsesOriginalRequest(t *testing.T) {
	for _, tc := range []struct{ remote, ip, port string }{
		{"192.0.2.8:45123", "192.0.2.8", "45123"},
		{"[2001:db8::8]:45123", "2001:db8::8", "45123"},
		{"[::ffff:192.0.2.8]:45123", "192.0.2.8", "45123"},
	} {
		r := httptest.NewRequest("POST", "https://APP.example.com:8443/api/%E4%B8%AD?q=secret", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("Forwarded", "for=evil;proto=http")
		r.Header.Set("X-Real-IP", "evil")
		r.Header.Set("X-Forwarded-Proto", "http")
		context := headerContext(r, "request", "site", "route")
		if context["remote_addr"] != tc.ip || context["remote_port"] != tc.port || context["scheme"] != "https" || context["host"] != "app.example.com" || context["http_host"] != "APP.example.com:8443" || context["uri"] != "/api/%E4%B8%AD" {
			t.Fatalf("原始上下文错误：%v", context)
		}
		r.TLS = nil
		if headerContext(r, "request", "site", "route")["scheme"] != "http" {
			t.Fatal("错误信任转发协议")
		}
	}
}

func TestProxyHeaderVariablesAndLegacy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Trace", "backend-one")
		w.Header().Add("X-Trace", "backend-two")
		w.Header().Set("Set-Cookie", "v2way_session=backend; Path=/")
		json.NewEncoder(w).Encode(r.Header)
	}))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	route := &c.Sites[0].Routes[0]
	route.HeaderVariables = true
	route.Path = "/api"
	route.StripPrefix = true
	route.RequestHeaders = map[string]string{"X-Client-IP": "${remote_addr}", "X-Original": "${uri}", "X-Trace": "${request_id}", "X-Context": "${site_id}/${route_id}/${request_method}/${scheme}/${host}/${http_host}/${remote_port}", "Cookie": "v2way_session=constant; app=yes", "X-Literal": "$${remote_addr}"}
	route.ResponseHeaders = map[string]string{"X-Trace": "${request_id}", "Set-Cookie": "v2way_session=template; Path=/"}
	activate(t, e, c)
	r := httptest.NewRequest("GET", "http://APP.example.com:8080/api/users?token=secret", nil)
	r.RemoteAddr = "[::ffff:192.0.2.10]:42000"
	r.Header.Set("X-Forwarded-For", "evil")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	var headers http.Header
	if err := json.Unmarshal(w.Body.Bytes(), &headers); err != nil {
		t.Fatal(err, w.Body.String())
	}
	requestID := w.Header().Get("X-Request-ID")
	if w.Code != 200 || headers.Get("X-Client-IP") != "192.0.2.10" || headers.Get("X-Original") != "/api/users" || headers.Get("X-Literal") != "${remote_addr}" || headers.Get("X-Trace") != requestID || headers.Get("X-Context") != "site/route/GET/http/app.example.com/APP.example.com:8080/42000" {
		t.Fatalf("变量转发错误：%d %v", w.Code, headers)
	}
	if len(w.Header().Values("X-Trace")) != 1 || w.Header().Get("X-Trace") != requestID || e.Logs()[0].ID != requestID {
		t.Fatal("响应覆盖或请求 ID 错误", w.Header())
	}
	if strings.Contains(headers.Get("Cookie"), "v2way_session") || len(w.Result().Cookies()) != 0 {
		t.Fatal("绕过了管理 Cookie 策略")
	}
	// Disabling preserves literal values and the legacy response append behavior.
	route.HeaderVariables = false
	activate(t, e, c)
	w = request(e, "GET", "/api/users")
	json.Unmarshal(w.Body.Bytes(), &headers)
	if headers.Get("X-Client-IP") != "${remote_addr}" || len(w.Header().Values("X-Trace")) != 3 {
		t.Fatal("旧配置行为改变", headers, w.Header())
	}
}

func TestHeaderExpansionFailureDoesNotContactBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("非法展开访问了后端") }))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	c.Sites[0].Routes[0].HeaderVariables = true
	c.Sites[0].Routes[0].RequestHeaders = map[string]string{"X-Limit": strings.Repeat("${http_host}", 300)}
	activate(t, e, c)
	r := httptest.NewRequest("GET", "http://app.example.com/", nil)
	r.Host = "app.example.com:" + strings.Repeat("1", 30)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), r.Host) {
		t.Fatal("展开失败没有安全拒绝", w.Code, w.Body.String())
	}
	c.Sites[0].Routes[0].RequestHeaders = map[string]string{"X-IP": "${remote_addr}"}
	activate(t, e, c)
	r = httptest.NewRequest("GET", "http://app.example.com/", nil)
	r.RemoteAddr = "invalid"
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("缺失来源 IP 被接受", w.Code)
	}
}

func TestHeaderVariablesConcurrentIsolation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Header.Get("X-IP")) }))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	c.Sites[0].Routes[0].HeaderVariables = true
	c.Sites[0].Routes[0].RequestHeaders = map[string]string{"X-IP": "${remote_addr}"}
	activate(t, e, c)
	var wg sync.WaitGroup
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "2001:db8::1", "2001:db8::2"} {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			r := httptest.NewRequest("GET", "http://app.example.com/", nil)
			r.RemoteAddr = net.JoinHostPort(ip, "42000")
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			if w.Body.String() != ip {
				t.Error("请求变量串值", w.Body.String(), ip)
			}
		}(ip)
	}
	wg.Wait()
}
