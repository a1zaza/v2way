package admin

import (
	"context"
	"errors"
	"sync"
	"time"
)

func (s *Server) StartSessionCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sessions.mu.Lock()
				for token, value := range s.sessions.values {
					if !time.Now().Before(value.Expires) {
						delete(s.sessions.values, token)
					}
				}
				s.sessions.mu.Unlock()
			}
		}
	}()
}

const maxSessions = 4096

type loginSession struct {
	CSRF            string
	Username        string
	AccountRevision int64
	Expires         time.Time
}
type sessionTable struct {
	mu     sync.Mutex
	values map[string]loginSession
}

func (t *sessionTable) create(token string, value loginSession) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.values == nil {
		t.values = map[string]loginSession{}
	}
	now := time.Now()
	for key, s := range t.values {
		if !now.Before(s.Expires) {
			delete(t.values, key)
		}
	}
	if len(t.values) >= maxSessions {
		return errors.New("登录会话已达上限")
	}
	t.values[token] = value
	return nil
}
func (t *sessionTable) get(token string, revision int64) (loginSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	value, ok := t.values[token]
	if !ok || !time.Now().Before(value.Expires) || value.AccountRevision != revision {
		delete(t.values, token)
		return loginSession{}, errors.New("会话已过期，请重新登录")
	}
	return value, nil
}
func (t *sessionTable) remove(token string) { t.mu.Lock(); delete(t.values, token); t.mu.Unlock() }
func (t *sessionTable) clear()              { t.mu.Lock(); t.values = map[string]loginSession{}; t.mu.Unlock() }
