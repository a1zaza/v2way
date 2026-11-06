package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"v2way/internal/accessgate"
	"v2way/internal/password"
	"v2way/internal/sessioncookie"
	"v2way/internal/store"
)

const cookieName = sessioncookie.Name

func requestSourceIP(r *http.Request) string {
	ip, _ := accessgate.SourceIP(r.RemoteAddr)
	return ip
}

func HashPassword(value string) (string, error) { return password.Hash(value) }
func verify(value, encoded string) bool         { return password.Verify(value, encoded) }
func digest(value string) string                { b := sha256.Sum256([]byte(value)); return hex.EncodeToString(b[:]) }
func Initialize(s *store.Store, username, password string) error {
	if strings.TrimSpace(username) == "" || len(username) > 64 {
		return errors.New("管理员名称应为 1–64 个字符")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.InitializeAccount(username, hash)
}

type attempt struct {
	count int
	reset time.Time
}
type limiter struct {
	mu     sync.Mutex
	values map[string]attempt
	login  sync.Mutex
}

func (l *limiter) take(remote string) bool {
	h, _, _ := net.SplitHostPort(remote)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.values == nil {
		l.values = map[string]attempt{}
	}
	now := time.Now()
	if len(l.values) > 4096 {
		for k, v := range l.values {
			if now.After(v.reset) {
				delete(l.values, k)
			}
		}
		if len(l.values) > 4096 {
			return false
		}
	}
	a := l.values[h]
	if now.After(a.reset) {
		a = attempt{reset: now.Add(time.Minute)}
	}
	a.count++
	l.values[h] = a
	return a.count <= 5
}

type session struct {
	Username string `json:"username"`
	CSRF     string `json:"csrf"`
}

func (s *Server) authenticate(r *http.Request) (session, error) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return session{}, err
	}
	account, err := s.store.Account()
	if err != nil {
		return session{}, err
	}
	value, err := s.sessions.get(digest(cookie.Value), account.Revision)
	if err != nil {
		return session{}, err
	}
	return session{value.Username, value.CSRF}, nil
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		session, err := s.authenticate(r)
		if err != nil {
			fail(w, 401, "请先登录")
			return
		}
		respond(w, 200, session)
	case "POST":
		if !s.originAllowed(r) {
			fail(w, 403, "登录来源不被允许")
			return
		}
		if !s.limiter.take(r.RemoteAddr) {
			w.Header().Set("Retry-After", "60")
			fail(w, 429, "登录尝试过多，请稍后重试")
			return
		}
		select {
		case s.loginSlots <- struct{}{}:
			defer func() { <-s.loginSlots }()
		default:
			w.Header().Set("Retry-After", "1")
			fail(w, 429, "登录繁忙，请稍后重试")
			return
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeLimit(w, r, &in, 8192) {
			return
		}
		// Bound Argon2 memory regardless of the number of source addresses.
		s.limiter.login.Lock()
		defer s.limiter.login.Unlock()
		account, err := s.store.Account()
		if errors.Is(err, store.ErrNotFound) {
			fail(w, 503, "请先使用本地 init 命令初始化管理员")
			return
		}
		if err != nil {
			fail(w, 500, "读取账户失败")
			return
		}
		valid := verify(in.Password, account.PasswordHash)
		if !valid || subtle.ConstantTimeCompare([]byte(account.Username), []byte(in.Username)) != 1 {
			fail(w, 401, "用户名或密码不正确")
			return
		}
		token, csrf := store.Token(), store.Token()
		if err = s.sessions.create(digest(token), loginSession{CSRF: csrf, Username: account.Username, AccountRevision: account.Revision, Expires: time.Now().Add(12 * time.Hour)}); err != nil {
			fail(w, 503, "登录会话已达上限，请稍后再试")
			return
		}
		_ = s.store.Audit("session.login", "管理员登录", requestSourceIP(r))
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/api/admin/v1", HttpOnly: true, Secure: sessionSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		respond(w, 200, session{account.Username, csrf})
	case "DELETE":
		auth, err := s.authenticate(r)
		if err != nil {
			fail(w, 401, "会话已过期")
			return
		}
		if !s.originAllowed(r) || subtle.ConstantTimeCompare([]byte(auth.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			fail(w, 403, "请求校验失败")
			return
		}
		cookie, _ := r.Cookie(cookieName)
		s.sessions.remove(digest(cookie.Value))
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/api/admin/v1", MaxAge: -1, HttpOnly: true, Secure: sessionSecure(r), SameSite: http.SameSiteStrictMode})
		respond(w, 200, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, "GET, POST, DELETE")
	}
}
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// HTTPS may terminate at a proxy that preserves Host. Compare the browser
	// Origin directly; forwarded headers are not used to establish trust.
	if origin == scheme+"://"+r.Host || r.TLS == nil && origin == "https://"+r.Host {
		return true
	}
	for _, allowed := range s.options.Origins {
		if origin == allowed {
			return true
		}
	}
	return false
}
func (s *Server) hostAllowed(r *http.Request) bool {
	if s.options.AdminAddress == "" {
		return true
	}
	h, p, err := net.SplitHostPort(s.options.AdminAddress)
	if err != nil {
		return false
	}
	if strings.EqualFold(r.Host, net.JoinHostPort(h, p)) {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		rh, rp, err := net.SplitHostPort(r.Host)
		if err == nil && rp == p && (rh == "localhost" || net.ParseIP(rh) != nil && net.ParseIP(rh).IsLoopback()) {
			return true
		}
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsUnspecified() {
		u, err := url.Parse("//" + r.Host)
		if err == nil && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && (strings.EqualFold(u.Hostname(), "localhost") || net.ParseIP(u.Hostname()) != nil) {
			return true
		}
	}
	if s.engine != nil && s.engine.ServesHost(r.Host) {
		return true
	}
	for _, origin := range s.options.Origins {
		u, err := url.Parse(origin)
		if err == nil && strings.EqualFold(u.Host, r.Host) {
			return true
		}
	}
	return false
}

// sessionSecure is evaluated only after Origin validation on login/logout.
// It supports TLS termination without trusting X-Forwarded-Proto from clients.
func sessionSecure(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https://")
}
func ValidateAdminAddress(address string) error {
	_, p, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("管理端口范围为 1–65535")
	}
	return nil
}
