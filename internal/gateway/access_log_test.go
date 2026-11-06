package gateway

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestAccessLogHTTPBodyBytes(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	body := "普通响应正文"
	activate(t, e, configuration(response(body)))
	if w := request(e, "GET", "/"); w.Code != 200 || w.Body.String() != body {
		t.Fatalf("普通响应错误：%d %q", w.Code, w.Body.String())
	}
	if logs := e.Logs(); len(logs) != 1 || logs[0].Bytes != int64(len(body)) {
		t.Fatalf("普通响应体积错误：%+v", logs)
	}
}

func TestTrackedConnCloseIncludesPartialWrite(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	e := New(nil, nil)
	defer e.Close()
	var counter atomic.Int64
	conn := &trackedConn{Conn: left, engine: e, bytes: &counter}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.Write(make([]byte, 128))
	}()
	right.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := right.Read(make([]byte, 17)); err != nil || n != 17 {
		t.Fatalf("未收到部分写入：%d %v", n, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if got := counter.Load(); got != 17 {
		t.Fatalf("连接关闭时遗漏或重复计入写入字节：%d", got)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("连接关闭后写入未退出")
	}
}

func TestAccessLogWebSocketDownloadBytes(t *testing.T) {
	for _, secureBackend := range []bool{false, true} {
		name := "ws"
		if secureBackend {
			name = "wss-backend"
		}
		t.Run(name, func(t *testing.T) {
			backend := httptest.NewUnstartedServer(websocket.Server{
				Handshake: func(*websocket.Config, *http.Request) error { return nil },
				Handler: websocket.Handler(func(ws *websocket.Conn) {
					defer ws.Close()
					io.Copy(ws, ws)
				}),
			})
			if secureBackend {
				backend.StartTLS()
			} else {
				backend.Start()
			}
			defer backend.Close()
			e := New(nil, nil)
			defer e.Close()
			c := usageConfig(backend.URL)
			c.Upstreams[0].SkipTLSVerify = secureBackend
			activate(t, e, c)
			server := httptest.NewServer(e)
			defer server.Close()
			cfg, err := websocket.NewConfig("ws://app.example.com/", "http://app.example.com")
			if err != nil {
				t.Fatal(err)
			}
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
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
			var want int64
			for _, payload := range []string{"发布前的消息", strings.Repeat("x", 64)} {
				if err := websocket.Message.Send(ws, payload); err != nil {
					t.Fatal(err)
				}
				var got string
				if err := websocket.Message.Receive(ws, &got); err != nil || got != payload {
					t.Fatalf("WebSocket 转发失败：%q %v", got, err)
				}
				// These short server frames contain a two-byte header, without a mask.
				want += int64(len(payload) + 2)
				if len(e.Logs()) != 0 {
					t.Fatal("升级连接未结束即写入最终访问日志")
				}
				activate(t, e, c)
			}
			ws.Close()
			deadline := time.Now().Add(2 * time.Second)
			for {
				logs := e.Logs()
				if len(logs) > 0 {
					entry := logs[0]
					// A close control frame may also reach the client before shutdown.
					if len(logs) != 1 || entry.Status != 101 || entry.Bytes < want || entry.Bytes > want+4 {
						t.Fatalf("访问日志下行体积错误：%+v，预期 %d–%d 字节", logs, want, want+4)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("WebSocket 关闭后缺少访问日志")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
