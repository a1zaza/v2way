package store

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"time"

	"v2way/internal/config"
	"v2way/internal/jsonfile"
)

func grantKey(siteID, ip string) string { return siteID + "|" + ip }
func validSiteID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

const AccessLifetime = 48 * time.Hour
const AccessLimit = 4096
const SiteAccessLimit = 8192

type AccessGrant struct {
	SiteID          string    `json:"siteId,omitempty"`
	ID              string    `json:"id"`
	IP              string    `json:"ip"`
	Username        string    `json:"username"`
	AccountRevision int64     `json:"accountRevision"`
	GrantedAt       time.Time `json:"grantedAt"`
	LastAccessAt    time.Time `json:"lastAccessAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}
type SiteAccessAccount struct {
	Account  Account `json:"account"`
	Disabled bool    `json:"disabled,omitempty"`
}
type AccessControl struct {
	Accounts      map[string]SiteAccessAccount `json:"accounts,omitempty"`
	SchemaVersion int                          `json:"schemaVersion"`
	Revision      int64                        `json:"revision"`
	Account       *Account                     `json:"account,omitempty"`
	Grants        map[string]AccessGrant       `json:"grants"`
	Disabled      bool                         `json:"disabled,omitempty"`
}
type AccessStatus struct {
	Initialized bool   `json:"initialized"`
	Enabled     bool   `json:"enabled"`
	Username    string `json:"username"`
	Revision    int64  `json:"revision"`
	Count       int    `json:"count"`
	Problem     string `json:"problem,omitempty"`
	Warning     string `json:"warning,omitempty"`
	Pending     bool   `json:"pending"`
}

func (s *Store) loadAccessLocked() {
	var value AccessControl
	err := jsonfile.Read(filepath.Join(s.dir, "access-control.json"), &value)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		err = validateAccess(value)
	}
	if err == nil {
		err = jsonfile.Sync(filepath.Join(s.dir, "access-control.json"))
	}
	if err != nil {
		s.state.AccessProblem = "访问授权文件损坏或无法确认持久化，受保护站点已拒绝访问"
		return
	}
	if value.SchemaVersion < 3 {
		accounts := map[string]SiteAccessAccount{}
		ids := map[string]bool{}
		for _, cfg := range []config.Config{s.active.Config, s.draft.Config} {
			for _, site := range cfg.Sites {
				if site.RequireIPLogin {
					ids[site.ID] = true
				}
				for _, route := range site.Routes {
					if route.Action == config.LoginAction {
						ids[site.ID] = true
					}
				}
			}
		}
		if value.SchemaVersion == 2 {
			for _, grant := range value.Grants {
				ids[grant.SiteID] = true
			}
		} else {
			value.Grants = map[string]AccessGrant{}
		}
		for id := range ids {
			accounts[id] = SiteAccessAccount{Account: *value.Account, Disabled: value.Disabled}
		}
		if value.Disabled {
			value.Grants = map[string]AccessGrant{}
		}
		if value.Revision >= maxInteger {
			s.state.AccessProblem = "访问授权修订已达上限"
			return
		}
		value.Revision++
		value.SchemaVersion = 3
		value.Accounts = accounts
		value.Account = nil
		value.Disabled = false
		s.accessDirty = true
	}

	s.access = &value
}
func validateAccess(value AccessControl) error {
	if value.SchemaVersion < 1 || value.SchemaVersion > 3 || value.Revision < 1 || value.Revision > maxInteger || len(value.Grants) > AccessLimit || value.Grants == nil {
		return errors.New("访问授权格式无效")
	}
	if value.SchemaVersion < 3 {
		if value.Account == nil || validateAccount(*value.Account) != nil {
			return errors.New("旧访问账号无效")
		}
	} else {
		if value.Account != nil || value.Disabled || value.Accounts == nil || len(value.Accounts) > SiteAccessLimit {
			return errors.New("站点账号格式无效")
		}
		for id, account := range value.Accounts {
			if !validSiteID(id) || validateAccount(account.Account) != nil {
				return errors.New("站点账号无效")
			}
		}
	}
	seen := map[string]bool{}
	for key, grant := range value.Grants {
		account := value.Account
		if value.SchemaVersion == 3 {
			entry, ok := value.Accounts[grant.SiteID]
			if !ok || entry.Disabled {
				return errors.New("授权所属站点账号不存在或已停用")
			}
			account = &entry.Account
		}
		expected := grant.IP
		if value.SchemaVersion >= 2 {
			expected = grantKey(grant.SiteID, grant.IP)
			if !validSiteID(grant.SiteID) {
				return errors.New("授权站点无效")
			}
		}
		address, err := netip.ParseAddr(grant.IP)
		if err != nil || address.Unmap().String() != grant.IP || key != expected || len(grant.ID) != 64 || grant.Username != account.Username || grant.AccountRevision != account.Revision || grant.GrantedAt.IsZero() || grant.LastAccessAt.Before(grant.GrantedAt) || !grant.ExpiresAt.Equal(grant.LastAccessAt.Add(AccessLifetime)) {
			return errors.New("访问授权记录无效")
		}
		if _, err := hex.DecodeString(grant.ID); err != nil || seen[grant.ID] {
			return errors.New("授权记录标识无效或重复")
		}
		seen[grant.ID] = true
	}
	return nil
}
func (s *Store) siteAccessStatusLocked(siteID string, now time.Time) AccessStatus {
	result := AccessStatus{Problem: s.state.AccessProblem, Warning: s.state.AccessWarning, Pending: s.pending != nil}
	if s.access == nil {
		return result
	}
	result.Revision = s.access.Revision
	if siteID != "" {
		result.Revision = 0
		if entry, ok := s.access.Accounts[siteID]; ok {
			result.Initialized = true
			result.Enabled = !entry.Disabled
			result.Username = entry.Account.Username
			result.Revision = entry.Account.Revision
		}
	} else {
		result.Initialized = len(s.access.Accounts) > 0
		for _, entry := range s.access.Accounts {
			result.Enabled = result.Enabled || !entry.Disabled
		}
	}
	for _, grant := range s.access.Grants {
		if (siteID == "" || grant.SiteID == siteID) && now.Before(grant.ExpiresAt) {
			result.Count++
		}
	}
	return result
}
func (s *Store) SiteAccessStatus(siteID string, now time.Time) AccessStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.siteAccessStatusLocked(siteID, now)
}
func (s *Store) AccessStatus(now time.Time) AccessStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.siteAccessStatusLocked("", now)
}
func (s *Store) AccessAccount(siteID string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return Account{}, ErrPending
	}
	if s.state.AccessProblem != "" {
		return Account{}, errors.New(s.state.AccessProblem)
	}
	if s.access == nil {
		return Account{}, ErrNotFound
	}
	entry, ok := s.access.Accounts[siteID]
	if !ok || entry.Disabled {
		return Account{}, ErrNotFound
	}
	return entry.Account, nil
}
func (s *Store) writeAccessLocked(value AccessControl) error {
	if err := s.writableLocked(); err != nil {
		return err
	}
	if s.state.AccessProblem != "" {
		return errors.New(s.state.AccessProblem)
	}
	if err := validateAccess(value); err != nil {
		return err
	}
	return s.writeStateLocked("access-control.json", "access-control", value, func() { s.access = &value; s.accessDirty = false; s.state.AccessWarning = "" }, Version{})
}
func (s *Store) UpdateAccessAccount(siteID string, revision int64, username, hash string, now time.Time, enabled ...bool) error {
	return s.updateAccessAccount(siteID, revision, username, hash, now, "", enabled...)
}
func (s *Store) UpdateAccessAccountFrom(siteID string, revision int64, username, hash string, now time.Time, sourceIP string) error {
	return s.updateAccessAccount(siteID, revision, username, hash, now, sourceIP)
}
func (s *Store) updateAccessAccount(siteID string, revision int64, username, hash string, now time.Time, sourceIP string, enabled ...bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSiteID(siteID) {
		return errors.New("站点标识无效")
	}
	next := AccessControl{SchemaVersion: 3, Revision: 1, Accounts: map[string]SiteAccessAccount{}, Grants: map[string]AccessGrant{}}
	if s.access != nil {
		next = clone(*s.access)
		if next.Revision >= maxInteger {
			return ErrConflict
		}
		next.Revision++
	}
	old, exists := next.Accounts[siteID]
	current := int64(0)
	if exists {
		current = old.Account.Revision
	}
	if revision != current || current >= maxInteger {
		return ErrConflict
	}
	if !exists && len(next.Accounts) >= SiteAccessLimit {
		return errors.New("站点访问账号已达上限")
	}
	account := Account{1, username, hash, DefaultPasswordParameters(), current + 1, now.UTC().Format(time.RFC3339Nano)}
	entry := SiteAccessAccount{Account: account}
	if len(enabled) > 0 {
		entry.Disabled = !enabled[0]
	}
	next.Accounts[siteID] = entry
	for key, grant := range next.Grants {
		if grant.SiteID == siteID {
			if entry.Disabled {
				delete(next.Grants, key)
				continue
			}
			// Carry existing grants across credential rotation without renewing
			// them or replacing IDs used by live connections and revoke actions.
			grant.Username = account.Username
			grant.AccountRevision = account.Revision
			next.Grants[key] = grant
		}
	}
	if err := s.writeAccessLocked(next); err != nil {
		return err
	}
	detail := "更新站点「%s」访问码，保留已有 IP 授权"
	if entry.Disabled {
		detail = "停用站点「%s」访问码并撤销本站 IP 授权"
	}
	s.noteSiteAuditLocked(siteID, "access.account.updated", detail, sourceIP)
	return nil
}
func (s *Store) AuthorizeIP(siteID, ip string, accountRevision int64, now time.Time) (AccessGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access == nil {
		return AccessGrant{}, ErrConflict
	}
	entry, exists := s.access.Accounts[siteID]
	if !exists || entry.Disabled || entry.Account.Revision != accountRevision {
		return AccessGrant{}, ErrConflict
	}
	address, err := netip.ParseAddr(ip)
	if err != nil || address.Zone() != "" {
		return AccessGrant{}, errors.New("来源 IP 无效")
	}
	if !validSiteID(siteID) {
		return AccessGrant{}, errors.New("授权站点无效")
	}
	ip = address.Unmap().String()
	key := grantKey(siteID, ip)
	next := clone(*s.access)
	for key, grant := range next.Grants {
		if !now.Before(grant.ExpiresAt) {
			delete(next.Grants, key)
		}
	}
	if _, exists := next.Grants[key]; !exists && len(next.Grants) >= AccessLimit {
		return AccessGrant{}, errors.New("IP 授权已达容量上限")
	}
	if next.Revision >= maxInteger {
		return AccessGrant{}, ErrConflict
	}
	next.Revision++
	grant := AccessGrant{SiteID: siteID, ID: Token(), IP: ip, Username: entry.Account.Username, AccountRevision: accountRevision, GrantedAt: now.UTC(), LastAccessAt: now.UTC(), ExpiresAt: now.UTC().Add(AccessLifetime)}
	next.Grants[key] = grant
	if err := s.writeAccessLocked(next); err != nil {
		return AccessGrant{}, err
	}
	s.noteSiteAuditLocked(siteID, "access.ip.authorized", "站点「%s」授权来源 IP "+ip, ip)
	return grant, nil
}
func (s *Store) CheckAccessIP(siteID, ip string, now time.Time) (AccessGrant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pending != nil || s.state.AccessProblem != "" || s.access == nil {
		return AccessGrant{}, false
	}
	entry, exists := s.access.Accounts[siteID]
	grant, ok := s.access.Grants[grantKey(siteID, ip)]
	return grant, exists && !entry.Disabled && ok && now.Before(grant.ExpiresAt) && grant.AccountRevision == entry.Account.Revision
}
func (s *Store) TouchAccessIP(siteID, ip, id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pending != nil || s.state.AccessProblem != "" || s.state.AccessWarning != "" || s.access == nil {
		return
	}
	entry, exists := s.access.Accounts[siteID]
	grant, ok := s.access.Grants[grantKey(siteID, ip)]
	if !exists || entry.Disabled || !ok || grant.ID != id || grant.AccountRevision != entry.Account.Revision || !now.Before(grant.ExpiresAt) || !now.After(grant.LastAccessAt) {
		return
	}
	grant.LastAccessAt = now.UTC()
	grant.ExpiresAt = now.UTC().Add(AccessLifetime)
	s.access.Grants[grantKey(siteID, ip)] = grant
	s.accessDirty = true
}
func (s *Store) FlushAccess() error { s.mu.Lock(); defer s.mu.Unlock(); return s.flushAccessLocked() }
func (s *Store) flushAccessLocked() error {
	if !s.accessDirty || s.access == nil {
		return nil
	}
	if err := s.writeAccessLocked(clone(*s.access)); err != nil {
		s.state.AccessWarning = "访问授权续期落盘失败，自动续期已暂停"
		return err
	}
	return nil
}
func (s *Store) AccessGrants(now time.Time, offset, limit int) ([]AccessGrant, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []AccessGrant{}
	if s.access != nil {
		for _, grant := range s.access.Grants {
			if now.Before(grant.ExpiresAt) {
				values = append(values, grant)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].GrantedAt.After(values[j].GrantedAt) })
	total := len(values)
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return values[offset:end], total
}

// AccessGrantSnapshot returns immutable display data and its security revision together.
func (s *Store) AccessGrantSnapshot(now time.Time) ([]AccessGrant, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []AccessGrant{}
	revision := int64(0)
	if s.access != nil {
		revision = s.access.Revision
		for _, grant := range s.access.Grants {
			if now.Before(grant.ExpiresAt) {
				values = append(values, grant)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].GrantedAt.After(values[j].GrantedAt) })
	return values, revision
}
func (s *Store) PruneAccess(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pending != nil || s.access == nil || s.state.AccessProblem != "" {
		return
	}
	for ip, grant := range s.access.Grants {
		if !now.Before(grant.ExpiresAt) {
			delete(s.access.Grants, ip)
			s.accessDirty = true
		}
	}
}
func (s *Store) RevokeAccess(id string, revision int64, sourceIP ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access == nil {
		return ErrNotFound
	}
	if revision >= 0 && revision != s.access.Revision {
		return ErrConflict
	}
	next := clone(*s.access)
	if id == "" {
		next.Grants = map[string]AccessGrant{}
	} else {
		found := false
		for ip, grant := range next.Grants {
			if grant.ID == id {
				delete(next.Grants, ip)
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
	}
	if next.Revision >= maxInteger {
		return ErrConflict
	}
	next.Revision++
	if err := s.writeAccessLocked(next); err != nil {
		return err
	}
	s.noteAuditLocked("", "access.ip.revoked", fmt.Sprintf("撤销 IP 授权，当前记录 %d 条", len(next.Grants)), sourceIP...)
	return nil
}
