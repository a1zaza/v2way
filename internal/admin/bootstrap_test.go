package admin

import (
	"os"
	"path/filepath"
	"testing"

	"v2way/internal/store"
)

func TestInitializeOnStartupCreatesAccountOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	password, err := InitializeOnStartup(s)
	if err != nil || len(password) != 8 {
		t.Fatal("首次启动未生成随机密码", err)
	}
	account, err := s.Account()
	if err != nil || account.Username != "admin" || !verify(password, account.PasswordHash) {
		t.Fatal("初始化密码无法验证", err)
	}
	if next, err := InitializeOnStartup(s); err != nil || next != "" {
		t.Fatal("重复初始化返回了密码", err)
	}
	// Restart after changing the password must neither reveal nor replace it.
	hash, err := HashPassword("changed-testing-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePassword(account.Revision, hash); err != nil {
		t.Fatal(err)
	}
	s.Close()
	restarted, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if next, err := InitializeOnStartup(restarted); err != nil || next != "" {
		t.Fatal("重启重新生成了密码", err)
	}
	saved, err := restarted.Account()
	if err != nil || saved.PasswordHash != hash {
		t.Fatal("重启覆盖了密码", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "initial-admin.json")); !os.IsNotExist(err) {
		t.Fatal("不应保存初始明文凭据文件")
	}
}

func TestInitializeOnStartupPreservesManualAccount(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := Initialize(s, "operator", "manual-testing-password"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Account()
	if password, err := InitializeOnStartup(s); err != nil || password != "" {
		t.Fatal("已有手动账户仍初始化", err)
	}
	after, _ := s.Account()
	if before != after {
		t.Fatal("手动账户发生变化")
	}
}
