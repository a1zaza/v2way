package admin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"v2way/internal/config"
)

func TestManagementProxyRetainsLoginSession(t *testing.T) {
	h := setup(t)
	backend := httptest.NewServer(h.handler)
	defer backend.Close()
	c := config.Config{Sites: []config.Site{{ID: "site", Name: "管理站点", ForwardAdminCookie: true, Domains: []string{"app.example.com"}, Enabled: true, HTTPPort: 18080, MaxBodyMiB: 1, Routes: []config.Route{{ID: "route", Path: "/", Match: "prefix", Action: "proxy", UpstreamID: "u", PreserveHost: true}}}}, Upstreams: []config.Upstream{{ID: "u", Name: "管理后端", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}}
	snap, err := h.engine.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	h.engine.Activate(snap)
	login := httptest.NewRequest("POST", "http://app.example.com/api/admin/v1/session", strings.NewReader(`{"username":"admin","password":"testing-long-password"}`))
	login.RemoteAddr = "127.0.0.1:1234"
	login.Header.Set("Origin", "http://app.example.com")
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, login)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("管理代理登录失败: %d %s", w.Code, w.Body.String())
	}
	var auth session
	if err := json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	for _, method := range []string{"GET", "DELETE"} {
		r := httptest.NewRequest(method, "http://app.example.com/api/admin/v1/session", nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", "http://app.example.com")
		r.Header.Set("X-CSRF-Token", auth.CSRF)
		out := httptest.NewRecorder()
		h.engine.ServeHTTP(out, r)
		if out.Code != 200 {
			t.Fatalf("管理代理 %s 失败: %d %s", method, out.Code, out.Body.String())
		}
		if method == "DELETE" && len(out.Result().Cookies()) != 1 {
			t.Fatal("退出 Cookie 被过滤")
		}
	}
}

func TestLoginBodyLimit(t *testing.T) {
	h := setup(t)
	if w := h.call("POST", "/session", map[string]string{"username": "admin", "password": strings.Repeat("x", 8192)}, false); w.Code != 400 {
		t.Fatalf("过大登录请求未拒绝: %d", w.Code)
	}
}

type observedBody struct {
	io.Reader
	entered chan struct{}
}

func (b *observedBody) Read(p []byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	return b.Reader.Read(p)
}

func TestConcurrentLoginBodiesAreBounded(t *testing.T) {
	h := setup(t)
	done := make(chan struct{}, cap(h.handler.loginSlots))
	for i := 0; i < cap(h.handler.loginSlots); i++ {
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		entered := make(chan struct{}, 1)
		r := httptest.NewRequest("POST", "http://localhost:4033/api/admin/v1/session", &observedBody{reader, entered})
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1)
		r.Header.Set("Origin", "http://localhost:4033")
		go func() { defer func() { done <- struct{}{} }(); h.handler.ServeHTTP(httptest.NewRecorder(), r) }()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("未开始读取正文")
		}
	}
	w := h.call("POST", "/session", map[string]string{}, false)
	if w.Code != 429 {
		t.Fatalf("并发登录未限流: %d", w.Code)
	}
	// Deferred pipe closes release blocked handlers even on assertion failure.
	t.Cleanup(func() {
		for i := 0; i < cap(h.handler.loginSlots); i++ {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("登录处理器未释放")
				return
			}
		}
	})
}

func TestManagementHTTPServerExpiresSlowBody(t *testing.T) {
	h := setup(t)
	s := httptest.NewUnstartedServer(h.handler)
	s.Config = HTTPServer("", h.handler)
	if s.Config.ReadTimeout <= 0 || s.Config.ReadTimeout > 30*time.Second {
		t.Fatal("管理正文期限无效")
	}
	// Keep the real HTTP server behavior, shorten only the deadline for this test.
	s.Config.ReadTimeout = 150 * time.Millisecond
	s.Start()
	defer s.Close()
	conn, err := net.DialTimeout("tcp", s.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(conn, "POST /api/admin/v1/session HTTP/1.1\r\nHost: localhost:4033\r\nOrigin: http://localhost:4033\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{\"username\":")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("慢正文未终止: %d", resp.StatusCode)
	}
}
