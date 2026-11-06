package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"v2way/internal/config"
	"v2way/internal/jsonfile"
)

func configuration(body string) config.Config {
	return config.Config{Sites: []config.Site{{ID: "site", Name: "站点", Domains: []string{"app.example.com"}, Enabled: true, HTTPPort: 18080, MaxBodyMiB: 20, Routes: []config.Route{{ID: "route", Name: "首页", Path: "/", Match: "prefix", Action: "response", Status: 200, Body: body}}}}, Upstreams: []config.Upstream{}}
}
func publish(t *testing.T, s *Store, body string) Version {
	t.Helper()
	d, err := s.Draft()
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.SaveDraft(configuration(body), d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := s.Active()
	v, err := s.Commit(d.Config, PublishInput{Revision: d.Revision, ExpectedActiveVersion: active.ID, RequestID: Token(), Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestMaintenanceStateSurvivesJSONRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := configuration("original route")
	c.Sites[0].Maintenance = true
	d, err := s.SaveDraft(c, 0)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	v, err := s.Commit(d.Config, PublishInput{Revision: d.Revision, ExpectedActiveVersion: 0, RequestID: Token(), Actor: "test"})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	active, err := s.Active()
	if err != nil || active.ID != v.ID || !active.Config.Sites[0].Maintenance || !active.Config.Sites[0].Enabled || active.Config.Sites[0].Routes[0].Body != "original route" {
		t.Fatal("维护状态或原配置未持久化", active, err)
	}
	draft, err := s.Draft()
	if err != nil || !draft.Config.Sites[0].Maintenance || draft.Config.Sites[0].HTTPPort != 18080 {
		t.Fatal("草稿维护状态未持久化", draft, err)
	}
}
func TestJSONDurabilityAndExclusiveLock(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, e := Open(dir); e == nil {
		other.Close()
		t.Fatal("数据目录允许多实例")
	}
	d, err := s.Draft()
	if err != nil || d.Revision != 0 {
		t.Fatal(err)
	}
	d, err = s.SaveDraft(configuration("v1"), 0)
	if err != nil || d.Revision != 1 {
		t.Fatal(err)
	}
	if active, _ := s.Active(); active.ID != 0 {
		t.Fatal("草稿提前影响运行配置")
	}
	if _, err = s.SaveDraft(config.Empty(), 0); !errors.Is(err, ErrConflict) {
		t.Fatal("旧修订被接受")
	}
	in := PublishInput{Revision: 1, ExpectedActiveVersion: 0, RequestID: "publish-request", Actor: "admin", Note: "首版"}
	first, err := s.Commit(d.Config, in)
	if err != nil {
		t.Fatal(err)
	}
	if replay, ok, err := s.CheckReplay(in); err != nil || !ok || replay.ID != first.ID {
		t.Fatal("发布回执未持久化")
	}
	in.Note = "不同参数"
	if _, _, err = s.CheckReplay(in); !errors.Is(err, ErrConflict) {
		t.Fatal("复用了不同参数的幂等标识")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	active, err := s.Active()
	if err != nil || active.ID != first.ID || active.Config.Sites[0].Routes[0].Body != "v1" {
		t.Fatal("重启丢失当前版本")
	}
	if _, err = os.Stat(filepath.Join(dir, "gateway.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("JSON 存储生成了数据库")
	}
	for _, file := range []string{"storage.json", "draft.json", "gateway.json"} {
		info, err := os.Stat(filepath.Join(dir, file))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("配置权限错误")
		}
	}
}
func TestWriteFailuresDoNotActivateAndSkipOrphans(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := publish(t, s, "first")
	d, _ := s.Draft()
	d, err = s.SaveDraft(configuration("second"), d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	s.write = func(path string, value any) error {
		if filepath.Base(path) == "gateway.json" {
			return errors.New("injected disk full")
		}
		return jsonfile.Write(path, value)
	}
	if _, err = s.Commit(d.Config, PublishInput{Revision: d.Revision, ExpectedActiveVersion: first.ID, RequestID: Token()}); err == nil {
		t.Fatal("忽略了写入失败")
	}
	if active, _ := s.Active(); active.ID != first.ID {
		t.Fatal("失败提交改变运行版本")
	}
	s.write = jsonfile.Write
	second := publish(t, s, "third")
	if second.ID != first.ID+2 {
		t.Fatal("复用了孤立候选版本号")
	}
	versions, err := s.Versions()
	if err != nil || len(versions) != 2 {
		t.Fatal("孤立候选进入成功历史")
	}
}
func TestUncertainDirectorySyncFreezesUntilConfirmed(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := publish(t, s, "old")
	d, _ := s.Draft()
	d, _ = s.SaveDraft(configuration("new"), d.Revision)
	s.write = func(path string, value any) error {
		if err := jsonfile.Write(path, value); err != nil {
			return err
		}
		if filepath.Base(path) == "gateway.json" {
			return jsonfile.ErrUncertain
		}
		return nil
	}
	in := PublishInput{Revision: d.Revision, ExpectedActiveVersion: first.ID, RequestID: "unknown-request"}
	if _, err = s.Commit(d.Config, in); !errors.Is(err, ErrPending) {
		t.Fatalf("没有标记结果待确认: %v", err)
	}
	if active, _ := s.Active(); active.ID != first.ID {
		t.Fatal("未确认结果提前激活")
	}
	if _, err = s.ByRequest(in.RequestID); !errors.Is(err, ErrPending) {
		t.Fatal("查询将未知结果误报成功")
	}
	if _, err = s.SaveDraft(configuration("ignored"), d.Revision); !errors.Is(err, ErrPending) {
		t.Fatal("未知状态仍允许后续写入")
	}
	if err = s.ConfirmPending(); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.Active(); active.Config.Sites[0].Routes[0].Body != "new" {
		t.Fatal("确认后没有采用已提交配置")
	}
}
func TestCommittedFileRecoversAfterCrashBeforeActivation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, "old")
	d, _ := s.Draft()
	d, _ = s.SaveDraft(configuration("new"), d.Revision)
	active, _ := s.Active()
	s.write = func(path string, value any) error {
		if err := jsonfile.Write(path, value); err != nil {
			return err
		}
		if filepath.Base(path) == "gateway.json" {
			return jsonfile.ErrUncertain
		}
		return nil
	}
	_, _ = s.Commit(d.Config, PublishInput{Revision: d.Revision, ExpectedActiveVersion: active.ID, RequestID: Token()})
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	current, _ := s.Active()
	if current.Config.Sites[0].Routes[0].Body != "new" {
		t.Fatal("重启没有恢复完整提交文件")
	}
}
func TestHistoryCorruptionPreservesActiveAndBlocksWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := publish(t, s, "one")
	last := publish(t, s, "two")
	s.Close()
	os.WriteFile(snapshotPath(dir, first.ID), []byte(`{"broken":true}`), 0600)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if active, _ := s.Active(); active.ID != last.ID {
		t.Fatal("历史损坏影响独立运行配置")
	}
	if !s.Status().ReadOnly {
		t.Fatal("历史损坏未冻结写入")
	}
	if _, err = s.SaveDraft(config.Empty(), 2); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
}
func TestMalformedCurrentAndMissingCurrentNeverReset(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			publish(t, s, "one")
			s.Close()
			if missing {
				os.Remove(filepath.Join(dir, "gateway.json"))
			} else {
				os.WriteFile(filepath.Join(dir, "gateway.json"), []byte(`{"schemaVersion":1`), 0600)
			}
			if s, err = Open(dir); err == nil {
				s.Close()
				t.Fatal("损坏/缺失当前配置被重建为空")
			}
		})
	}
}
func TestDraftRecoveryAndExternalEditConflict(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, "published")
	s.Close()
	os.WriteFile(filepath.Join(dir, "draft.json"), []byte(`{"broken":`), 0600)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Draft(); !errors.Is(err, ErrReadOnly) {
		t.Fatal("损坏草稿被忽略")
	}
	d, err := s.ResetDraft()
	if err != nil || d.Config.Sites[0].Routes[0].Body != "published" {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "draft.json.damaged-*"))
	if len(backups) != 1 {
		t.Fatal("没有保留损坏草稿")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "draft.json"))
	os.WriteFile(filepath.Join(dir, "draft.json"), append(raw, ' '), 0600)
	if _, err = s.SaveDraft(config.Empty(), d.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("外部编辑被覆盖")
	}
}
func TestRetentionKeepsOnlyCommittedIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var latest Version
	for i := 0; i < 103; i++ {
		latest = publish(t, s, fmt.Sprint(i))
	}
	versions, err := s.Versions()
	if err != nil || len(versions) != 101 || versions[0].ID != latest.ID {
		t.Fatal("历史保留索引错误")
	}
	if _, err = s.Version(1); !errors.Is(err, ErrNotFound) {
		t.Fatal("已清理历史仍可回滚")
	}
	if _, err = os.Stat(snapshotPath(s.dir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("未清理超出保留范围的历史")
	}
}
func TestOldSQLiteRefusedAndStrictParsing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gateway.db"), []byte("legacy"), 0600)
	if s, err := Open(dir); err == nil {
		s.Close()
		t.Fatal("静默忽略了旧数据库")
	}
	for _, raw := range []string{`{"sites":[],"sites":[],"upstreams":[]}`, `{"sites":[],"upstreams":[]} {}`, `{"sites":[],"upstreams":[],"unknown":true}`, strings.Repeat("[", 70) + strings.Repeat("]", 70)} {
		if _, err := DecodeConfig([]byte(raw)); err == nil {
			t.Fatal("接受无效/重复配置")
		}
	}
}
