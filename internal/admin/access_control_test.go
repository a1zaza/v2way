package admin

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSiteAccountGenerationAndRotation(t *testing.T) {
	h := setup(t)
	path := "/access-control/sites/site-a/account"
	if w := h.call("POST", path, map[string]any{"revision": 0}, false); w.Code != 401 {
		t.Fatal("匿名可生成", w.Code)
	}
	if w := h.call("PUT", "/access-control/account", map[string]any{}, true); w.Code != 404 {
		t.Fatal("全局账号接口未移除", w.Code)
	}
	type issued struct {
		Code     string
		Revision int64
	}
	generate := func(id string, revision int64) issued {
		w := h.call("POST", "/access-control/sites/"+id+"/account", map[string]any{"revision": revision}, true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var fields map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["username"]; exists {
			t.Fatal("访问码接口不应返回用户名")
		}
		if _, exists := fields["password"]; exists {
			t.Fatal("访问码接口不应返回密码字段")
		}
		var value issued
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9]{8}$`).MatchString(value.Code) {
			t.Fatal("随机凭据格式错误")
		}
		return value
	}
	a := generate("site-a", 0)
	b := generate("site-b", 0)
	acctA, err := h.store.AccessAccount("site-a")
	if err != nil || !verify(a.Code, acctA.PasswordHash) {
		t.Fatal("生成账号不可用", err)
	}
	acctB, err := h.store.AccessAccount("site-b")
	if err != nil || verify(a.Code, acctB.PasswordHash) {
		t.Fatal("跨站点共用新密码", err)
	}
	grantA, err := h.store.AuthorizeIP("site-a", "192.0.2.9", a.Revision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	grantB, err := h.store.AuthorizeIP("site-b", grantA.IP, b.Revision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	metadata := h.call("GET", path, nil, true)
	if metadata.Code != 200 || strings.Contains(metadata.Body.String(), a.Code) || strings.Contains(metadata.Body.String(), "passwordHash") || strings.Contains(metadata.Body.String(), "username") {
		t.Fatal("元数据泄漏密码")
	}
	next := generate("site-a", a.Revision)
	if next.Revision != a.Revision+1 {
		t.Fatal("账号修订错误")
	}
	if got, ok := h.store.CheckAccessIP("site-a", grantA.IP, time.Now()); !ok || got.ID != grantA.ID || got.ExpiresAt != grantA.ExpiresAt {
		t.Fatal("重置影响已授权登录", got, ok)
	}
	rotated, err := h.store.AccessAccount("site-a")
	if err != nil || rotated.Username != "访问码" || !verify(next.Code, rotated.PasswordHash) || verify(a.Code, rotated.PasswordHash) {
		t.Fatal("重置后凭据未替换", err)
	}
	if _, ok := h.store.CheckAccessIP("site-b", grantB.IP, time.Now()); !ok {
		t.Fatal("更新影响其他站点")
	}
	if w := h.call("POST", path, map[string]any{"revision": a.Revision}, true); w.Code != 409 {
		t.Fatal("旧修订覆盖账号", w.Code)
	}
	if list := h.call("GET", "/access-control/grants?siteId=site-b", nil, true); list.Code != 200 || !strings.Contains(list.Body.String(), "site-b") {
		t.Fatal("列表查询失败", list.Code)
	}
}
