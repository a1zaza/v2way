package certificate

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudflareOnlyDeletesCreatedRecordAndReportsFailure(t *testing.T) {
	t.Setenv("LEGO_DISABLE_CNAME_SUPPORT", "true")
	records := map[string]string{"existing": "unrelated-txt"}
	failDelete := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/zones" {
			if r.Header.Get("Authorization") != "Bearer zone-token" {
				t.Error("区域查询未使用独立令牌")
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]string{{"id": "zone-id", "name": "example.com"}}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer dns-token" {
			t.Error("记录操作未使用 DNS 令牌")
		}
		if r.Method == "POST" {
			var body struct{ Type, Name, Content string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Type != "TXT" || body.Name != "_acme-challenge.example.com" {
				t.Error("创建了错误的 DNS 记录")
			}
			records["created"] = body.Content
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]string{"id": "created"}})
			return
		}
		if r.Method == "DELETE" {
			if r.URL.Path != "/zones/zone-id/dns_records/created" {
				t.Error("尝试删除非本次创建的记录")
			}
			if failDelete {
				w.WriteHeader(503)
				json.NewEncoder(w).Encode(map[string]bool{"success": false})
				return
			}
			delete(records, "created")
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]string{"id": "created"}})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	p := cloudflareOwned(&cfAPI{base: server.URL, token: "dns-token", zoneToken: "zone-token", client: server.Client()})
	p.zone = func(string) (string, error) { return "example.com.", nil }
	persisted := 0
	p.persist = func(map[string]ownedRecord) error { persisted++; return nil }
	if err := p.Present("example.com", "token", "token.thumbprint"); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanUp("example.com", "wrong-token", "token.thumbprint"); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatal("未知 token 删除了记录")
	}
	if err := p.CleanUp("example.com", "token", "different.thumbprint"); err == nil {
		t.Fatal("允许了不匹配的清理请求")
	}
	if err := p.CleanUp("example.com", "token", "token.thumbprint"); err == nil {
		t.Fatal("吞掉了 DNS 清理失败")
	}
	if len(p.records) != 1 {
		t.Fatal("删除失败丢失恢复信息")
	}
	failDelete = false
	if err := p.CleanUp("example.com", "token", "token.thumbprint"); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records["existing"] != "unrelated-txt" || persisted != 2 {
		t.Fatal("未保留既有 TXT 或未保存恢复状态")
	}
}
func TestOwnedCleanupAfterAmbiguousPersistenceFailure(t *testing.T) {
	t.Setenv("LEGO_DISABLE_CNAME_SUPPORT", "true")
	removed := ""
	p := owned(func(zone, name, value string) (ownedRecord, error) {
		return ownedRecord{"own-id", zone, name, value}, nil
	}, func(r ownedRecord) error { removed = r.ID; return nil })
	p.zone = func(string) (string, error) { return "example.com.", nil }
	p.persist = func(map[string]ownedRecord) error { return errors.New("disk full") }
	if err := p.Present("example.com", "token", "token.thumbprint"); err == nil {
		t.Fatal("忽略了恢复信息落盘失败")
	}
	_ = p.CleanUp("example.com", "token", "token.thumbprint")
	if removed != "own-id" {
		t.Fatal("落盘失败后未清理已创建的记录")
	}
}
func TestRFC2136CredentialValidation(t *testing.T) {
	err := validateCredentials("rfc2136", map[string]string{"nameserver": "dns.example.com:53", "zone": "example.com", "tsigKey": "key.example.com", "tsigSecret": "not-base64"})
	if err == nil || !strings.Contains(err.Error(), "Base64") {
		t.Fatal("无效 TSIG 密钥被接受")
	}
}
