package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"v2way/internal/config"
)

func TestLegacyMigrationResumeAndRecovery(t *testing.T) {
	dir := t.TempDir()
	bundle := LegacyExport{SchemaVersion: 1, ActiveVersion: 7,
		Account:  &LegacyAccount{Username: "admin", PasswordHash: strings.Repeat("a", 32) + ":" + strings.Repeat("b", 64)},
		Draft:    Draft{Revision: 9, BaseVersion: 7, Config: configuration("draft")},
		Versions: []Version{{ID: 3, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), RequestID: "legacy-3", Config: configuration("old")}, {ID: 7, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), RequestID: "legacy-7", Config: configuration("current")}},
		Audits:   []Audit{{ID: 1, Time: time.Now().UTC().Format(time.RFC3339Nano), Action: "login", Detail: "admin"}}}
	if err := os.WriteFile(filepath.Join(dir, "gateway.db"), []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := BeginLegacyImport(dir, bundle)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if opened, e := Open(dir); e == nil {
		opened.Close()
		t.Fatal("未完成转换允许启动")
	}
	different := bundle
	different.Draft.Revision++
	if opened, e := BeginLegacyImport(dir, different); e == nil {
		opened.Close()
		t.Fatal("不同导出覆盖未完成迁移")
	}
	s, err = BeginLegacyImport(dir, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishLegacyImport(bundle); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Account()
	d, _ := s.Draft()
	v, _ := s.Active()
	if a.PasswordHash != bundle.Account.PasswordHash || d.Revision != 9 || v.ID != 7 || v.Config.Sites[0].Routes[0].Body != "current" {
		t.Fatal("旧数据未保留")
	}
	s.Close()
	if opened, e := BeginLegacyImport(dir, bundle); e == nil {
		opened.Close()
		t.Fatal("已完成实例被覆盖")
	}
	if err = os.WriteFile(filepath.Join(dir, "gateway.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("资源不可用")
	if err = Recover(dir, 3, func(config.Config) error { return rejected }); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "gateway.json"))
	if string(raw) != "broken" {
		t.Fatal("资源校验失败仍执行恢复")
	}
	if err = Recover(dir, 3, func(config.Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, _ = s.Active()
	if v.ID != 8 || v.Config.Sites[0].Routes[0].Body != "old" {
		t.Fatal("恢复没有生成新版本")
	}
	copies, _ := filepath.Glob(filepath.Join(dir, "backups", "recovery-*", "gateway.json"))
	if len(copies) != 1 {
		t.Fatal("未保留损坏原文件")
	}
	raw, _ = os.ReadFile(copies[0])
	if string(raw) != "broken" {
		t.Fatal("恢复备份内容错误")
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "gateway.db"))
	if string(raw) != "preserved" {
		t.Fatal("旧库被修改")
	}
}
