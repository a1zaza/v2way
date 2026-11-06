package admin

import (
	"errors"

	"v2way/internal/store"
)

// InitializeOnStartup returns a random password only when creating the account.
// The store holds an exclusive instance lock; subsequent starts preserve it.
func InitializeOnStartup(s *store.Store) (string, error) {
	if _, err := s.Account(); err == nil {
		return "", nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	password := store.Token()[:8]
	if err := Initialize(s, "admin", password); err != nil {
		return "", err
	}
	return password, nil
}
