package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"

	"v2way/internal/headertemplate"
)

func TestBodyVariablesRequestContextAndPublication(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	route := response("ip=${remote_addr};port=${remote_port};scheme=${scheme};host=${host};raw=${http_host}\nmethod=${request_method};uri=${uri};request=${request_id};site=${site_id};route=${route_id};$${unknown};$$")
	route.BodyVariables = true
	activate(t, e, configuration(route))
	r := httptest.NewRequest("GET", "http://app.example.com/a%20b?secret=hidden", nil)
	r.Host = "app.example.com:18080"
	r.RemoteAddr = "[::ffff:192.0.2.9]:4321"
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	want := "ip=192.0.2.9;port=4321;scheme=http;host=app.example.com;raw=app.example.com:18080\nmethod=GET;uri=/a%20b;request=" + w.Header().Get("X-Request-ID") + ";site=site;route=route;${unknown};$"
	if w.Code != 200 || w.Body.String() != want || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal(w.Code, w.Body.String(), want)
	}
	logs := e.Logs()
	if len(logs) != 1 || logs[0].ID != w.Header().Get("X-Request-ID") || logs[0].Bytes != int64(len(want)) {
		t.Fatal("日志请求标识或响应字节不一致", logs)
	}
	route.Body, route.BodyVariables = "${unknown}\n$$", false
	activate(t, e, configuration(route))
	if w := request(e, "GET", "/"); w.Body.String() != route.Body {
		t.Fatal("关闭变量未原文返回", w.Body.String())
	}
	route.Body, route.BodyVariables = "新版本 ${route_id}", true
	activate(t, e, configuration(route))
	if w := request(e, "GET", "/"); w.Body.String() != "新版本 route" {
		t.Fatal("发布后模板未更新", w.Body.String())
	}
	if w := request(e, "HEAD", "/"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD 返回正文", w.Code, w.Body.String())
	}
	route.Status = 304
	activate(t, e, configuration(route))
	if w := request(e, "GET", "/"); w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal("304 返回正文")
	}
}

func TestBodyVariablesExpansionFailureIsAtomic(t *testing.T) {
	e := New(nil, nil)
	defer e.Close()
	route := response(strings.Repeat("${uri}", 100))
	route.BodyVariables = true
	activate(t, e, configuration(route))
	w := request(e, "GET", "/"+strings.Repeat("x", headertemplate.MaxBodyBytes/100))
	if w.Code != 400 || strings.Contains(w.Body.String(), "xxx") {
		t.Fatal("超限展开未拒绝或泄漏正文", w.Code)
	}
	route.Body = "${remote_port}"
	activate(t, e, configuration(route))
	r := httptest.NewRequest("GET", "http://app.example.com/", nil)
	r.RemoteAddr = "192.0.2.1"
	w = httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), "remote_port") {
		t.Fatal("不可用变量处理错误", w.Code, w.Body.String())
	}
}
