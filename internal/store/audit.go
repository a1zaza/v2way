package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"v2way/internal/config"
	"v2way/internal/jsonfile"
)

const maxAuditBytes = 16 << 20

var auditName = regexp.MustCompile(`^audit-[0-9]{4}-[0-9]{2}\.jsonl$`)

type Audit struct {
	ID      int64  `json:"id"`
	EventID string `json:"eventId,omitempty"`
	Time    string `json:"time"`
	Action  string `json:"action"`
	Detail  string `json:"detail"`
	SiteID  string `json:"siteId,omitempty"`
	IP      string `json:"ip,omitempty"`
}

func (s *Store) loadAuditsLocked() error {
	dir := filepath.Join(s.dir, "logs")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names := []string{}
	for _, entry := range entries {
		if auditName.MatchString(entry.Name()) {
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("审计日志不能是符号链接")
			}
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for n, name := range names {
		month, err := time.Parse("2006-01", strings.TrimSuffix(strings.TrimPrefix(name, "audit-"), ".jsonl"))
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		oldest := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)
		if month.Before(oldest) {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil || info.Size() > maxAuditBytes {
			f.Close()
			return errors.New("审计日志超过大小上限")
		}
		reader := bufio.NewReader(f)
		offset := int64(0)
		for {
			line, e := reader.ReadBytes('\n')
			if len(line) > 1<<20 {
				f.Close()
				return errors.New("审计记录过长")
			}
			if errors.Is(e, io.EOF) {
				if len(line) > 0 {
					if n != len(names)-1 {
						f.Close()
						return errors.New("非最新审计文件含有不完整记录")
					}
					if err = f.Truncate(offset); err == nil {
						err = f.Sync()
					}
					if err != nil {
						f.Close()
						return err
					}
				}
				break
			}
			if e != nil {
				f.Close()
				return e
			}
			var a Audit
			if err = jsonfile.Decode(line, &a); err != nil || a.ID <= s.auditSeq || a.Time == "" || a.Action == "" {
				f.Close()
				return errors.New("审计记录损坏或序号倒退")
			}
			s.auditSeq = a.ID
			s.keepAuditLocked(a)
			offset += int64(len(line))
		}
		f.Close()
	}
	return nil
}
func (s *Store) keepAuditLocked(a Audit) {
	if a.EventID != "" {
		s.auditEvents[a.EventID] = true
	}
	s.audits = append(s.audits, a)
	if len(s.audits) > 200 {
		s.audits = s.audits[len(s.audits)-200:]
	}
}
func (s *Store) appendAuditLocked(a Audit) error {
	if s.state.AuditProblem != "" {
		return errors.New(s.state.AuditProblem)
	}
	if a.EventID != "" && s.auditEvents[a.EventID] {
		return nil
	}
	if len(a.Detail) > 8192 || len(a.Action) > 128 {
		return errors.New("审计记录过长")
	}
	s.auditSeq++
	a.ID = s.auditSeq
	if a.Time == "" {
		a.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if a.EventID == "" {
		a.EventID = Token()
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Join(s.dir, "logs")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = jsonfile.Sync(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, "audit-"+time.Now().UTC().Format("2006-01")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size()+int64(len(raw)) > maxAuditBytes {
		return errors.New("本月审计日志已达 16 MiB 上限")
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		s.state.AuditProblem = "审计追加未完成，需要检查末尾记录"
		return err
	}
	if err = jsonfile.Sync(path); err != nil {
		return err
	}
	s.keepAuditLocked(a)
	return nil
}
func auditSourceIP(values []string) string {
	if len(values) == 0 {
		return ""
	}
	ip, err := netip.ParseAddr(values[0])
	if err != nil || ip.Zone() != "" {
		return ""
	}
	return ip.Unmap().String()
}
func (s *Store) noteAuditLocked(event, action, detail string, sourceIP ...string) {
	if err := s.appendAuditLocked(Audit{EventID: event, Action: action, Detail: detail, IP: auditSourceIP(sourceIP)}); err != nil {
		s.state.Warning = "操作已提交，但审计日志写入失败"
	}
}

// siteNameLocked resolves published names first; draft-only sites can also
// generate accounts before their first publication.
func (s *Store) siteNameLocked(siteID string) string {
	for _, cfg := range []config.Config{s.active.Config, s.draft.Config} {
		for _, site := range cfg.Sites {
			if site.ID == siteID && strings.TrimSpace(site.Name) != "" {
				return site.Name
			}
		}
	}
	return siteID
}
func (s *Store) noteSiteAuditLocked(siteID, action, detail string, sourceIP ...string) {
	if err := s.appendAuditLocked(Audit{Action: action, Detail: fmt.Sprintf(detail, s.siteNameLocked(siteID)), SiteID: siteID, IP: auditSourceIP(sourceIP)}); err != nil {
		s.state.Warning = "操作已提交，但审计日志写入失败"
	}
}

// Legacy details used IDs directly. Resolve their display copy without rewriting
// persisted audit records; new records retain the name at the time of the event.
func (s *Store) displaySiteAuditLocked(a Audit) Audit {
	if a.SiteID != "" {
		return a
	}
	var prefixes []string
	switch a.Action {
	case "access.ip.authorized":
		prefixes = []string{"站点 "}
	case "access.account.updated":
		prefixes = []string{"更新站点 ", "停用站点 "}
	default:
		return a
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(a.Detail, prefix) {
			continue
		}
		siteID, rest, ok := strings.Cut(strings.TrimPrefix(a.Detail, prefix), " ")
		if !ok || !validSiteID(siteID) {
			return a
		}
		a.SiteID = siteID
		a.Detail = strings.TrimSpace(prefix) + "「" + s.siteNameLocked(siteID) + "」 " + rest
		return a
	}
	return a
}
func (s *Store) Audit(action, detail string, sourceIP ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("存储已关闭")
	}
	return s.appendAuditLocked(Audit{Action: action, Detail: detail, IP: auditSourceIP(sourceIP)})
}
func (s *Store) Audits() ([]Audit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.AuditProblem != "" {
		return nil, fmt.Errorf("%w：%s", ErrReadOnly, s.state.AuditProblem)
	}
	out := clone(s.audits)
	if out == nil {
		out = []Audit{}
	}
	for i := range out {
		out[i] = s.displaySiteAuditLocked(out[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}
