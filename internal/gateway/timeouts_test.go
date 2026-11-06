package gateway

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"v2way/internal/config"
)

func timeoutProxy(path string) config.Route {
	r := response("")
	r.ID = strings.Trim(path, "/")
	r.Path, r.Match, r.Action, r.UpstreamID = path, "exact", "proxy", "u"
	r.TimeoutSeconds = 5
	return r
}
func TestRouteResponseHeaderTimeoutIsolation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(1200 * time.Millisecond):
			io.WriteString(w, "ready")
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	fast, slow := timeoutProxy("/fast"), timeoutProxy("/slow")
	fast.ResponseHeaderTimeoutSeconds, slow.ResponseHeaderTimeoutSeconds = 1, 3
	slow.ConnectTimeoutSeconds, slow.TLSHandshakeTimeoutSeconds = 2, 4
	c := configuration(fast)
	c.Sites[0].Routes = []config.Route{fast, slow}
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	p := e.Current().pools["u"]
	tr := p.customTransports[routeTimeouts(slow)]
	if tr == nil || tr.TLSHandshakeTimeout != 4*time.Second || tr.ResponseHeaderTimeout != 3*time.Second || routeTimeouts(slow).connect != 2*time.Second {
		t.Fatal("路由阶段超时未应用")
	}
	if p.transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatal("修改了共享默认超时")
	}
	if w := request(e, "GET", "/fast"); w.Code != 504 {
		t.Fatalf("响应头超时未返回504：%d", w.Code)
	}
	if w := request(e, "GET", "/slow"); w.Code != 200 || w.Body.String() != "ready" {
		t.Fatalf("不同超时路由相互影响：%d %s", w.Code, w.Body.String())
	}
}
func TestTLSHandshakeTimeoutReturns504(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	defer close(done)
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-done
	}()
	r := timeoutProxy("/tls")
	r.TLSHandshakeTimeoutSeconds = 1
	c := configuration(r)
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: "https://" + listener.Addr().String(), Weight: 1}}}}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	if w := request(e, "GET", "/tls"); w.Code != 504 {
		t.Fatalf("TLS握手超时未返回504：%d %s", w.Code, w.Body.String())
	}
}
func TestStreamingBypassesTotalTimeoutAfterHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: start\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(1200 * time.Millisecond):
			io.WriteString(w, "data: end\n\n")
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	regular, stream := timeoutProxy("/regular"), timeoutProxy("/stream")
	regular.TimeoutSeconds, stream.TimeoutSeconds = 1, 1
	stream.Streaming = true
	c := configuration(regular)
	c.Sites[0].Routes = []config.Route{regular, stream}
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: backend.URL, Weight: 1}}}}
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = 4 * time.Second
	for _, path := range []string{"/regular", "/stream"} {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		req.Host = "app.example.com"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if path == "/regular" {
			if err == nil {
				t.Fatal("普通请求未受总超时限制")
			}
		} else if err != nil || !strings.Contains(string(body), "data: end") {
			t.Fatalf("流式响应被总超时中断：%s %v", body, err)
		}
	}
}
