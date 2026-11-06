package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"v2way/internal/config"
)

func TestUpstreamSkipTLSVerifyIsOptInAndPoolScoped(t *testing.T) {
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "self-signed backend")
	}))
	backend.Config.ErrorLog = log.New(io.Discard, "", 0)
	backend.StartTLS()
	defer backend.Close()
	c := usageConfig(backend.URL)
	c.Upstreams[0].HealthPath = "/health"
	e := New(nil, nil)
	defer e.Close()
	activate(t, e, c)
	if w := request(e, "GET", "/"); w.Code != 502 {
		t.Fatal("默认允许了不可信后端证书", w.Code)
	}
	c.Upstreams[0].SkipTLSVerify = true
	c.Upstreams = append(c.Upstreams, config.Upstream{ID: "strict", Name: "严格校验", Targets: c.Upstreams[0].Targets})
	for _, path := range []string{"/custom", "/stream", "/strict"} {
		route := c.Sites[0].Routes[0]
		route.ID, route.Path = "route-"+path[1:], path
		switch path {
		case "/custom":
			route.ResponseHeaderTimeoutSeconds = 2
		case "/stream":
			route.Streaming = true
		case "/strict":
			route.UpstreamID = "strict"
		}
		c.Sites[0].Routes = append(c.Sites[0].Routes, route)
	}
	// Ensure the new option survives the portable JSON configuration format.
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var persisted config.Config
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	activate(t, e, persisted)
	for _, path := range []string{"/", "/custom", "/stream"} {
		if w := request(e, "GET", path); w.Code != 200 || w.Body.String() != "self-signed backend" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := request(e, "GET", "/strict"); w.Code != 502 {
		t.Fatal("设置影响了其他后端池", w.Code)
	}
	// Active and recovery probes use the same transport as this direct probe.
	req, _ := http.NewRequest("GET", backend.URL+"/health", nil)
	resp, err := e.Current().pools["u"].transport.RoundTrip(req)
	if err != nil {
		t.Fatal("健康探测未沿用忽略证书设置", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	persisted.Upstreams[0].SkipTLSVerify = false
	activate(t, e, persisted)
	for _, path := range []string{"/", "/custom", "/stream"} {
		if w := request(e, "GET", path); w.Code != 502 {
			t.Fatal("恢复校验未生效", path, w.Code)
		}
	}
}
