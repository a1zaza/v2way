package gateway

import (
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

func usageConfig(url string) config.Config {
	route := response("")
	route.Action = "proxy"
	route.UpstreamID = "u"
	c := configuration(route)
	c.Upstreams = []config.Upstream{{ID: "u", Name: "后端", Targets: []config.Target{{URL: url, Weight: 1}}}}
	return c
}
func TestUpstreamStatsBodiesPublicationAndRestart(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		io.WriteString(w, "reply:"+string(body))
	}))
	defer backend.Close()
	c := usageConfig(backend.URL)
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	send := func() {
		req := httptest.NewRequest("POST", "http://app.example.com/", strings.NewReader("hello"))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		if w.Code != 200 || w.Body.String() != "reply:hello" {
			t.Error(w.Code, w.Body.String())
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); send() }()
	}
	wg.Wait()
	want := UpstreamStats{Requests: 20, UploadBytes: 100, DownloadBytes: 220}
	if got := e.UpstreamStats()["u"]; got != want {
		t.Fatalf("正文统计错误：%+v", got)
	}
	staged, err := e.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	staged.Close()
	if got := e.UpstreamStats()["u"]; got != want {
		t.Fatal("草稿校验改变了运行统计")
	}
	c.Upstreams[0].Name = "改名后端"
	activate(t, e, c)
	send()
	if got := e.UpstreamStats()["u"]; got != (UpstreamStats{21, 105, 231}) {
		t.Fatalf("发布清空了统计：%+v", got)
	}
	restarted := New(nil, nil)
	defer restarted.Close()
	activate(t, restarted, c)
	if got := restarted.UpstreamStats()["u"]; got != (UpstreamStats{}) {
		t.Fatal("新实例继承了旧进程统计")
	}
	activate(t, e, configuration(response("no pool")))
	activate(t, e, c)
	if got := e.UpstreamStats()["u"]; got != (UpstreamStats{}) {
		t.Fatal("删除后重建的后端池继承了旧计数")
	}
}
func TestUpstreamStatsExcludeProbeAndLocalErrorBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "healthy") }))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	activate(t, e, c)
	probe, _ := http.NewRequest("GET", backend.URL+"/health", nil)
	resp, err := e.Current().pools["u"].transport.RoundTrip(probe)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if e.UpstreamStats()["u"] != (UpstreamStats{}) {
		t.Fatal("探测请求被计入业务统计")
	}
	e.Current().pools["u"].targets[0].healthy.Store(false)
	if w := request(e, "GET", "/"); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if got := e.UpstreamStats()["u"]; got != (UpstreamStats{Requests: 1}) {
		t.Fatalf("错误正文被计入后端流量：%+v", got)
	}
}
func TestUpstreamStatsStreamingVisibleBeforeCompletion(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	c.Sites[0].Routes[0].Streaming = true
	activate(t, e, c)
	server := httptest.NewServer(e)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	req, _ := http.NewRequest("GET", server.URL, nil)
	req.Host = "app.example.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, len("data: 1\n\n"))
	if _, err := io.ReadFull(resp.Body, body); err != nil {
		t.Fatal(err)
	}
	if got := e.UpstreamStats()["u"]; got.Requests != 1 || got.DownloadBytes < uint64(len(body)) {
		t.Fatalf("流式传输结束前未更新统计：%+v", got)
	}
}
func TestUpstreamStatsWebSocketSurvivesPublication(t *testing.T) {
	backend := httptest.NewServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: websocket.Handler(func(ws *websocket.Conn) { defer ws.Close(); io.Copy(ws, ws) })})
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	activate(t, e, c)
	server := httptest.NewServer(e)
	defer server.Close()
	cfg, err := websocket.NewConfig("ws://app.example.com/", "http://app.example.com")
	if err != nil {
		t.Fatal(err)
	}
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
	echo := func(text string) {
		if err := websocket.Message.Send(ws, text); err != nil {
			t.Fatal(err)
		}
		var result string
		if err := websocket.Message.Receive(ws, &result); err != nil || result != text {
			t.Fatal("WS转发失败", err, result)
		}
	}
	echo("before")
	before := e.UpstreamStats()["u"]
	if before.Requests != 1 || before.UploadBytes < 6 || before.DownloadBytes < 6 {
		t.Fatalf("未统计WS隧道：%+v", before)
	}
	activate(t, e, c)
	echo("after")
	after := e.UpstreamStats()["u"]
	if after.Requests != 1 || after.UploadBytes <= before.UploadBytes || after.DownloadBytes <= before.DownloadBytes {
		t.Fatalf("发布后WS流量未继续累计：%+v", after)
	}
}
