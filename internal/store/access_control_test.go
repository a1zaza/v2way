package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"v2way/internal/config"
	"v2way/internal/jsonfile"
)

func accessHash() string { return strings.Repeat("a", 32) + ":" + strings.Repeat("b", 64) }
func TestAccessSlidingExpiryPersistenceAndRevocation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	if err = s.UpdateAccessAccount("site", 0, "visitor", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	grant, err := s.AuthorizeIP("site", "::ffff:192.0.2.1", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if grant.IP != "192.0.2.1" || !grant.ExpiresAt.Equal(now.Add(48*time.Hour)) {
		t.Fatal(grant)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(47*time.Hour))
	extended, ok := s.CheckAccessIP("site", grant.IP, now.Add(49*time.Hour))
	if !ok || !extended.ExpiresAt.Equal(now.Add(95*time.Hour)) {
		t.Fatal(extended, ok)
	}
	if err = s.FlushAccess(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if saved, ok := s.CheckAccessIP("site", grant.IP, now.Add(49*time.Hour)); !ok || saved.ExpiresAt != extended.ExpiresAt {
		t.Fatal("续期未持久化", saved)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(95*time.Hour))
	if _, ok = s.CheckAccessIP("site", grant.IP, now.Add(95*time.Hour)); ok {
		t.Fatal("到期边界被续期复活")
	}
	renewed, err := s.AuthorizeIP("site", grant.IP, 1, now.Add(96*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(97*time.Hour))
	if got, _ := s.CheckAccessIP("site", grant.IP, now.Add(97*time.Hour)); got.ID != renewed.ID || got.LastAccessAt != renewed.LastAccessAt {
		t.Fatal("旧连接修改重新登录记录")
	}
	if err = s.RevokeAccess(renewed.ID, -1); err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site", grant.IP, renewed.ID, now.Add(98*time.Hour))
	if _, ok = s.CheckAccessIP("site", grant.IP, now.Add(98*time.Hour)); ok {
		t.Fatal("撤销后复活")
	}
	active, err := s.AuthorizeIP("site", grant.IP, 1, now.Add(99*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	status := s.SiteAccessStatus("site", now.Add(99*time.Hour))
	if err = s.UpdateAccessAccount("site", status.Revision, "renamed", accessHash(), now.Add(100*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site", grant.IP, active.ID, now.Add(101*time.Hour))
	if _, ok = s.CheckAccessIP("site", grant.IP, now.Add(101*time.Hour)); ok || s.AccessStatus(now).Enabled {
		t.Fatal("停用未撤销授权")
	}
	if _, err = s.AuthorizeIP("site", grant.IP, 2, now.Add(101*time.Hour)); err == nil {
		t.Fatal("停用后可授权")
	}
	if info, err := os.Stat(filepath.Join(dir, "access-control.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("文件权限", err)
	}
}
func TestAccessUncertainRevocationFailsClosed(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	if err = s.UpdateAccessAccount("site", 0, "visitor", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	grant, err := s.AuthorizeIP("site", "192.0.2.2", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	s.write = func(path string, value any) error {
		if err := jsonfile.Write(path, value); err != nil {
			return err
		}
		return jsonfile.ErrUncertain
	}
	if err = s.RevokeAccess(grant.ID, -1); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if _, ok := s.CheckAccessIP("site", grant.IP, now); ok {
		t.Fatal("结果未知时仍放行")
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(time.Minute))
	s.write = jsonfile.Write
	if err = s.ConfirmPending(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CheckAccessIP("site", grant.IP, now); ok {
		t.Fatal("确认后撤销记录被恢复")
	}
}
func TestAccessCorruptionDoesNotResetOrBreakOtherConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = os.WriteFile(filepath.Join(dir, "access-control.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.AccessStatus(time.Now()).Problem == "" {
		t.Fatal("损坏未报告")
	}
	if _, ok := s.CheckAccessIP("site", "192.0.2.1", time.Now()); ok {
		t.Fatal("损坏后开放")
	}
	if _, err := s.Draft(); err != nil {
		t.Fatal("影响了普通配置", err)
	}
	if err = s.UpdateAccessAccount("site", 0, "visitor", accessHash(), time.Now()); err == nil {
		t.Fatal("覆盖了损坏文件")
	}
}
func TestAccessFlushFailurePausesRenewal(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	if err = s.UpdateAccessAccount("site", 0, "visitor", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	grant, err := s.AuthorizeIP("site", "192.0.2.3", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(time.Hour))
	s.write = func(string, any) error { return errors.New("disk full") }
	if err = s.FlushAccess(); err == nil {
		t.Fatal("错误未报告")
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(2*time.Hour))
	if got, _ := s.CheckAccessIP("site", grant.IP, now.Add(2*time.Hour)); !got.LastAccessAt.Equal(now.Add(time.Hour)) {
		t.Fatal("失败后仍续期")
	}
	s.write = jsonfile.Write
	if err = s.FlushAccess(); err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(3*time.Hour))
	if got, _ := s.CheckAccessIP("site", grant.IP, now.Add(3*time.Hour)); !got.LastAccessAt.Equal(now.Add(3 * time.Hour)) {
		t.Fatal("修复后未续期")
	}
}

func TestAccessIsIsolatedBySite(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	if err = s.UpdateAccessAccount("site-a", 0, "visitor", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccessAccount("site-b", 0, "visitor", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	a, err := s.AuthorizeIP("site-a", "192.0.2.50", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CheckAccessIP("site-b", a.IP, now); ok {
		t.Fatal("A 站授权可用于 B 站")
	}
	b, err := s.AuthorizeIP("site-b", a.IP, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site-a", a.IP, a.ID, now.Add(time.Hour))
	currentA, _ := s.CheckAccessIP("site-a", a.IP, now.Add(time.Hour))
	currentB, _ := s.CheckAccessIP("site-b", b.IP, now.Add(time.Hour))
	if !currentA.LastAccessAt.After(a.LastAccessAt) || currentB.LastAccessAt != b.LastAccessAt {
		t.Fatal("续期跨站点串用")
	}
	s.TouchAccessIP("site-b", a.IP, a.ID, now.Add(2*time.Hour))
	if got, _ := s.CheckAccessIP("site-b", b.IP, now.Add(2*time.Hour)); got.LastAccessAt != b.LastAccessAt {
		t.Fatal("旧连接跨站点续期")
	}
	if err = s.RevokeAccess(a.ID, -1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CheckAccessIP("site-b", b.IP, now.Add(time.Hour)); !ok {
		t.Fatal("撤销 A 影响 B")
	}
}
func TestLegacyGlobalGrantsAreDroppedButAccountIsPreserved(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = s.UpdateAccessAccount("site", 0, "visitor", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	account, _ := s.AccessAccount("site")
	c := configuration("old")
	c.Sites[0].RequireIPLogin = true
	draft, _ := s.Draft()
	if _, err = s.SaveDraft(c, draft.Revision); err != nil {
		t.Fatal(err)
	}
	s.Close()
	old := AccessControl{SchemaVersion: 1, Revision: 10, Account: &account, Grants: map[string]AccessGrant{"192.0.2.5": {ID: Token(), IP: "192.0.2.5", Username: account.Username, AccountRevision: account.Revision, GrantedAt: now, LastAccessAt: now, ExpiresAt: now.Add(AccessLifetime)}}}
	if err = jsonfile.Write(filepath.Join(dir, "access-control.json"), old); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if a, err := s.AccessAccount("site"); err != nil || a.PasswordHash != account.PasswordHash || a.Username != account.Username {
		t.Fatal("升级重置了访问账号", err)
	}
	if status := s.AccessStatus(now); !status.Initialized || status.Count != 0 || status.Revision != 11 {
		t.Fatal(status)
	}
	if _, ok := s.CheckAccessIP("site-a", "192.0.2.5", now); ok {
		t.Fatal("全局授权被扩展到本站")
	}
	if err = s.FlushAccess(); err != nil {
		t.Fatal(err)
	}
	var saved AccessControl
	if err = jsonfile.Read(filepath.Join(dir, "access-control.json"), &saved); err != nil || saved.SchemaVersion != 3 || len(saved.Grants) != 0 {
		t.Fatal("未保存站点隔离格式", err, saved.SchemaVersion)
	}
}
func TestLegacyGlobalSnapshotRemainsReadable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := publish(t, s, "original")
	s.Close()
	var old Snapshot
	if err = jsonfile.Read(filepath.Join(dir, "gateway.json"), &old); err != nil {
		t.Fatal(err)
	}
	old.Config.Sites[0].RequireIPLogin = true
	old.Config.AccessGate = &config.AccessGate{Enabled: true, EntrySiteID: "site", EntryDomain: "app.example.com", LoginPath: "/__access/" + Token()}
	old.ConfigHash = Hash(config.Normalize(clone(old.Config)))
	if err = jsonfile.Write(filepath.Join(dir, "gateway.json"), old); err != nil {
		t.Fatal(err)
	}
	if err = jsonfile.Write(snapshotPath(dir, v.ID), old); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal("升级后无法加载旧运行快照", err)
	}
	defer s.Close()
	if active, err := s.Active(); err != nil || active.ID != v.ID || active.Config.AccessGate == nil {
		t.Fatal(err, active.ID)
	}
}

func TestSchemaTwoAccountMigratesIntoExistingSites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = s.UpdateAccessAccount("site", 0, "legacy", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	account, _ := s.AccessAccount("site")
	cfg := configuration("old")
	cfg.Sites[0].ID = "site-b"
	cfg.Sites[0].RequireIPLogin = true
	draft, _ := s.Draft()
	if _, err = s.SaveDraft(cfg, draft.Revision); err != nil {
		t.Fatal(err)
	}
	s.Close()
	grant := AccessGrant{SiteID: "site-a", ID: Token(), IP: "192.0.2.44", Username: account.Username, AccountRevision: account.Revision, GrantedAt: now, LastAccessAt: now, ExpiresAt: now.Add(AccessLifetime)}
	old := AccessControl{SchemaVersion: 2, Revision: 20, Account: &account, Grants: map[string]AccessGrant{grantKey(grant.SiteID, grant.IP): grant}}
	if err = jsonfile.Write(filepath.Join(dir, "access-control.json"), old); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.AccessAccount("site-a")
	if err != nil || a.PasswordHash != account.PasswordHash {
		t.Fatal("已有授权站点未迁移", err)
	}
	b, err := s.AccessAccount("site-b")
	if err != nil || b.PasswordHash != account.PasswordHash {
		t.Fatal("已有配置站点未迁移", err)
	}
	if _, ok := s.CheckAccessIP("site-a", grant.IP, now); !ok {
		t.Fatal("原站点授权丢失")
	}
	if _, ok := s.CheckAccessIP("site-b", grant.IP, now); ok {
		t.Fatal("迁移扩大了授权")
	}
	if err = s.UpdateAccessAccount("site-a", a.Revision, "new-a", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.CheckAccessIP("site-a", grant.IP, now); !ok || got.ID != grant.ID || !got.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatal("更新本站影响已迁移授权", got, ok)
	}
	if other, _ := s.AccessAccount("site-b"); other.Username != b.Username || other.Revision != b.Revision {
		t.Fatal("更新影响其他站点账号")
	}
	var saved AccessControl
	if err = jsonfile.Read(filepath.Join(dir, "access-control.json"), &saved); err != nil || saved.SchemaVersion != 3 || saved.Account != nil || len(saved.Accounts) != 2 {
		t.Fatal("格式升级失败", err)
	}
}

func TestCredentialRotationPreservesGrantsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now().UTC()
	if err := s.UpdateAccessAccount("site", 0, "012345", accessHash(), now); err != nil {
		t.Fatal(err)
	}
	grant, err := s.AuthorizeIP("site", "192.0.2.50", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.AuthorizeIP("site", "192.0.2.51", 1, now.Add(-49*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccessAccount("site", 1, "654321", accessHash(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, ok := s.CheckAccessIP("site", grant.IP, now.Add(time.Minute))
	if !ok || got.ID != grant.ID || !got.GrantedAt.Equal(grant.GrantedAt) || !got.LastAccessAt.Equal(grant.LastAccessAt) || !got.ExpiresAt.Equal(grant.ExpiresAt) || got.AccountRevision != 2 || got.Username != "654321" {
		t.Fatal("重置修改了授权或有效期", got, ok)
	}
	if _, ok := s.CheckAccessIP("site", expired.IP, now); ok {
		t.Fatal("重置复活过期授权")
	}
	if _, err := s.AuthorizeIP("site", "192.0.2.52", 1, now); err == nil {
		t.Fatal("旧账号修订仍能新增授权")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved, ok := s.CheckAccessIP("site", grant.IP, now.Add(2*time.Minute)); !ok || saved != got {
		t.Fatal("重启未保留原授权", saved, ok)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(3*time.Minute))
	if renewed, ok := s.CheckAccessIP("site", grant.IP, now.Add(3*time.Minute)); !ok || !renewed.ExpiresAt.Equal(now.Add(3*time.Minute).Add(AccessLifetime)) {
		t.Fatal("重置后原连接不能续期", renewed, ok)
	}
	if err := s.RevokeAccess(grant.ID, -1); err != nil {
		t.Fatal(err)
	}
	s.TouchAccessIP("site", grant.IP, grant.ID, now.Add(4*time.Minute))
	if _, ok := s.CheckAccessIP("site", grant.IP, now.Add(4*time.Minute)); ok {
		t.Fatal("手动撤销未生效")
	}
}
