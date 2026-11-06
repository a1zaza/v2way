package store

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type PasswordParameters struct {
	Algorithm   string `json:"algorithm"`
	Iterations  uint32 `json:"iterations"`
	MemoryKiB   uint32 `json:"memoryKiB"`
	Parallelism uint8  `json:"parallelism"`
	KeyLength   uint32 `json:"keyLength"`
}
type Account struct {
	SchemaVersion int                `json:"schemaVersion"`
	Username      string             `json:"username"`
	PasswordHash  string             `json:"passwordHash"`
	Parameters    PasswordParameters `json:"parameters"`
	Revision      int64              `json:"revision"`
	UpdatedAt     string             `json:"updatedAt"`
}

func DefaultPasswordParameters() PasswordParameters {
	return PasswordParameters{"argon2id", 3, 64 * 1024, 2, 32}
}
func validateAccount(a Account) error {
	if a.SchemaVersion != 1 || a.Revision < 1 || a.Revision > maxInteger || strings.TrimSpace(a.Username) == "" || len(a.Username) > 64 || a.Parameters != DefaultPasswordParameters() {
		return errors.New("管理员文件格式或密码参数不受支持")
	}
	parts := strings.Split(a.PasswordHash, ":")
	if len(parts) != 2 || len(parts[0]) != 32 || len(parts[1]) != 64 {
		return errors.New("密码摘要格式无效")
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return errors.New("密码盐格式无效")
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return errors.New("密码摘要格式无效")
	}
	return nil
}
func (s *Store) Account() (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account == nil {
		return Account{}, ErrNotFound
	}
	return *s.account, nil
}
func (s *Store) InitializeAccount(username, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(); err != nil {
		return err
	}
	if s.account != nil {
		return errors.New("管理员已存在，不会覆盖原账号")
	}
	a := Account{1, username, hash, DefaultPasswordParameters(), 1, time.Now().UTC().Format(time.RFC3339Nano)}
	if err := validateAccount(a); err != nil {
		return err
	}
	if err := s.writeStateLocked("account.json", "account", a, func() { s.account = &a }, Version{}); err != nil {
		return err
	}
	s.noteAuditLocked("account-1", "account.initialized", "初始化管理员")
	return nil
}
func (s *Store) UpdatePassword(revision int64, hash string) error {
	account, err := s.Account()
	if err != nil {
		return err
	}
	return s.UpdateAccount(revision, account.Username, hash)
}
func (s *Store) UpdateAccount(revision int64, username, hash string, sourceIP ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writableLocked(); err != nil {
		return err
	}
	if s.account == nil {
		return ErrNotFound
	}
	if revision != s.account.Revision || revision >= maxInteger {
		return ErrConflict
	}
	a := *s.account
	a.Username = username
	a.PasswordHash = hash
	a.Revision++
	a.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := validateAccount(a); err != nil {
		return err
	}
	if err := s.writeStateLocked("account.json", "account", a, func() { s.account = &a }, Version{}); err != nil {
		return err
	}
	s.noteAuditLocked("", "account.updated", "修改账户并撤销旧账户修订的会话", sourceIP...)
	return nil
}
