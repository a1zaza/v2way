package gateway

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBusinessProxyIsolatesAdminCookie(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("v2way_session"); err == nil {
			t.Error("业务后端收到管理会话")
		}
		if cookie, err := r.Cookie("app"); err != nil || cookie.Value != "keep" {
			t.Error("应用 Cookie 丢失")
		}
		w.Header().Add("Set-Cookie", "app=updated; Path=/")
		w.Header().Add("Set-Cookie", "v2way_session=overwrite; Path=/api/admin/v1")
	}))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	c := usageConfig(backend.URL)
	// Filtering must run after configured request headers as well.
	c.Sites[0].Routes[0].RequestHeaders = map[string]string{"Cookie": "v2way_session=configured; app=keep; v2way_session=duplicate"}
	activate(t, e, c)
	r := httptest.NewRequest("GET", "http://app.example.com/api/admin/v1/probe", nil)
	r.AddCookie(&http.Cookie{Name: "v2way_session", Value: "browser-session"})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || !reflect.DeepEqual(w.Header().Values("Set-Cookie"), []string{"app=updated; Path=/"}) {
		t.Fatalf("响应 Cookie 未隔离: %d %v", w.Code, w.Header())
	}
}

func TestSiteAdminCookieSwitch(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "v2way_session=backend; Path=/api/admin/v1")
		w.Header().Add("Set-Cookie", "app=keep; Path=/")
		w.Write([]byte(r.Header.Get("Cookie")))
	}))
	defer backend.Close()
	e := New(nil, nil)
	defer e.Close()
	// The same ordinary backend must follow only the published site switch.
	for _, forward := range []bool{false, true, false} {
		c := usageConfig(backend.URL)
		c.Sites[0].ForwardAdminCookie = forward
		activate(t, e, c)
		r := httptest.NewRequest("GET", "http://app.example.com/api/admin/v1/probe", nil)
		r.AddCookie(&http.Cookie{Name: "v2way_session", Value: "browser"})
		r.AddCookie(&http.Cookie{Name: "app", Value: "keep"})
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "app=keep") {
			t.Fatalf("应用 Cookie 转发失败: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "v2way_session=browser") != forward {
			t.Fatalf("请求未遵循开关 %v: %s", forward, w.Body.String())
		}
		wantCookies := 1
		if forward {
			wantCookies = 2
		}
		if len(w.Result().Cookies()) != wantCookies {
			t.Fatalf("响应未遵循开关 %v: %v", forward, w.Header())
		}
	}
}

func TestAdminCookieMultipleHeaders(t *testing.T) {
	h := http.Header{}
	h.Add("Cookie", "v2way_session=first; app=keep")
	h.Add("Cookie", "other=yes; v2way_session=second")
	h.Add("Cookie", "v2way_session=third")
	stripAdminRequestCookie(h)
	r := &http.Request{Header: h}
	if _, err := r.Cookie("v2way_session"); err == nil || len(r.Cookies()) != 2 {
		t.Fatalf("多行 Cookie 过滤失败: %v", h)
	}
}
