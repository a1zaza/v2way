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

func TestSiteAccountCustomCode(t *testing.T) {
	h := setup(t)
	path := "/access-control/sites/site-a/account"
	for _, code := range []any{"", "1234567", "123456789", "abcdefgh", "１２３４５６７８", "1234567\n", " 12345678", 12345678} {
		w := h.call("POST", path, map[string]any{"revision": 0, "code": code}, true)
		if w.Code != 400 {
			t.Fatalf("无效访问码被接受: %v: %d", code, w.Code)
		}
	}
	for revision, code := range []string{"00123456", "00876543"} {
		w := h.call("POST", path, map[string]any{"revision": revision, "code": code}, true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var value struct {
			Code     string
			Revision int64
		}
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if value.Code != code || value.Revision != int64(revision+1) {
			t.Fatal("未保留指定值或修订", value)
		}
		account, err := h.store.AccessAccount("site-a")
		if err != nil || !verify(code, account.PasswordHash) {
			t.Fatal("指定访问码不可用", err)
		}
		if revision == 0 {
			if _, err := h.store.AuthorizeIP("site-a", "192.0.2.9", value.Revision, time.Now()); err != nil {
				t.Fatal(err)
			}
		} else if _, ok := h.store.CheckAccessIP("site-a", "192.0.2.9", time.Now()); !ok {
			t.Fatal("指定访问码清除了已有授权")
		}
		metadata := h.call("GET", path, nil, true)
		if strings.Contains(metadata.Body.String(), code) {
			t.Fatal("元数据回显明文")
		}
	}
	if w := h.call("POST", path, map[string]any{"revision": 0, "code": "12345678"}, true); w.Code != 409 {
		t.Fatal("旧修订可覆盖指定访问码", w.Code)
	}
}
