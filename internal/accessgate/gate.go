// Package accessgate provides the independent IP authorization login endpoint.
package accessgate

import (
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"v2way/internal/password"
	"v2way/internal/store"
	"v2way/web"
)

//go:embed login.html
var loginHTML string
var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

type Entry struct{ SiteID, SiteName, LoginPath string }

type formSession struct {
	SiteID                          string
	IP, Origin, Path, CSRF, GrantID string
	Expires                         time.Time
}
type attempt struct {
	Count int
	Reset time.Time
}
type Gate struct {
	store      *store.Store
	mu         sync.Mutex
	forms      map[string]formSession
	attempts   map[string]attempt
	verifySlot chan struct{}
	dummyHash  string
	now        func() time.Time
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
}

func New(s *store.Store) *Gate {
	dummy, _ := password.Hash(store.Token())
	g := &Gate{store: s, forms: map[string]formSession{}, attempts: map[string]attempt{}, verifySlot: make(chan struct{}, 1), dummyHash: dummy, now: time.Now, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(g.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.PruneAccess(g.now())
				_ = s.FlushAccess()
			case <-g.stop:
				_ = s.FlushAccess()
				return
			}
		}
	}()
	return g
}
func (g *Gate) Close() { g.closeOnce.Do(func() { close(g.stop); <-g.done }) }
func (g *Gate) Ready(siteID string) error {
	status := g.store.SiteAccessStatus(siteID, g.now())
	if status.Pending || status.Problem != "" || !status.Initialized || !status.Enabled {
		return errors.New("请先生成本站访问码并确认授权存储状态")
	}
	return nil
}
func SourceIP(remote string) (string, error) {
	h, _, err := net.SplitHostPort(remote)
	if err != nil {
		return "", err
	}
	ip, err := netip.ParseAddr(h)
	if err != nil || ip.Zone() != "" {
		return "", errors.New("来源 IP 无效")
	}
	return ip.Unmap().String(), nil
}
func (g *Gate) Check(siteID, ip string) (store.AccessGrant, bool) {
	return g.store.CheckAccessIP(siteID, ip, g.now())
}
func (g *Gate) Touch(siteID, ip, id string) { g.store.TouchAccessIP(siteID, ip, id, g.now()) }
func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (g *Gate) take(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for key, item := range g.attempts {
		if !now.Before(item.Reset) {
			delete(g.attempts, key)
		}
	}
	item, exists := g.attempts[ip]
	if !exists {
		if len(g.attempts) >= 4096 {
			return false
		}
		item.Reset = now.Add(time.Minute)
	}
	item.Count++
	g.attempts[ip] = item
	return item.Count <= 5
}
func (g *Gate) newForm(siteID, ip, origin, path, grantID string) (string, formSession, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for key, item := range g.forms {
		if !now.Before(item.Expires) {
			delete(g.forms, key)
		}
	}
	if len(g.forms) >= 4096 {
		return "", formSession{}, errors.New("登录请求过多，请稍后重试")
	}
	token := store.Token()
	value := formSession{SiteID: siteID, IP: ip, Origin: origin, Path: path, CSRF: store.Token(), GrantID: grantID, Expires: now.Add(10 * time.Minute)}
	g.forms[digest(token)] = value
	return token, value, nil
}
func validAccessCode(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func cookieName(siteID string) string { return "v2way_access_form_" + digest(siteID)[:16] }
func formCookie(w http.ResponseWriter, token, path, siteID string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName(siteID), Value: token, Path: path, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 600})
}

type view struct {
	Path, IP, CSRF, Error, Expires, SiteName string
	Authorized, CanRevoke                    bool
}

func render(w http.ResponseWriter, status int, v view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, v)
}

// loginOrigin uses the browser-facing authority, not the container listener port.
// The gateway must first match the request Host and TLS SNI to the current site.
// Forwarded headers are intentionally ignored.
func loginOrigin(r *http.Request) (string, error) {
	if r.TLS == nil {
		return "", errors.New("登录需要 HTTPS")
	}
	u, err := url.Parse("https://" + strings.ToLower(r.Host))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("登录地址无效")
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("登录端口无效")
		}
		port = strconv.Itoa(value)
	}
	authority := u.Hostname()
	if port != "" && port != "443" {
		authority = net.JoinHostPort(authority, port)
	} else if strings.Contains(authority, ":") {
		authority = "[" + authority + "]"
	}
	return "https://" + authority, nil
}

// Handle is called only for the matched login route on the current HTTPS site.
func (g *Gate) Handle(w http.ResponseWriter, r *http.Request, c Entry) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	ip, err := SourceIP(r.RemoteAddr)
	if err != nil {
		http.Error(w, "来源 IP 无效", 400)
		return
	}
	origin, err := loginOrigin(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if (r.Method == "GET" || r.Method == "HEAD") && strings.HasPrefix(r.URL.Path, c.LoginPath+"/fonts/") {
		data, contentType, err := web.LoginFont(strings.TrimPrefix(r.URL.Path, c.LoginPath+"/fonts/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(200)
		if r.Method != "HEAD" {
			_, _ = w.Write(data)
		}
		return
	}
	if r.Method == "GET" && r.URL.Path == c.LoginPath {
		grant, authorized := g.Check(c.SiteID, ip)
		// Reuse an authenticated form only on this exact source/entry.
		if cookie, err := r.Cookie(cookieName(c.SiteID)); err == nil {
			g.mu.Lock()
			form, ok := g.forms[digest(cookie.Value)]
			g.mu.Unlock()
			if ok && form.SiteID == c.SiteID && form.IP == ip && form.Origin == origin && form.Path == c.LoginPath && g.now().Before(form.Expires) {
				expires := ""
				if authorized {
					expires = grant.ExpiresAt.Format(time.RFC3339)
				}
				render(w, 200, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Authorized: authorized, CanRevoke: authorized && form.GrantID == grant.ID, Expires: expires})
				return
			}
		}
		if !g.take("form:" + ip) {
			http.Error(w, "请求过多，请稍后重试", 429)
			return
		}
		token, form, err := g.newForm(c.SiteID, ip, origin, c.LoginPath, "")
		if err != nil {
			http.Error(w, "登录请求过多，请稍后重试", 429)
			return
		}
		formCookie(w, token, c.LoginPath, c.SiteID)
		expires := ""
		if authorized {
			expires = grant.ExpiresAt.Format(time.RFC3339)
		}
		render(w, 200, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Authorized: authorized, Expires: expires})
		return
	}
	if r.Method != "POST" || (r.URL.Path != c.LoginPath+"/login" && r.URL.Path != c.LoginPath+"/logout") {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Origin") != origin {
		http.Error(w, "请求来源校验失败", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	cookie, err := r.Cookie(cookieName(c.SiteID))
	if err != nil {
		http.Error(w, "登录页面已过期，请重新打开", 403)
		return
	}
	g.mu.Lock()
	form, ok := g.forms[digest(cookie.Value)]
	g.mu.Unlock()
	if !ok || form.SiteID != c.SiteID || form.IP != ip || form.Origin != origin || form.Path != c.LoginPath || !g.now().Before(form.Expires) || subtle.ConstantTimeCompare([]byte(form.CSRF), []byte(r.PostForm.Get("csrf"))) != 1 {
		http.Error(w, "登录页面已过期或表单校验失败", 403)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/logout") {
		grant, allowed := g.Check(c.SiteID, ip)
		if !allowed || form.GrantID == "" || form.GrantID != grant.ID {
			http.Error(w, "请重新验证访问码后撤销", 403)
			return
		}
		if err := g.store.RevokeAccess(grant.ID, -1, ip); err != nil {
			http.Error(w, "撤销未确认，请稍后重试", 503)
			return
		}
		g.mu.Lock()
		delete(g.forms, digest(cookie.Value))
		g.mu.Unlock()
		http.Redirect(w, r, c.LoginPath, 303)
		return
	}
	if !g.take("login:" + ip) {
		w.Header().Set("Retry-After", "60")
		render(w, 429, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Error: "登录尝试过多，请稍后重试"})
		return
	}
	select {
	case g.verifySlot <- struct{}{}:
		defer func() { <-g.verifySlot }()
	default:
		render(w, 429, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Error: "登录繁忙，请稍后重试"})
		return
	}
	codes := r.PostForm["code"]
	if len(codes) != 1 || !validAccessCode(codes[0]) {
		render(w, 400, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Error: "请输入 8 位数字访问码"})
		return
	}
	account, accountErr := g.store.AccessAccount(c.SiteID)
	hash := g.dummyHash
	if accountErr == nil {
		hash = account.PasswordHash
	}
	valid := password.Verify(codes[0], hash)
	if accountErr != nil || !valid || !g.store.SiteAccessStatus(c.SiteID, g.now()).Enabled {
		render(w, 401, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Error: "访问码不正确"})
		return
	}
	grant, err := g.store.AuthorizeIP(c.SiteID, ip, account.Revision, g.now())
	if err != nil {
		render(w, 503, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: form.CSRF, Error: "授权未确认，请检查授权存储或稍后重试"})
		return
	}
	token, next, err := g.newForm(c.SiteID, ip, origin, c.LoginPath, grant.ID)
	if err != nil {
		http.Redirect(w, r, c.LoginPath, 303)
		return
	}
	g.mu.Lock()
	delete(g.forms, digest(cookie.Value))
	g.mu.Unlock()
	formCookie(w, token, c.LoginPath, c.SiteID)
	render(w, 200, view{SiteName: c.SiteName, Path: c.LoginPath, IP: ip, CSRF: next.CSRF, Authorized: true, CanRevoke: true, Expires: grant.ExpiresAt.Format(time.RFC3339)})
}
