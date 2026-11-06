package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"v2way/internal/certificate"
	"v2way/internal/config"
	"v2way/internal/gateway"
	"v2way/internal/store"
)

type harness struct {
	handler *Server
	store   *store.Store
	engine  *gateway.Engine
	cookie  *http.Cookie
	csrf    string
}

func setup(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = Initialize(s, "admin", "testing-long-password"); err != nil {
		t.Fatal(err)
	}
	certs, err := certificate.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := gateway.New(nil, certs)
	t.Cleanup(e.Close)
	h := &harness{handler: New(s, e, certs, Options{}), store: s, engine: e}
	w := h.call("POST", "/session", map[string]any{"username": "admin", "password": "testing-long-password"}, false)
	if w.Code != 200 {
		t.Fatalf("登录失败: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("会话 Cookie 不安全")
	}
	h.cookie = cookies[0]
	var auth session
	json.Unmarshal(w.Body.Bytes(), &auth)
	h.csrf = auth.CSRF
	return h
}
func (h *harness) call(method, path string, value any, authenticated bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if value != nil {
		json.NewEncoder(&body).Encode(value)
	}
	r := httptest.NewRequest(method, "http://localhost:4033/api/admin/v1"+path, &body)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Origin", "http://localhost:4033")
	if authenticated {
		r.AddCookie(h.cookie)
		r.Header.Set("X-CSRF-Token", h.csrf)
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndCSRF(t *testing.T) {
	h := setup(t)
	if w := h.call("GET", "/overview", nil, false); w.Code != 401 {
		t.Fatalf("匿名访问未拒绝: %d", w.Code)
	}
	r := httptest.NewRequest("PUT", "http://localhost:4033/api/admin/v1/draft", strings.NewReader(`{}`))
	r.AddCookie(h.cookie)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-CSRF-Token", h.csrf)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("接受了跨站写入")
	}
	if w = h.call("GET", "/overview", nil, true); w.Code != 200 {
		t.Fatal("有效会话被拒绝")
	}
	if w = h.call("DELETE", "/session", nil, true); w.Code != 200 {
		t.Fatal("退出失败")
	}
	if w = h.call("GET", "/overview", nil, true); w.Code != 401 {
		t.Fatal("退出后会话仍有效")
	}
}
func TestPublishIdempotencyRollbackAndFailedValidation(t *testing.T) {
	h := setup(t)
	c := config.Config{Sites: []config.Site{{ID: "site", Name: "站点", Domains: []string{"app.example.com"}, Enabled: true, HTTPPort: 18080, MaxBodyMiB: 20, Routes: []config.Route{{ID: "route", Path: "/", Match: "prefix", Action: "response", Status: 200, Body: "v1"}}}}, Upstreams: []config.Upstream{}}
	w := h.call("PUT", "/draft", store.Draft{Config: c, Revision: 0}, true)
	if w.Code != 200 {
		t.Fatalf("保存失败 %d %s", w.Code, w.Body.String())
	}
	if h.engine.Current().Version != 0 {
		t.Fatal("保存草稿改变了业务配置")
	}
	if w = h.call("PUT", "/draft", store.Draft{Config: c, Revision: 0}, true); w.Code != 409 {
		t.Fatal("丢失并发编辑冲突")
	}
	publish := map[string]any{"revision": 1, "requestId": "first-request", "note": "首版", "expectedActiveVersion": 0}
	w = h.call("POST", "/releases", publish, true)
	if w.Code != 200 {
		t.Fatalf("发布失败 %d %s", w.Code, w.Body.String())
	}
	var first store.Version
	json.Unmarshal(w.Body.Bytes(), &first)
	w = h.call("POST", "/releases", publish, true)
	var again store.Version
	json.Unmarshal(w.Body.Bytes(), &again)
	if w.Code != 200 || again.ID != first.ID {
		t.Fatal("幂等请求产生重复发布")
	}
	c.Sites[0].Routes[0].Body = "v2"
	w = h.call("PUT", "/draft", store.Draft{Config: c, Revision: 1}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = h.call("POST", "/releases", map[string]any{"revision": 2, "requestId": "second-request", "expectedActiveVersion": first.ID}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var second store.Version
	json.Unmarshal(w.Body.Bytes(), &second)
	w = h.call("POST", "/releases", map[string]any{"revision": 2, "requestId": "rollback-request", "rollbackVersion": first.ID, "expectedActiveVersion": second.ID}, true)
	if w.Code != 200 {
		t.Fatalf("回滚失败: %s", w.Body.String())
	}
	active, err := h.store.Active()
	if err != nil || active.Config.Sites[0].Routes[0].Body != "v1" || active.ID <= first.ID {
		t.Fatal("回滚未产生新版本")
	}
	c.Sites[0].Routes[0].Action = "proxy"
	c.Sites[0].Routes[0].UpstreamID = "missing"
	h.call("PUT", "/draft", store.Draft{Config: c, Revision: 2}, true)
	old := h.engine.Current().Version
	w = h.call("POST", "/releases", map[string]any{"revision": 3, "requestId": "invalid-request", "expectedActiveVersion": active.ID}, true)
	if w.Code != 422 || h.engine.Current().Version != old {
		t.Fatal("无效发布影响数据面")
	}
	w = h.call("GET", "/export", nil, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "testing-long-password") {
		t.Fatal("导出包含账户秘密")
	}
}
func TestPublicationPersistenceFailureRestoresSnapshot(t *testing.T) {
	h := setup(t)
	if err := os.Mkdir(filepath.Join(h.store.Dir(), "gateway.json"), 0700); err != nil {
		t.Fatal(err)
	}
	w := h.call("POST", "/releases", map[string]any{"revision": 0, "requestId": "failure-request", "expectedActiveVersion": 0}, true)
	if w.Code != 500 || h.engine.Current().Version != 0 {
		t.Fatalf("失败后没有恢复: %d %d", w.Code, h.engine.Current().Version)
	}
	active, err := h.store.Active()
	if err != nil || active.ID != 0 {
		t.Fatal("失败发布改变持久化指针")
	}
}
func TestPasswordChangeRevokesSession(t *testing.T) {
	h := setup(t)
	w := h.call("POST", "/password", map[string]string{"current": "testing-long-password", "password": "another-long-password"}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = h.call("GET", "/overview", nil, true); w.Code != 401 {
		t.Fatal("密码变更后会话未撤销")
	}
}
func TestAdminHTTPAddress(t *testing.T) {
	for _, address := range []string{"0.0.0.0:4033", "192.168.1.10:4033", "[::]:4033", "127.0.0.1:4033"} {
		if err := ValidateAdminAddress(address); err != nil {
			t.Fatal(err)
		}

	}
	for _, address := range []string{"127.0.0.1:0", "0.0.0.0:65536", "0.0.0.0:-1", "127.0.0.1:abc"} {
		if ValidateAdminAddress(address) == nil {
			t.Fatal("非法管理端口被接受")
		}
	}
}
func TestACMERoutesRequireLoginAndNeverReturnCredentials(t *testing.T) {
	h := setup(t)
	request := certificate.IssueRequest{Name: "自动证书", Domains: []string{"app.example.com"}, Email: "admin@example.com", Challenge: "dns-01", Environment: "staging", DNSProvider: "cloudflare", Credentials: map[string]string{"apiToken": "never-return-this-secret"}, AcceptTerms: true, AutoRenew: true}
	if w := h.call("POST", "/acme/issue", request, false); w.Code != 401 {
		t.Fatal("匿名用户创建了签发任务")
	}
	if w := h.call("GET", "/acme/providers", nil, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := h.call("POST", "/acme/issue", request, true)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var j certificate.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	w = h.call("GET", "/acme/jobs", nil, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "never-return-this-secret") || strings.Contains(w.Body.String(), "credentials") {
		t.Fatal("任务接口暴露了凭据")
	}
	if w = h.call("POST", "/certificates/"+j.CertificateID+"/renew", map[string]any{}, true); w.Code != 422 {
		t.Fatal("允许同一证书并发签发")
	}
}

func TestJSONImportDoesNotPublishAndSessionRestart(t *testing.T) {
	h := setup(t)
	d, _ := h.store.Draft()
	raw := `{"sites":[],"upstreams":[],"sites":[]}`
	w := h.call("POST", "/draft/import", map[string]any{"revision": d.Revision, "document": raw}, true)
	if w.Code != 422 {
		t.Fatalf("重复键未被拒绝: %d", w.Code)
	}
	raw = `{"sites":[],"upstreams":[]}`
	w = h.call("POST", "/draft/import", map[string]any{"revision": d.Revision, "document": raw}, true)
	if w.Code != 200 {
		t.Fatalf("导入失败: %d %s", w.Code, w.Body.String())
	}
	saved, _ := h.store.Draft()
	active, _ := h.store.Active()
	if saved.Revision != d.Revision+1 || active.ID != 0 || h.engine.Current().Version != 0 {
		t.Fatal("导入意外发布")
	}
	w = h.call("POST", "/draft/import", map[string]any{"revision": d.Revision, "document": raw}, true)
	if w.Code != 409 {
		t.Fatal("导入未检查草稿修订")
	}
	h.handler = New(h.store, h.engine, h.handler.certs, Options{})
	if w = h.call("GET", "/overview", nil, true); w.Code != 401 {
		t.Fatal("新实例接受旧内存会话")
	}
	if w = h.call("POST", "/session", map[string]any{"username": "admin", "password": "testing-long-password"}, false); w.Code != 200 {
		t.Fatal("重启后账号无法登录")
	}
}

func TestPortPublicationIsTransactional(t *testing.T) {
	h := setup(t)
	m := gateway.NewListeners(h.engine, "127.0.0.1")
	t.Cleanup(m.Close)
	h.handler.options.Listeners = m
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := occupied.Addr().(*net.TCPAddr).Port
	c := config.Config{Sites: []config.Site{{ID: "site", Name: "站点", Enabled: true, HTTPPort: port, Domains: []string{"app.example.com"}, MaxBodyMiB: 20, Routes: []config.Route{{ID: "route", Match: "prefix", Path: "/", Action: "response", Status: 200, Body: "published"}}}}}
	w := h.call("PUT", "/draft", store.Draft{Config: c, Revision: 0}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	release := map[string]any{"revision": 1, "requestId": "port-publication", "expectedActiveVersion": 0}
	w = h.call("POST", "/releases", release, true)
	active, _ := h.store.Active()
	if w.Code != 422 || active.ID != 0 || len(m.Info()) != 0 || h.engine.Current().Version != 0 {
		t.Fatal("端口预占失败仍然提交配置")
	}
	occupied.Close()
	// A storage failure after reservation must release sockets without activation.
	path := filepath.Join(h.store.Dir(), "gateway.json")
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	w = h.call("POST", "/releases", release, true)
	if w.Code != 500 || h.engine.Current().Version != 0 || len(m.Info()) != 0 {
		t.Fatal("磁盘写入失败影响运行状态")
	}
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal("失败发布泄漏预占端口")
	}
	probe.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w = h.call("POST", "/releases", release, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(m.Info()) != 1 {
		t.Fatal("发布后未启用业务监听")
	}
	var version store.Version
	json.Unmarshal(w.Body.Bytes(), &version)
	if h.engine.Current().Version != version.ID {
		t.Fatal("内存与磁盘版本不同步")
	}
	w = h.call("GET", "/overview", nil, true)
	var overview struct {
		Listeners []gateway.ListenerInfo `json:"listeners"`
	}
	json.Unmarshal(w.Body.Bytes(), &overview)
	if len(overview.Listeners) != 1 || overview.Listeners[0].Port != port {
		t.Fatal("实例信息未展示实际监听")
	}
	w = h.call("POST", "/route-tests", map[string]any{"host": "app.example.com", "path": "/", "method": "GET", "port": port, "https": false}, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "published") {
		t.Fatal("端口推演失败")
	}
	// A stale running version must abort its reservation and preserve the active snapshot.
	extra, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	newPort := extra.Addr().(*net.TCPAddr).Port
	extra.Close()
	c.Sites[0].HTTPPort = newPort
	w = h.call("PUT", "/draft", store.Draft{Config: c, Revision: 1}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = h.call("POST", "/releases", map[string]any{"revision": 2, "requestId": "stale-active-version", "expectedActiveVersion": 0}, true)
	if w.Code != 409 || h.engine.Current().Version != version.ID {
		t.Fatal("运行版本冲突未阻止发布")
	}
	probe, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", newPort))
	if err != nil {
		t.Fatal("冲突发布泄漏端口")
	}
	probe.Close()
	// Rollback to the same port reuses the listener rather than rebinding it.
	w = h.call("POST", "/releases", map[string]any{"revision": 2, "requestId": "port-rollback-request", "expectedActiveVersion": version.ID, "rollbackVersion": version.ID}, true)
	if w.Code != 200 || len(m.Info()) != 1 || m.Info()[0].Port != port {
		t.Fatalf("端口回滚失败 %s", w.Body.String())
	}
}

func TestAdminHTTPAndProxyOrigins(t *testing.T) {
	h := setup(t)
	h.handler.options.AdminAddress = "0.0.0.0:4033"
	invoke := func(method, host, origin, forwarded string, login bool) *httptest.ResponseRecorder {
		var body bytes.Buffer
		if login {
			json.NewEncoder(&body).Encode(map[string]string{"username": "admin", "password": "testing-long-password"})
		}
		r := httptest.NewRequest(method, "http://"+host+"/api/admin/v1/session", &body)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Forwarded-Proto", forwarded)
		if !login {
			r.AddCookie(h.cookie)
		}
		w := httptest.NewRecorder()
		h.handler.ServeHTTP(w, r)
		return w
	}
	for _, host := range []string{"127.0.0.1:4033", "192.168.1.10:4033", "127.0.0.1:14033", "[::1]:4033", "localhost:4033"} {
		if w := invoke("GET", host, "", "", false); w.Code != 200 {
			t.Fatalf("直接 IP 管理入口不可用: %s %d", host, w.Code)
		}
	}
	if w := invoke("GET", "unknown.example.com:4033", "", "", false); w.Code != 421 {
		t.Fatal("未知域名绕过 Host 校验")
	}
	w := invoke("POST", "127.0.0.1:4033", "http://127.0.0.1:4033", "https", true)
	if w.Code != 200 || w.Result().Cookies()[0].Secure {
		t.Fatal("HTTP 登录失败或信任了伪造转发协议")
	}
	if w = invoke("POST", "127.0.0.1:4033", "https://evil.example.com", "https", true); w.Code != 403 {
		t.Fatal("跨来源登录被允许")
	}
	h.handler.options.Origins = []string{"https://external-console.example.com"}
	w = invoke("POST", "127.0.0.1:4033", "https://external-console.example.com", "", true)
	if w.Code != 200 || !w.Result().Cookies()[0].Secure {
		t.Fatalf("代理终止 HTTPS 未正确设置会话: %d", w.Code)
	}
	h.handler.options.Origins = nil
	c := config.Config{Sites: []config.Site{{ID: "console", Name: "控制台代理", Enabled: true, HTTPPort: 18080, Domains: []string{"console.example.com"}, MaxBodyMiB: 20}}}
	d, _ := h.store.Draft()
	if _, err := h.store.SaveDraft(c, d.Revision); err != nil {
		t.Fatal(err)
	}
	if w = invoke("GET", "console.example.com", "", "", false); w.Code != 421 {
		t.Fatal("未发布草稿域名被信任")
	}
	snap, err := h.engine.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	h.engine.Activate(snap)
	w = invoke("POST", "console.example.com", "https://console.example.com", "", true)
	if w.Code != 200 || !w.Result().Cookies()[0].Secure {
		t.Fatalf("已发布内部代理域名无法无额外来源登录: %d", w.Code)
	}
	c.Sites[0].Enabled = false
	snap, err = h.engine.Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	h.engine.Activate(snap)
	if w = invoke("GET", "console.example.com", "", "", false); w.Code != 421 {
		t.Fatal("停用域名仍被信任")
	}
}

func TestAccountRenameAndPasswordChange(t *testing.T) {
	for _, newPassword := range []string{"", "12345678"} {
		t.Run("password="+newPassword, func(t *testing.T) {
			h := setup(t)
			before, _ := h.store.Account()
			w := h.call("POST", "/password", map[string]string{"current": "testing-long-password", "username": "operator", "password": newPassword}, true)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			after, _ := h.store.Account()
			if after.Username != "operator" || after.Revision != before.Revision+1 {
				t.Fatal("用户名未持久化")
			}
			password := newPassword
			if password == "" {
				password = "testing-long-password"
				if after.PasswordHash != before.PasswordHash {
					t.Fatal("只改用户名时覆盖了密码")
				}
			}
			if !verify(password, after.PasswordHash) {
				t.Fatal("新账号密码无效")
			}
			if w = h.call("GET", "/overview", nil, true); w.Code != 401 {
				t.Fatal("旧会话未撤销")
			}
			if w = h.call("POST", "/session", map[string]string{"username": "admin", "password": password}, false); w.Code != 401 {
				t.Fatal("旧用户名仍可登录")
			}
			if w = h.call("POST", "/session", map[string]string{"username": "operator", "password": password}, false); w.Code != 200 {
				t.Fatal("新用户名登录失败", w.Body.String())
			}
		})
	}
}

func TestAccountChangeValidation(t *testing.T) {
	h := setup(t)
	before, _ := h.store.Account()
	for _, input := range []map[string]string{
		{"current": "wrong-password", "username": "operator"},
		{"current": "testing-long-password", "username": "  "},
		{"current": "testing-long-password", "username": "operator", "password": "1234567"},
		{"current": "testing-long-password", "password": "testing-long-password"},
	} {
		w := h.call("POST", "/password", input, true)
		if w.Code != 403 && w.Code != 422 {
			t.Fatalf("无效输入未拒绝：%d", w.Code)
		}
		after, _ := h.store.Account()
		if before != after {
			t.Fatal("无效修改改变了账号")
		}
	}
}

func TestPasswordCharacterLength(t *testing.T) {
	for _, password := range []string{"12345678", "一二三四五六七八"} {
		hash, err := HashPassword(password)
		if err != nil || !verify(password, hash) {
			t.Fatal("8 字符密码不可用", err)
		}
	}
	for _, password := range []string{"1234567", "一二三四五六七"} {
		if _, err := HashPassword(password); err == nil {
			t.Fatal("不足8字符密码被允许")
		}
	}
}

func TestAuditRecordsRequestIPWithoutForwardedHeaders(t *testing.T) {
	h := setup(t)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://localhost:4033/api/admin/v1"+path, bytes.NewReader(raw))
		r.RemoteAddr = "[::ffff:192.0.2.40]:5000"
		r.Header.Set("Origin", "http://localhost:4033")
		r.Header.Set("X-CSRF-Token", h.csrf)
		r.Header.Set("X-Forwarded-For", "198.51.100.99")
		r.Header.Set("X-Real-IP", "198.51.100.99")
		r.AddCookie(h.cookie)
		w := httptest.NewRecorder()
		h.handler.ServeHTTP(w, r)
		return w
	}
	for _, tt := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/session", map[string]any{"username": "admin", "password": "testing-long-password"}},
		{"PUT", "/draft", map[string]any{"config": config.Empty(), "revision": 0}},
		{"POST", "/access-control/sites/site/account", map[string]any{"revision": 0}},
		{"POST", "/access-control/grants/clear", map[string]any{"revision": 1}},
		{"POST", "/releases", map[string]any{"revision": 1, "expectedActiveVersion": 0, "requestId": "ip-audit-release"}},
	} {
		if w := call(tt.method, tt.path, tt.body); w.Code != 200 {
			t.Fatal(tt.path, w.Code, w.Body.String())
		}
	}
	w := call("GET", "/audit", nil)
	var records []store.Audit
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &records) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	found := map[string]bool{}
	for _, record := range records {
		if record.Action == "account.initialized" || record.IP == "127.0.0.1" {
			continue
		}
		if record.IP != "192.0.2.40" {
			t.Fatal("来源 IP 未记录或信任了伪造头", record)
		}
		found[record.Action] = true
	}
	for _, action := range []string{"session.login", "draft.saved", "access.account.updated", "access.ip.revoked", "config.published"} {
		if !found[action] {
			t.Fatal("缺少操作 IP", action)
		}
	}
}
