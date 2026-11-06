package accessgate

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
	"v2way/internal/password"
	"v2way/internal/store"
)

func TestHiddenLoginSourceBindingAndLogout(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hash, err := password.Hash("01234567")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccessAccount("site", 0, "visitor", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	g := New(s)
	defer g.Close()
	c := Entry{SiteID: "site", SiteName: "测试站点", LoginPath: "/__access/" + strings.Repeat("a", 64)}
	origin := "https://app.example.com"
	call := func(method, path, ip string, cookie *http.Cookie, values url.Values, source string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(values.Encode()))
		r.RemoteAddr = ip + ":1234"
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("X-Forwarded-For", "198.51.100.99")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if source != "" {
			r.Header.Set("Origin", source)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		g.Handle(w, r, c)
		return w
	}
	csrf := func(w *httptest.ResponseRecorder) string {
		m := regexp.MustCompile(`name="csrf"\s+value="([^"]+)"`).FindStringSubmatch(w.Body.String())
		if len(m) != 2 {
			t.Fatal("无 CSRF 表单", w.Body.String())
		}
		return m[1]
	}
	first := call("GET", c.LoginPath, "192.0.2.4", nil, nil, "")
	if first.Code != 200 || !strings.Contains(first.Body.String(), "48 小时") {
		t.Fatal(first.Code, first.Body.String())
	}
	if first.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("登录表单必须保留同源 POST 的 Origin", first.Header())
	}
	if strings.Contains(first.Body.String(), `name="username"`) || strings.Contains(first.Body.String(), `type="password"`) || !strings.Contains(first.Body.String(), `name="code"`) || !strings.Contains(first.Body.String(), `type="text"`) || !strings.Contains(first.Body.String(), `inputmode="numeric"`) || !strings.Contains(first.Body.String(), `pattern="[0-9]{8}"`) {
		t.Fatal("授权页必须只有明文数字访问码输入")
	}
	if strings.Contains(first.Body.String(), "ZgotmplZ") {
		t.Fatal("字体模板 URL 无效")
	}
	font := call("HEAD", c.LoginPath+"/fonts/consolas.woff", "192.0.2.4", nil, nil, "")
	if font.Code != 200 || font.Body.Len() != 0 || font.Header().Get("Content-Type") != "font/woff" || font.Header().Get("Content-Length") == "" {
		t.Fatal("字体 HEAD 响应错误", font.Code, font.Header())
	}
	cookie := first.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != c.LoginPath {
		t.Fatal(cookie)
	}
	values := url.Values{"code": {"01234567"}, "csrf": {csrf(first)}}
	for _, source := range []string{"", "null", "https://evil.example", "http://app.example.com", "https://app.example.com:8443"} {
		if w := call("POST", c.LoginPath+"/login", "192.0.2.4", cookie, values, source); w.Code != 403 || !strings.Contains(w.Body.String(), "请求来源校验失败") {
			t.Fatal("无效来源登录未拒绝", source, w.Code)
		}
	}
	if w := call("POST", c.LoginPath+"/login", "192.0.2.5", cookie, values, origin); w.Code != 403 {
		t.Fatal("上下文未绑定 IP", w.Code)
	}
	for _, invalid := range []url.Values{
		{"csrf": {csrf(first)}, "username": {"visitor"}, "password": {"01234567"}},
		{"csrf": {csrf(first)}, "code": {"0123456"}},
		{"csrf": {csrf(first)}, "code": {"01234567", "01234567"}},
	} {
		w := call("POST", c.LoginPath+"/login", "192.0.2.4", cookie, invalid, origin)
		if w.Code != 400 {
			t.Fatal("无效访问码表单未拒绝", w.Code, w.Body.String())
		}
		if _, ok := g.Check("site", "192.0.2.4"); ok {
			t.Fatal("无效访问码获得授权")
		}
	}
	success := call("POST", c.LoginPath+"/login", "192.0.2.4", cookie, values, origin)
	if success.Code != 200 || !strings.Contains(success.Body.String(), "当前 IP 已授权") {
		t.Fatal(success.Code, success.Body.String())
	}
	if _, ok := g.Check("site", "198.51.100.99"); ok {
		t.Fatal("信任了伪造 IP")
	}
	if _, ok := g.Check("site", "192.0.2.4"); !ok {
		t.Fatal("真实 IP 未授权")
	}
	// Another browser on the same NAT may see authorization but cannot revoke it without authentication.
	other := call("GET", c.LoginPath, "192.0.2.4", nil, nil, "")
	otherCookie := other.Result().Cookies()[0]
	if w := call("POST", c.LoginPath+"/logout", "192.0.2.4", otherCookie, url.Values{"csrf": {csrf(other)}}, origin); w.Code != 403 {
		t.Fatal("未认证表单可撤销")
	}
	authCookie := success.Result().Cookies()[0]
	revoked := call("POST", c.LoginPath+"/logout", "192.0.2.4", authCookie, url.Values{"csrf": {csrf(success)}}, origin)
	if revoked.Code != 303 {
		t.Fatal(revoked.Code, revoked.Body.String())
	}
	if _, ok := g.Check("site", "192.0.2.4"); ok {
		t.Fatal("撤销未生效")
	}
}
func TestSourceIPOnlyUsesRemoteAddress(t *testing.T) {
	for _, tt := range []struct{ in, want string }{{"192.0.2.1:100", "192.0.2.1"}, {"[2001:db8::1]:999", "2001:db8::1"}, {"[::ffff:192.0.2.1]:111", "192.0.2.1"}} {
		got, err := SourceIP(tt.in)
		if err != nil || got != tt.want {
			t.Fatal(tt, got, err)
		}
	}
	for _, value := range []string{"bad", "192.0.2.1", "[fe80::1%eth0]:80"} {
		if _, err := SourceIP(value); err == nil {
			t.Fatal("无效地址被接受", value)
		}
	}
}

func TestLoginFormAndGrantAreBoundToSite(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hash, _ := password.Hash("01234567")
	if err = s.UpdateAccessAccount("site-a", 0, "visitor", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccessAccount("site-b", 0, "visitor", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	g := New(s)
	defer g.Close()
	a := Entry{SiteID: "site-a", SiteName: "站点 A", LoginPath: "/access"}
	b := Entry{SiteID: "site-b", SiteName: "站点 B", LoginPath: "/access"}
	origin := "https://app.example.com"
	call := func(entry Entry, method, path string, cookie *http.Cookie, values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(values.Encode()))
		r.TLS = &tls.ConnectionState{}
		r.RemoteAddr = "192.0.2.6:100"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		g.Handle(w, r, entry)
		return w
	}
	token := func(w *httptest.ResponseRecorder) string {
		m := regexp.MustCompile(`name="csrf"\s+value="([^"]+)"`).FindStringSubmatch(w.Body.String())
		if len(m) != 2 {
			t.Fatal(w.Body.String())
		}
		return m[1]
	}
	getA := call(a, "GET", "/access", nil, nil)
	cookieA := getA.Result().Cookies()[0]
	values := url.Values{"code": {"01234567"}, "csrf": {token(getA)}}
	successA := call(a, "POST", "/access/login", cookieA, values)
	if successA.Code != 200 {
		t.Fatal(successA.Code)
	}
	if _, ok := g.Check("site-b", "192.0.2.6"); ok {
		t.Fatal("A 站登录授权了 B 站")
	}
	forged := *cookieA
	forged.Name = cookieName(b.SiteID)
	if w := call(b, "POST", "/access/login", &forged, values); w.Code != 403 {
		t.Fatal("跨站点表单可用", w.Code)
	}
	getB := call(b, "GET", "/access", nil, nil)
	cookieB := getB.Result().Cookies()[0]
	if cookieA.Name == cookieB.Name {
		t.Fatal("不同站点 Cookie 名冲突")
	}
	values.Set("csrf", token(getB))
	successB := call(b, "POST", "/access/login", cookieB, values)
	if successB.Code != 200 {
		t.Fatal(successB.Code, successB.Body.String())
	}
	if _, ok := g.Check("site-a", "192.0.2.6"); !ok {
		t.Fatal("B 站登录替换了 A 站授权")
	}
	if _, ok := g.Check("site-b", "192.0.2.6"); !ok {
		t.Fatal("B 站未授权")
	}
}

func TestLoginOriginUsesPublicAuthority(t *testing.T) {
	for _, tt := range []struct{ host, want string }{
		{"app.example.com", "https://app.example.com"},
		{"APP.example.com:443", "https://app.example.com"},
		{"app.example.com:9443", "https://app.example.com:9443"},
		{"[2001:db8::1]:443", "https://[2001:db8::1]"},
		{"[2001:db8::1]:9443", "https://[2001:db8::1]:9443"},
	} {
		r := httptest.NewRequest("GET", "https://app.example.com/access", nil)
		r.Host = tt.host
		r.Header.Set("X-Forwarded-Host", "evil.example:8080")
		r.Header.Set("X-Forwarded-Proto", "http")
		got, err := loginOrigin(r)
		if err != nil || got != tt.want {
			t.Fatal(tt, got, err)
		}
	}
	for _, host := range []string{"", "evil.example@app.example.com", "app.example.com:bad", "app.example.com:65536", "app.example.com/path", "app.example.com?query=1", "app.example.com#fragment"} {
		r := httptest.NewRequest("GET", "https://app.example.com/access", nil)
		r.Host = host
		if got, err := loginOrigin(r); err == nil {
			t.Fatal("无效 Host 被接受", host, got)
		}
	}
	r := httptest.NewRequest("GET", "http://app.example.com/access", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if _, err := loginOrigin(r); err == nil {
		t.Fatal("转发头不能代替真实 TLS")
	}
}

func TestAccessCodeFormat(t *testing.T) {
	for _, value := range []string{"01234567", "00000000", "99999999"} {
		if !validAccessCode(value) {
			t.Fatal("合法访问码被拒绝", value)
		}
	}
	for _, value := range []string{"", "1234567", "123456789", "1234567a", " 12345678", "12345678 ", "１２３４５６７８", "1e000008", "1234.567"} {
		if validAccessCode(value) {
			t.Fatal("非法访问码被接受", value)
		}
	}
}
