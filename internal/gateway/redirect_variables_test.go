package gateway

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedirectVariablesPathsQueriesAndStatuses(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	route := response("")
	route.Action = "redirect"
	route.LocationVariables = true
	for _, status := range []int{301, 302, 307, 308} {
		route.Status = status
		route.Location = "https://new.example.com${request_uri}"
		activate(t, e, configuration(route))
		for _, path := range []string{"/a%20b?x=1&x=2&q=%2B", "/plain", "/empty?"} {
			w := request(e, "GET", path)
			if w.Code != status || w.Header().Get("Location") != "https://new.example.com"+path {
				t.Fatal(w.Code, w.Header(), path)
			}
		}
	}
	route.Status = 302
	route.Location = "/new${uri}${is_args}${args}"
	activate(t, e, configuration(route))
	for _, path := range []string{"/a%20b?x=%2F&x=2", "/plain", "/empty?"} {
		if w := request(e, "GET", path); w.Header().Get("Location") != "/new"+path {
			t.Fatal("查询串未保留", w.Header(), path)
		}
	}
	route.Location = "https://new.example.com/${remote_addr}/${remote_port}?trace=${request_id}&literal=$${uri}&dollar=$$"
	activate(t, e, configuration(route))
	r := httptest.NewRequest("GET", "http://app.example.com/", nil)
	r.RemoteAddr = "[::ffff:192.0.2.7]:4321"
	r.Header.Set("X-Forwarded-For", "203.0.113.1")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	expected := "https://new.example.com/192.0.2.7/4321?trace=" + w.Header().Get("X-Request-ID") + "&literal=${uri}&dollar=$"
	if w.Header().Get("Location") != expected {
		t.Fatal(w.Header(), expected)
	}
	route.Location, route.LocationVariables = "/literal/${unknown}$$", false
	activate(t, e, configuration(route))
	if w := request(e, "GET", "/"); w.Header().Get("Location") != route.Location {
		t.Fatal("关闭模式未保留字面量", w.Header())
	}
	route.Location, route.LocationVariables = "${scheme}://${http_host}${uri}", true
	activate(t, e, configuration(route))
	r = httptest.NewRequest("HEAD", "https://app.example.com/path", nil)
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "127.0.0.1:4321"
	// Request variables are based on the original client connection context.
	context := headerContext(r, "request", "site", "route")
	if context["scheme"] != "https" || context["request_uri"] != "/path" {
		t.Fatal(context)
	}
	if w := request(e, "HEAD", "/path"); w.Code != 302 || w.Body.Len() != 0 || w.Header().Get("Location") != "http://app.example.com/path" {
		t.Fatal("HEAD 重定向错误", w.Code, w.Header(), w.Body.String())
	}
}

func TestRedirectVariablesRejectInvalidExpandedLocations(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	route := response("")
	route.Action = "redirect"
	route.Status = 302
	route.LocationVariables = true
	for _, tc := range []struct{ template, path string }{
		{"${uri}", "//evil.example/path"},
		{"https://example.com${uri}${args}", "/path?%zz"},
		{"https://example.com" + strings.Repeat("${uri}", 100), "/" + strings.Repeat("x", 90)},
	} {
		route.Location = tc.template
		activate(t, e, configuration(route))
		w := request(e, "GET", tc.path)
		if w.Code != 400 || w.Header().Get("Location") != "" {
			t.Fatal("非法展开结果生成跳转", tc, w.Code, w.Header())
		}
	}
	route.Location = "https://example.com/${remote_port}"
	activate(t, e, configuration(route))
	r := httptest.NewRequest("GET", "http://app.example.com/", nil)
	r.RemoteAddr = "192.0.2.1"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal("缺失变量未拒绝", w.Code, w.Header())
	}
}
