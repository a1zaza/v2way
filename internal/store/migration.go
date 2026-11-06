package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"v2way/internal/config"
	"v2way/internal/jsonfile"
)

type LegacyAccount struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
}
type LegacyCertificate struct {
	ID     string          `json:"id"`
	Info   json.RawMessage `json:"info"`
	Secret []byte          `json:"secret"`
}
type LegacyExport struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ActiveVersion int64               `json:"activeVersion"`
	Account       *LegacyAccount      `json:"account"`
	Draft         Draft               `json:"draft"`
	Versions      []Version           `json:"versions"`
	Audits        []Audit             `json:"audits"`
	Certificates  []LegacyCertificate `json:"certificates"`
}

// BeginLegacyImport only accepts an explicit export and holds the instance lock
// through certificate conversion and FinishLegacyImport. It never reads SQLite.
func BeginLegacyImport(dir string, bundle LegacyExport) (*Store, error) {
	bundle = clone(bundle)
	if bundle.SchemaVersion != 1 || bundle.ActiveVersion < 0 || len(bundle.Versions) > 101 || bundle.Draft.Revision < 0 || bundle.Draft.Revision > maxInteger {
		return nil, errors.New("旧数据导出格式或数量无效")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	lock, err := lockDirectory(absolute)
	if err != nil {
		return nil, err
	}
	s := newStore(absolute, lock)
	done := false
	defer func() {
		if !done {
			s.Close()
		}
	}()
	sourceHash := Hash(bundle)
	var meta marker
	err = jsonfile.Read(filepath.Join(absolute, "storage.json"), &meta)
	if err == nil {
		if meta.Complete {
			return nil, errors.New("JSON 实例已经初始化或转换完成，禁止覆盖")
		}
		if meta.MigrationHash != sourceHash {
			return nil, errors.New("未完成转换与本次导出文件不匹配")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("存储标记损坏")
	} else {
		for _, name := range []string{"account.json", "draft.json", "gateway.json", "versions"} {
			if _, e := os.Stat(filepath.Join(absolute, name)); e == nil {
				return nil, errors.New("已有 JSON 状态缺少转换标记，禁止覆盖")
			}
		}
		meta = marker{SchemaVersion: 1, Format: "json", Complete: false, MigrationHash: sourceHash}
		if err = s.write(filepath.Join(absolute, "storage.json"), meta); err != nil {
			return nil, err
		}
	}
	write := func(name string, value any) error {
		path := filepath.Join(absolute, name)
		var old json.RawMessage
		if err := jsonfile.Read(path, &old); err == nil {
			if Hash(old) != Hash(value) {
				return fmt.Errorf("转换目标 %s 已有不同内容，拒绝覆盖", name)
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return s.write(path, value)
	}
	if bundle.Account != nil {
		account := Account{SchemaVersion: 1, Username: bundle.Account.Username, PasswordHash: bundle.Account.PasswordHash, Parameters: DefaultPasswordParameters(), Revision: 1, UpdatedAt: "1970-01-01T00:00:00Z"}
		if err = validateAccount(account); err != nil {
			return nil, err
		}
		if err = write("account.json", account); err != nil {
			return nil, err
		}
	}
	d := bundle.Draft
	d.SchemaVersion = 1
	d.Config = config.Normalize(d.Config)
	if err = write("draft.json", d); err != nil {
		return nil, err
	}
	sorted := append([]Version{}, bundle.Versions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	previous := []VersionRef{}
	found := bundle.ActiveVersion == 0
	for _, v := range sorted {
		if v.ID < 1 || v.ID > maxInteger {
			return nil, errors.New("旧版本号无效")
		}
		c := config.Normalize(v.Config)
		snapshot := Snapshot{SchemaVersion: 1, Version: v.ID, CreatedAt: v.CreatedAt, Release: Release{RequestID: v.RequestID, PayloadHash: "legacy-" + Hash(v), Actor: "legacy-import", Note: v.Note}, ConfigHash: Hash(c), History: append([]VersionRef{}, previous...), Config: c}
		if len(snapshot.History) > historyLimit {
			snapshot.History = snapshot.History[:historyLimit]
		}
		if err = validateSnapshot(snapshot); err != nil {
			return nil, err
		}
		if err = write(filepath.Join("versions", fmt.Sprintf("%012d.json", v.ID)), snapshot); err != nil {
			return nil, err
		}
		if v.ID == bundle.ActiveVersion {
			if err = write("gateway.json", snapshot); err != nil {
				return nil, err
			}
			found = true
		}
		previous = append([]VersionRef{{v.ID, Hash(snapshot)}}, previous...)
	}
	if !found || bundle.ActiveVersion == 0 && len(sorted) > 0 {
		return nil, errors.New("旧库运行版本与成功版本记录不一致")
	}
	if err = s.loadAuditsLocked(); err != nil {
		return nil, err
	}
	for _, old := range bundle.Audits {
		if old.Action == "" {
			return nil, errors.New("旧审计记录无效")
		}
		old.EventID = fmt.Sprintf("legacy-%s-%d", sourceHash[:16], old.ID)
		if err = s.appendAuditLocked(old); err != nil {
			return nil, err
		}
	}
	done = true
	return s, nil
}
func (s *Store) FinishLegacyImport(bundle LegacyExport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = nil
	s.auditSeq = 0
	s.auditEvents = map[string]bool{}
	if err := s.load(); err != nil {
		return err
	}
	if s.state.ReadOnly || s.state.DraftProblem != "" || s.state.AuditProblem != "" {
		return errors.New("转换后的 JSON 状态校验未通过")
	}
	return s.write(filepath.Join(s.dir, "storage.json"), marker{SchemaVersion: 1, Format: "json", Complete: true, MigrationHash: Hash(bundle)})
}

// Recover validates resources under the instance lock before preserving the
// original history and writing the selected configuration as a new commit.
func Recover(dir string, id int64, validate func(config.Config) error) error {
	if id < 1 || id > maxInteger {
		return errors.New("恢复版本号无效")
	}
	lock, err := lockDirectory(dir)
	if err != nil {
		return err
	}
	s := newStore(dir, lock)
	defer s.Close()
	var meta marker
	if err = jsonfile.Read(filepath.Join(dir, "storage.json"), &meta); err != nil || meta.Format != "json" || !meta.Complete {
		return errors.New("没有可恢复的完整 JSON 实例")
	}
	var chosen Snapshot
	if err = jsonfile.Read(snapshotPath(dir, id), &chosen); err != nil {
		return err
	}
	if err = validateSnapshot(chosen); err != nil {
		return err
	}
	if validate == nil {
		return errors.New("恢复需要运行资源校验")
	}
	if err = validate(chosen.Config); err != nil {
		return err
	}
	backupDir := filepath.Join(dir, "backups", "recovery-"+Token()[:16])
	if err = os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "versions"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !versionName.MatchString(entry.Name()) {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(dir, "versions", entry.Name()))
		if e != nil {
			return e
		}
		path := filepath.Join(backupDir, entry.Name())
		if e = os.WriteFile(path, raw, 0600); e != nil {
			return e
		}
		if e = jsonfile.Sync(path); e != nil {
			return e
		}
	}
	current := filepath.Join(dir, "gateway.json")
	if raw, err := os.ReadFile(current); err == nil {
		backup := filepath.Join(backupDir, "gateway.json")
		if err = os.WriteFile(backup, raw, 0600); err != nil {
			return err
		}
		if err = jsonfile.Sync(backup); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	maxID := id
	for _, entry := range entries {
		if versionName.MatchString(entry.Name()) {
			var n int64
			fmt.Sscanf(entry.Name(), "%d.json", &n)
			if n > maxID {
				maxID = n
			}
		}
	}
	if maxID >= maxInteger {
		return errors.New("恢复版本号已达上限")
	}
	chosen.Version = maxID + 1
	chosen.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	chosen.History = []VersionRef{}
	chosen.Release = Release{RequestID: Token(), PayloadHash: Hash(chosen.Config), Actor: "local-recovery", Note: fmt.Sprintf("显式恢复历史版本 %d", id), RollbackFrom: id}
	if err = jsonfile.Write(snapshotPath(dir, chosen.Version), chosen); err != nil {
		return err
	}
	return jsonfile.Write(current, chosen)
}
