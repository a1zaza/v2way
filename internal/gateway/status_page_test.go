package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"v2way/internal/config"
)

func TestGatewayNotFoundPages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/index.html", []byte("home"), 0600); err != nil {
		t.Fatal(err)
	}
	e := New([]string{root}, nil)
	defer e.Close()
	c := configuration(response("private"))
	c.Sites[0].Routes[0].Match = "exact"
	c.Sites[0].Routes[0].Path = "/exists"
	activate(t, e, c)
	for _, host := range []string{"app.example.com", "unknown.example.com"} {
		r := httptest.NewRequest("GET", "http://"+host+"/missing?secret=test", nil)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "页面未找到") || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("404 内容错误：%d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal(w.Header())
		}
	}
	r := httptest.NewRequest("GET", "http://app.example.com/missing", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data["status"] != float64(404) || data["requestId"] != w.Header().Get("X-Request-ID") {
		t.Fatal(err, data)
	}
	w = request(e, "HEAD", "/missing")
	if w.Code != 404 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	c.Sites[0].Routes[0] = response("")
	c.Sites[0].Routes[0].Action = "static"
	c.Sites[0].Routes[0].Root = root
	activate(t, e, c)
	if w := request(e, "GET", "/missing.js"); w.Code != 404 || !strings.Contains(w.Body.String(), "页面未找到") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMaintenancePreservesConfigurationAndRestoresService(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	c := configuration(response("original service"))
	c.Sites[0].Maintenance = true
	// JSON persistence must retain the new state and all existing route fields.
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var persisted config.Config
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	activate(t, e, persisted)
	for _, method := range []string{"GET", "POST", "HEAD"} {
		w := request(e, method, "/anything")
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "300" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Header())
		}
		if method == "HEAD" {
			if w.Body.Len() != 0 {
				t.Fatal("HEAD 返回正文")
			}
		} else if !strings.Contains(w.Body.String(), "站点维护中") || strings.Contains(w.Body.String(), "original service") {
			t.Fatal(w.Body.String())
		}
	}
	site, route := e.Current().Match("app.example.com", "/", "GET")
	if site == nil || !site.Maintenance || route != nil || len(site.Routes) != 1 {
		t.Fatal("推演未反映维护状态或配置丢失")
	}
	// Policies still restrict access to the maintenance page.
	persisted.Sites[0].DenyCIDRs = []string{"127.0.0.0/8"}
	activate(t, e, persisted)
	if w := request(e, "GET", "/"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	persisted.Sites[0].DenyCIDRs = nil
	persisted.Sites[0].Maintenance = false
	activate(t, e, persisted)
	if w := request(e, "GET", "/"); w.Code != 200 || w.Body.String() != "original service" {
		t.Fatal("恢复时丢失原配置", w.Code, w.Body.String())
	}
}
