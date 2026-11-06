// Package store persists gateway state in independently verifiable JSON files.
package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"v2way/internal/config"
	"v2way/internal/jsonfile"
)

var (
	ErrConflict = errors.New("草稿或运行版本已被更新，请重新加载")
	ErrNotFound = errors.New("记录不存在或已超出保留范围")
	ErrReadOnly = errors.New("存储需要恢复，已暂停写入")
	ErrPending  = errors.New("文件已替换但持久化结果待确认，请先确认存储状态")
)

const maxInteger = int64(9007199254740991)
const historyLimit = 100

var versionName = regexp.MustCompile(`^[0-9]{12,16}\.json$`)

type Draft struct {
	SchemaVersion int           `json:"schemaVersion"`
	Config        config.Config `json:"config"`
	Revision      int64         `json:"revision"`
	BaseVersion   int64         `json:"baseVersion"`
	UpdatedAt     string        `json:"updatedAt,omitempty"`
	UpdatedBy     string        `json:"updatedBy,omitempty"`
}
type Version struct {
	ID        int64         `json:"id"`
	CreatedAt string        `json:"createdAt"`
	Note      string        `json:"note"`
	Config    config.Config `json:"config"`
	RequestID string        `json:"requestId"`
}
type PublishInput struct {
	Revision              int64  `json:"revision"`
	ExpectedActiveVersion int64  `json:"expectedActiveVersion"`
	RequestID             string `json:"requestId"`
	Note                  string `json:"note"`
	RollbackVersion       int64  `json:"rollbackVersion"`
	Actor                 string `json:"-"`
	SourceIP              string `json:"-"`
}
type Release struct {
	RequestID             string `json:"requestId"`
	PayloadHash           string `json:"payloadHash"`
	Actor                 string `json:"actor"`
	SourceRevision        int64  `json:"sourceRevision"`
	ExpectedActiveVersion int64  `json:"expectedActiveVersion"`
	Note                  string `json:"note"`
	RollbackFrom          int64  `json:"rollbackFrom,omitempty"`
}
type VersionRef struct {
	Version      int64  `json:"version"`
	SnapshotHash string `json:"snapshotHash"`
}
type Snapshot struct {
	SchemaVersion int           `json:"schemaVersion"`
	Version       int64         `json:"version"`
	CreatedAt     string        `json:"createdAt"`
	Release       Release       `json:"release"`
	ConfigHash    string        `json:"configHash"`
	History       []VersionRef  `json:"history"`
	Config        config.Config `json:"config"`
}
type marker struct {
	SchemaVersion int    `json:"schemaVersion"`
	Format        string `json:"format"`
	Complete      bool   `json:"complete"`
	MigrationHash string `json:"migrationHash,omitempty"`
}
type Status struct {
	Format         string `json:"format"`
	Pending        bool   `json:"pending"`
	ReadOnly       bool   `json:"readOnly"`
	Reason         string `json:"reason,omitempty"`
	DraftProblem   string `json:"draftProblem,omitempty"`
	HistoryProblem string `json:"historyProblem,omitempty"`
	AuditProblem   string `json:"auditProblem,omitempty"`
	Warning        string `json:"warning,omitempty"`
	AccessProblem  string `json:"accessProblem,omitempty"`
	AccessWarning  string `json:"accessWarning,omitempty"`
}
type pendingWrite struct {
	path, hash, kind string
	apply            func()
	version          Version
}
type Store struct {
	mu          sync.Mutex
	dir         string
	lock        *os.File
	closed      bool
	active      Snapshot
	draft       Draft
	account     *Account
	access      *AccessControl
	accessDirty bool
	history     map[int64]Snapshot
	nextID      int64
	hashes      map[string]string
	state       Status
	pending     *pendingWrite
	write       func(string, any) error
	auditSeq    int64
	audits      []Audit
	auditEvents map[string]bool
}

func Token() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}
func canonical(value any) []byte {
	raw, _ := json.Marshal(value)
	var generic any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&generic)
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(generic)
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'})
}
func Hash(value any) string {
	sum := sha256.Sum256(canonical(value))
	return hex.EncodeToString(sum[:])
}
func fingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, jsonfile.MaxBytes+1))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(raw) > jsonfile.MaxBytes {
		return "", errors.New("状态文件超过大小上限")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func clone[T any](value T) T {
	raw, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}
func emptySnapshot() Snapshot {
	return Snapshot{SchemaVersion: 1, Config: config.Empty(), History: []VersionRef{}}
}
func dto(s Snapshot) Version {
	return Version{ID: s.Version, CreatedAt: s.CreatedAt, Note: s.Release.Note, Config: clone(s.Config), RequestID: s.Release.RequestID}
}
func snapshotPath(dir string, id int64) string {
	return filepath.Join(dir, "versions", fmt.Sprintf("%012d.json", id))
}
func lockDirectory(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("数据目录已被其他 v2way 进程占用")
	}
	return lock, nil
}
func Open(dir string) (*Store, error) {
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
	var meta marker
	err = jsonfile.Read(filepath.Join(absolute, "storage.json"), &meta)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Stat(filepath.Join(absolute, "gateway.db")); e == nil {
			return nil, errors.New("检测到旧 SQLite 数据，请先离线导出并执行 migrate，不能初始化空实例")
		}
		for _, name := range []string{"gateway.json", "draft.json", "account.json", "access-control.json", "versions", "certificates", "acme"} {
			if _, e := os.Stat(filepath.Join(absolute, name)); e == nil {
				return nil, errors.New("数据目录包含已有文件但缺少 storage.json，需显式恢复")
			}
		}
		s.draft = Draft{SchemaVersion: 1, Config: config.Empty()}
		if err = s.write(filepath.Join(absolute, "draft.json"), s.draft); err != nil {
			return nil, err
		}
		meta = marker{SchemaVersion: 1, Format: "json", Complete: true}
		if err = s.write(filepath.Join(absolute, "storage.json"), meta); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, errors.New("storage.json 损坏")
	}
	if meta.SchemaVersion != 1 || meta.Format != "json" || !meta.Complete {
		return nil, errors.New("存储格式不支持或数据转换尚未完成")
	}
	if err = s.load(); err != nil {
		return nil, err
	}
	done = true
	return s, nil
}
func newStore(dir string, lock *os.File) *Store {
	return &Store{dir: dir, lock: lock, active: emptySnapshot(), history: map[int64]Snapshot{}, hashes: map[string]string{}, write: jsonfile.Write, state: Status{Format: "json"}, auditEvents: map[string]bool{}}
}
func (s *Store) load() error {
	currentPath := filepath.Join(s.dir, "gateway.json")
	err := jsonfile.Read(currentPath, &s.active)
	entries, e := os.ReadDir(filepath.Join(s.dir, "versions"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for _, entry := range entries {
		if versionName.MatchString(entry.Name()) {
			id, _ := strconv.ParseInt(strings.TrimSuffix(entry.Name(), ".json"), 10, 64)
			if id > maxInteger {
				return errors.New("版本号超过安全整数范围")
			}
			if id > s.nextID {
				s.nextID = id
			}
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		if len(entries) > 0 {
			return errors.New("运行配置缺失但历史目录存在文件，需显式恢复")
		}
		s.active = emptySnapshot()
	} else if err != nil {
		return errors.New("gateway.json 损坏，不能重建为空配置")
	} else {
		if err = validateSnapshot(s.active); err != nil {
			return err
		}
		if err = jsonfile.Sync(currentPath); err != nil {
			return errors.New("无法确认运行配置已持久化")
		}
	}
	if s.active.Version > s.nextID {
		s.nextID = s.active.Version
	}
	s.history[s.active.Version] = s.active
	for _, ref := range s.active.History {
		var old Snapshot
		if err = jsonfile.Read(snapshotPath(s.dir, ref.Version), &old); err != nil || validateSnapshot(old) != nil || old.Version != ref.Version || Hash(old) != ref.SnapshotHash {
			s.state.HistoryProblem = "历史配置缺失、损坏或摘要不匹配"
			break
		}
		s.history[ref.Version] = old
	}
	if s.active.Version > 0 {
		var archived Snapshot
		if err = jsonfile.Read(snapshotPath(s.dir, s.active.Version), &archived); err != nil || Hash(archived) != Hash(s.active) {
			s.state.HistoryProblem = "当前版本的历史副本缺失或损坏"
		}
	}
	err = jsonfile.Read(filepath.Join(s.dir, "draft.json"), &s.draft)
	if err != nil || s.draft.SchemaVersion != 1 || s.draft.Revision < 0 || s.draft.Revision > maxInteger || s.draft.BaseVersion < 0 {
		s.state.DraftProblem = "草稿文件缺失或损坏，可从运行配置显式重建"
	} else {
		s.draft.Config = config.Normalize(s.draft.Config)
	}
	var account Account
	err = jsonfile.Read(filepath.Join(s.dir, "account.json"), &account)
	if err == nil {
		if err = validateAccount(account); err != nil {
			return err
		}
		s.account = &account
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("account.json 损坏，禁止重置账户")
	}
	s.loadAccessLocked()
	for _, name := range []string{"storage.json", "gateway.json", "draft.json", "account.json", "access-control.json"} {
		hash, err := fingerprint(filepath.Join(s.dir, name))
		if err != nil {
			if name == "access-control.json" {
				s.state.AccessProblem = "访问授权文件无法读取，受保护站点已拒绝访问"
				continue
			}
			return err
		}
		s.hashes[name] = hash
	}
	if s.state.HistoryProblem != "" {
		s.state.ReadOnly = true
		s.state.Reason = s.state.HistoryProblem
	}
	if err = s.loadAuditsLocked(); err != nil {
		s.state.AuditProblem = "审计日志损坏或无法读取"
	}
	if s.active.Version > 0 && s.state.AuditProblem == "" {
		s.noteAuditLocked("release-"+strconv.FormatInt(s.active.Version, 10), "config.published", fmt.Sprintf("激活配置版本 %d", s.active.Version))
	}
	return nil
}
func validateSnapshot(v Snapshot) error {
	if v.SchemaVersion != 1 || v.Version < 1 || v.Version > maxInteger || len(v.History) > historyLimit || v.Release.RequestID == "" || v.ConfigHash != Hash(config.Normalize(clone(v.Config))) {
		return errors.New("运行配置格式、版本或摘要无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, v.CreatedAt); err != nil {
		return errors.New("发布记录时间无效")
	}
	if len(config.ValidateStored(v.Config)) > 0 {
		return errors.New("已发布配置语义校验失败")
	}
	seen := map[int64]bool{}
	for _, ref := range v.History {
		if ref.Version < 1 || ref.Version >= v.Version || seen[ref.Version] || len(ref.SnapshotHash) != 64 {
			return errors.New("历史版本索引无效")
		}
		seen[ref.Version] = true
	}
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	flushErr := s.flushAccessLocked()
	s.closed = true
	if s.lock != nil {
		unix.Flock(int(s.lock.Fd()), unix.LOCK_UN)
		err := s.lock.Close()
		s.lock = nil
		if err != nil {
			return err
		}
		return flushErr
	}
	return flushErr
}
func (s *Store) Dir() string { return s.dir }
func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.state
	status.Pending = s.pending != nil
	return status
}
func (s *Store) writableLocked() error {
	if s.closed {
		return errors.New("存储已关闭")
	}
	if s.pending != nil {
		return ErrPending
	}
	if s.state.ReadOnly {
		return ErrReadOnly
	}
	return nil
}
func (s *Store) unchangedLocked(name string) error {
	hash, err := fingerprint(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	if hash != s.hashes[name] {
		return ErrConflict
	}
	return nil
}
func (s *Store) rememberLocked(name string) {
	s.hashes[name], _ = fingerprint(filepath.Join(s.dir, name))
}
func (s *Store) writeStateLocked(name, kind string, value any, apply func(), version Version) error {
	if err := s.unchangedLocked(name); err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)
	if err := s.write(path, value); err != nil {
		if errors.Is(err, jsonfile.ErrUncertain) {
			hash, _ := fingerprint(path)
			s.pending = &pendingWrite{path: path, hash: hash, kind: kind, apply: apply, version: version}
			return ErrPending
		}
		return err
	}
	apply()
	s.rememberLocked(name)
	return nil
}
func (s *Store) Active() (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return dto(s.active), nil
}
func (s *Store) Draft() (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.DraftProblem != "" {
		return Draft{}, ErrReadOnly
	}
	return clone(s.draft), nil
}
func (s *Store) SaveDraft(c config.Config, revision int64, sourceIP ...string) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(); err != nil {
		return Draft{}, err
	}
	if s.state.DraftProblem != "" {
		return Draft{}, ErrReadOnly
	}
	if revision != s.draft.Revision || revision >= maxInteger {
		return Draft{}, ErrConflict
	}
	d := Draft{SchemaVersion: 1, Config: config.Normalize(clone(c)), Revision: revision + 1, BaseVersion: s.draft.BaseVersion, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedBy: "admin"}
	if err := s.writeStateLocked("draft.json", "draft", d, func() { s.draft = d }, Version{}); err != nil {
		return Draft{}, err
	}
	s.noteAuditLocked("", "draft.saved", "保存配置草稿", sourceIP...)
	return clone(d), nil
}
func (s *Store) versionLocked(id int64) (Version, error) {
	if id == s.active.Version {
		return dto(s.active), nil
	}
	if s.state.HistoryProblem != "" {
		return Version{}, ErrReadOnly
	}
	v, ok := s.history[id]
	if !ok {
		return Version{}, ErrNotFound
	}
	return dto(v), nil
}
func (s *Store) Version(id int64) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versionLocked(id)
}
func (s *Store) ByRequest(request string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil && s.pending.kind == "gateway" && s.pending.version.RequestID == request {
		return Version{}, ErrPending
	}
	for _, v := range s.history {
		if v.Release.RequestID == request && v.Version > 0 {
			return dto(v), nil
		}
	}
	return Version{}, ErrNotFound
}
func (s *Store) CheckReplay(in PublishInput) (Version, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil && s.pending.kind == "gateway" && s.pending.version.RequestID == in.RequestID {
		return Version{}, false, ErrPending
	}
	for _, v := range s.history {
		if v.Version > 0 && v.Release.RequestID == in.RequestID {
			if v.Release.PayloadHash != Hash(in) {
				return Version{}, false, ErrConflict
			}
			return dto(v), true, nil
		}
	}
	return Version{}, false, nil
}
func (s *Store) Commit(c config.Config, in PublishInput) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(); err != nil {
		return Version{}, err
	}
	if s.state.DraftProblem != "" {
		return Version{}, ErrReadOnly
	}
	if len(in.RequestID) < 8 || len(in.RequestID) > 128 || len(in.Note) > 512 || in.Revision < 0 || in.ExpectedActiveVersion < 0 || in.RollbackVersion < 0 {
		return Version{}, errors.New("发布元信息无效")
	}
	if in.Revision != s.draft.Revision || in.ExpectedActiveVersion != s.active.Version {
		return Version{}, ErrConflict
	}
	for _, name := range []string{"storage.json", "gateway.json", "draft.json"} {
		if err := s.unchangedLocked(name); err != nil {
			return Version{}, err
		}
	}
	for id, known := range s.history {
		if id == 0 {
			continue
		}
		var disk Snapshot
		if err := jsonfile.Read(snapshotPath(s.dir, id), &disk); err != nil || Hash(disk) != Hash(known) {
			s.state.ReadOnly = true
			s.state.HistoryProblem = "历史文件在运行期间被修改或删除"
			s.state.Reason = s.state.HistoryProblem
			return Version{}, ErrReadOnly
		}
	}
	c = config.Normalize(clone(c))
	if len(config.Validate(c)) > 0 {
		return Version{}, errors.New("配置语义校验失败")
	}
	if s.nextID >= maxInteger {
		return Version{}, errors.New("版本号已达上限")
	}
	s.nextID++
	refs := []VersionRef{}
	if s.active.Version > 0 {
		refs = append(refs, VersionRef{s.active.Version, Hash(s.active)})
		refs = append(refs, s.active.History...)
	}
	if len(refs) > historyLimit {
		refs = refs[:historyLimit]
	}
	v := Snapshot{SchemaVersion: 1, Version: s.nextID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Release: Release{RequestID: in.RequestID, PayloadHash: Hash(in), Actor: in.Actor, SourceRevision: in.Revision, ExpectedActiveVersion: in.ExpectedActiveVersion, Note: in.Note, RollbackFrom: in.RollbackVersion}, ConfigHash: Hash(c), History: refs, Config: c}
	if in.RollbackVersion > 0 {
		v.Release.Note = fmt.Sprintf("回滚至版本 %d：%s", in.RollbackVersion, in.Note)
	}
	if _, err := os.Lstat(snapshotPath(s.dir, v.Version)); !errors.Is(err, os.ErrNotExist) {
		return Version{}, errors.New("候选版本路径已存在，禁止覆盖")
	}
	if err := s.write(snapshotPath(s.dir, v.Version), v); err != nil {
		return Version{}, err
	}
	apply := func() {
		s.active = v
		kept := map[int64]Snapshot{v.Version: v}
		for _, ref := range v.History {
			kept[ref.Version] = s.history[ref.Version]
		}
		s.history = kept
	}
	if err := s.writeStateLocked("gateway.json", "gateway", v, apply, dto(v)); err != nil {
		return Version{}, err
	}
	s.noteAuditLocked("release-"+strconv.FormatInt(v.Version, 10), "config.published", fmt.Sprintf("激活配置版本 %d", v.Version), in.SourceIP)
	s.cleanupVersionsLocked()
	return dto(v), nil
}
func (s *Store) Versions() ([]Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.HistoryProblem != "" {
		return nil, ErrReadOnly
	}
	out := []Version{}
	for _, v := range s.history {
		if v.Version > 0 {
			out = append(out, dto(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}
func (s *Store) cleanupVersionsLocked() {
	entries, err := os.ReadDir(filepath.Join(s.dir, "versions"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !versionName.MatchString(entry.Name()) {
			continue
		}
		id, _ := strconv.ParseInt(strings.TrimSuffix(entry.Name(), ".json"), 10, 64)
		if _, ok := s.history[id]; !ok {
			_ = os.Remove(filepath.Join(s.dir, "versions", entry.Name()))
		}
	}
}
func (s *Store) PendingVersion() (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return Version{}, ErrNotFound
	}
	if s.pending.kind == "gateway" {
		return clone(s.pending.version), nil
	}
	return dto(s.active), nil
}
func (s *Store) ConfirmPending(sourceIP ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return ErrNotFound
	}
	hash, err := fingerprint(s.pending.path)
	if err != nil || hash != s.pending.hash {
		return errors.New("待确认文件已再次变化，需离线恢复")
	}
	if err = jsonfile.Sync(s.pending.path); err != nil {
		return ErrPending
	}
	pending := s.pending
	pending.apply()
	s.rememberLocked(filepath.Base(pending.path))
	s.pending = nil
	if pending.kind == "gateway" {
		s.noteAuditLocked("release-"+strconv.FormatInt(s.active.Version, 10), "config.published", fmt.Sprintf("确认激活配置版本 %d", s.active.Version), sourceIP...)
		s.cleanupVersionsLocked()
	}
	return nil
}
func (s *Store) ResetDraft() (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(); err != nil {
		return Draft{}, err
	}
	path := filepath.Join(s.dir, "draft.json")
	if s.state.DraftProblem != "" {
		if _, err := os.Stat(path); err == nil {
			raw, err := os.ReadFile(path)
			if err != nil {
				return Draft{}, err
			}
			backup := path + ".damaged-" + Token()[:12]
			if err = os.WriteFile(backup, raw, 0600); err != nil {
				return Draft{}, err
			}
			if err = jsonfile.Sync(backup); err != nil {
				return Draft{}, err
			}
		}
		s.rememberLocked("draft.json")
	}
	revision := s.draft.Revision + 1
	if revision < 1 || revision > maxInteger {
		revision = 1
	}
	if revision < time.Now().UnixMilli() {
		revision = time.Now().UnixMilli()
	}
	d := Draft{SchemaVersion: 1, Revision: revision, BaseVersion: s.active.Version, Config: clone(s.active.Config), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedBy: "admin"}
	if err := s.writeStateLocked("draft.json", "draft", d, func() { s.draft = d; s.state.DraftProblem = "" }, Version{}); err != nil {
		return Draft{}, err
	}
	return clone(d), nil
}
func DecodeConfig(raw []byte) (config.Config, error) {
	var fields map[string]json.RawMessage
	if err := jsonfile.Decode(raw, &fields); err != nil {
		return config.Config{}, err
	}
	if _, ok := fields["schemaVersion"]; ok {
		var snapshot Snapshot
		if err := jsonfile.Decode(raw, &snapshot); err != nil {
			return config.Config{}, err
		}
		if err := validateSnapshot(snapshot); err != nil {
			return config.Config{}, err
		}
		return config.Normalize(snapshot.Config), nil
	}
	var c config.Config
	if err := jsonfile.Decode(raw, &c); err != nil {
		return c, err
	}
	if _, ok := fields["sites"]; !ok {
		return c, errors.New("配置缺少 sites")
	}
	if _, ok := fields["upstreams"]; !ok {
		return c, errors.New("配置缺少 upstreams")
	}
	return config.Normalize(c), nil
}
