package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditTailRepairAndMiddleCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Audit("test.action", "安全记录"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "logs", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal("没有生成 JSONL 日志")
	}
	f, _ := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"partial":`)
	f.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	records, err := s.Audits()
	if err != nil || len(records) != 1 {
		t.Fatal("没有修复末尾不完整记录")
	}
	s.Close()
	raw, _ := os.ReadFile(files[0])
	os.WriteFile(files[0], append([]byte("not-json\n"), raw...), 0600)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Status().AuditProblem == "" {
		t.Fatal("跳过了中间损坏的日志")
	}
	if err = s.Audit("must.fail", "不能追加"); err == nil {
		t.Fatal("在损坏日志上继续追加")
	}
}

func TestAccessAuditsDisplaySiteNamesAndKeepIDs(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "草稿站点"
		if published {
			name = "已发布站点"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			cfg := configuration("test")
			cfg.Sites[0].Name = name
			draft, err := s.SaveDraft(cfg, 0)
			if err != nil {
				t.Fatal(err)
			}
			if published {
				if _, err := s.Commit(cfg, PublishInput{Revision: draft.Revision, RequestID: Token(), Actor: "admin"}); err != nil {
					t.Fatal(err)
				}
			}
			legacy := "站点 site 授权来源 IP 192.0.2.1"
			if err := s.Audit("access.ip.authorized", legacy); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateAccessAccount("site", 0, "012345", accessHash(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AuthorizeIP("site", "192.0.2.2", 1, time.Now()); err != nil {
				t.Fatal(err)
			}
			check := func(legacyName string) {
				t.Helper()
				records, err := s.Audits()
				if err != nil {
					t.Fatal(err)
				}
				found := map[string]bool{}
				for _, record := range records {
					switch record.Action {
					case "access.account.updated":
						if record.SiteID != "site" || !strings.Contains(record.Detail, "「"+name+"」") {
							t.Fatal("账号日志未保留站点名称和 ID", record)
						}
						found["account"] = true
					case "access.ip.authorized":
						expected := name
						if strings.HasSuffix(record.Detail, "192.0.2.1") {
							expected = legacyName
						}
						if record.SiteID != "site" || !strings.Contains(record.Detail, "「"+expected+"」") {
							t.Fatal("授权日志未解析名称或丢失 ID", record)
						}
						found[record.Detail] = true
					}
				}
				if len(found) != 3 {
					t.Fatal("日志缺失", found)
				}
			}
			check(name)
			// A rename must not rewrite the recorded name on new audit events.
			cfg.Sites[0].Name = "后来修改的名称"
			if _, err := s.SaveDraft(cfg, draft.Revision); err != nil {
				t.Fatal(err)
			}
			legacyName := cfg.Sites[0].Name
			if published {
				legacyName = name
			}
			check(legacyName)
			files, _ := filepath.Glob(filepath.Join(dir, "logs", "*.jsonl"))
			raw, err := os.ReadFile(files[0])
			if err != nil || !strings.Contains(string(raw), legacy) || !strings.Contains(string(raw), `"siteId":"site"`) {
				t.Fatal("历史原文被改写或新日志未保存内部 ID", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			check(legacyName)
		})
	}
}
