package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"v2way/internal/accessgate"
	"v2way/internal/certificate"
	"v2way/internal/config"
	"v2way/internal/headertemplate"
	"v2way/internal/store"
)

type Entry struct {
	ID         string    `json:"id"`
	IP         string    `json:"ip"`
	Time       time.Time `json:"time"`
	Site       string    `json:"site"`
	Route      string    `json:"route"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"durationMs"`
	Bytes      int64     `json:"bytes"`
	Version    int64     `json:"version"`
}
type TargetState struct {
	URL      string `json:"url"`
	Healthy  bool   `json:"healthy"`
	Failures int    `json:"failures"`
}
type target struct {
	url      *url.URL
	weight   int
	failures atomic.Int32
	healthy  atomic.Bool
}
type transportTimeouts struct{ connect, handshake, header time.Duration }

func routeTimeouts(r config.Route) transportTimeouts {
	effective := func(value, fallback int) time.Duration {
		if value == 0 {
			value = fallback
		}
		return time.Duration(value) * time.Second
	}
	return transportTimeouts{effective(r.ConnectTimeoutSeconds, 5), effective(r.TLSHandshakeTimeoutSeconds, 5), effective(r.ResponseHeaderTimeoutSeconds, 30)}
}

type pool struct {
	counters         *upstreamCounters
	targets          []*target
	next             atomic.Uint64
	transport        *http.Transport
	customTransports map[transportTimeouts]*http.Transport
	healthPath       string
}
type site struct {
	config.Site
	headerTemplates map[string]routeHeaderTemplates
	routes          []config.Route
	cert            *tls.Certificate
	mu              sync.Mutex
	tokens          float64
	last            time.Time
}
type Snapshot struct {
	Version        int64
	sites          []*site
	pools          map[string]*pool
	cancel         context.CancelFunc
	legacyAccess   bool
	hasLoginRoutes bool
}
type Engine struct {
	current     atomic.Pointer[Snapshot]
	roots       []string
	rootHandles map[string]*os.Root
	certs       *certificate.Manager
	requests    atomic.Uint64
	failures    atomic.Uint64
	active      atomic.Int64
	mu          sync.Mutex
	logs        []Entry
	conns       map[net.Conn]struct{}
	Access      *accessgate.Gate
}

func New(roots []string, certs *certificate.Manager) *Engine {
	e := &Engine{roots: roots, rootHandles: map[string]*os.Root{}, certs: certs, conns: map[net.Conn]struct{}{}}
	for _, name := range roots {
		if handle, err := os.OpenRoot(name); err == nil {
			e.rootHandles[name] = handle
		}
	}
	e.current.Store(&Snapshot{pools: map[string]*pool{}})
	return e
}
func (e *Engine) Current() *Snapshot { return e.current.Load() }

// ServesHost only recognizes enabled domains from the published snapshot.
func (e *Engine) ServesHost(raw string) bool { return e.current.Load().find(host(raw)) != nil }
func (s *Snapshot) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	for _, p := range s.pools {
		p.transport.CloseIdleConnections()
		for _, transport := range p.customTransports {
			transport.CloseIdleConnections()
		}
	}
}
func (e *Engine) Swap(s *Snapshot) *Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.current.Load()
	for id, pool := range s.pools {
		if previous := old.pools[id]; previous != nil {
			pool.counters = previous.counters
		}
	}
	return e.current.Swap(s)
}
func (e *Engine) Activate(s *Snapshot) { old := e.Swap(s); e.StartHealth(s); old.Close() }
func (e *Engine) Compile(c config.Config) (*Snapshot, error) {
	if issues := config.ValidateStored(c); len(issues) > 0 {
		return nil, fmt.Errorf("%s：%s", issues[0].Field, issues[0].Message)
	}
	s := &Snapshot{pools: map[string]*pool{}}
	s.legacyAccess = c.AccessGate != nil
	fail := func(err error) (*Snapshot, error) { s.Close(); return nil, err }
	for _, u := range c.Upstreams {
		p := &pool{counters: &upstreamCounters{}, transport: newTransport(), customTransports: map[transportTimeouts]*http.Transport{}, healthPath: u.HealthPath}
		p.transport.TLSClientConfig.InsecureSkipVerify = u.SkipTLSVerify
		for _, t := range u.Targets {
			v, _ := url.Parse(t.URL)
			tt := &target{url: v, weight: t.Weight}
			tt.healthy.Store(true)
			p.targets = append(p.targets, tt)
		}
		s.pools[u.ID] = p
	}
	for _, v := range c.Sites {
		item := &site{Site: v, routes: append([]config.Route{}, v.Routes...), tokens: float64(v.RatePerSecond), last: time.Now()}
		item.headerTemplates = map[string]routeHeaderTemplates{}
		item.Domains = append([]string{}, v.Domains...)
		for i, d := range item.Domains {
			item.Domains[i], _ = config.Domain(d)
		}
		config.SortRoutes(item.routes)
		for _, r := range item.routes {
			if r.HeaderVariables {
				request, err := headertemplate.CompileHeaders(r.RequestHeaders)
				if err != nil {
					return fail(err)
				}
				response, err := headertemplate.CompileHeaders(r.ResponseHeaders)
				if err != nil {
					return fail(err)
				}
				item.headerTemplates[r.ID] = routeHeaderTemplates{request, response}
			}
			if r.Action == config.LoginAction {
				s.hasLoginRoutes = true
			}
			if r.Action == "proxy" {
				policy := routeTimeouts(r)
				if policy != routeTimeouts(config.Route{}) {
					p := s.pools[r.UpstreamID]
					if p.customTransports[policy] == nil {
						transport := newTransportWithTimeouts(policy)
						transport.TLSClientConfig = p.transport.TLSClientConfig.Clone()
						p.customTransports[policy] = transport
					}
				}
			}
			if r.Action == "static" {
				if err := e.validateRoot(r.Root); err != nil {
					return fail(err)
				}
			}
		}
		if v.HTTPS {
			if e.certs == nil {
				return fail(errors.New("未启用证书存储"))
			}
			cert, err := e.certs.Load(v.CertificateID)
			if err != nil {
				return fail(err)
			}
			if time.Now().Before(cert.Leaf.NotBefore) || !time.Now().Before(cert.Leaf.NotAfter) {
				return fail(errors.New("证书已过期或尚未生效"))
			}
			for _, d := range item.Domains {
				if strings.HasPrefix(d, "*.") {
					d = "test." + d[2:]
					if !contains(cert.Leaf.DNSNames, "*."+strings.TrimPrefix(d, "test.")) {
						return fail(errors.New("通配站点需要覆盖该通配域名的证书"))
					}
				}
				if err = cert.Leaf.VerifyHostname(d); err != nil {
					return fail(errors.New("证书不覆盖站点域名：" + d))
				}
			}
			item.cert = cert
		}
		s.sites = append(s.sites, item)
	}
	return s, nil
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func (e *Engine) validateRoot(root string) error {
	handle, err := e.openRoot(root)
	if err != nil {
		return err
	}
	return handle.Close()
}
func (e *Engine) openRoot(root string) (*os.Root, error) {
	for name, parent := range e.rootHandles {
		rel, err := filepath.Rel(name, root)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		handle, err := parent.OpenRoot(rel)
		if err != nil {
			return nil, errors.New("静态目录不可访问或越出批准范围")
		}
		return handle, nil
	}
	return nil, errors.New("静态目录不在启动参数批准的根目录内")
}
func host(raw string) string {
	if h, _, err := net.SplitHostPort(raw); err == nil {
		raw = h
	}
	d, err := config.Domain(strings.Trim(raw, "[]"))
	if err != nil || strings.HasPrefix(d, "*.") {
		return ""
	}
	return d
}
func (s *Snapshot) find(h string) *site { return s.findAt(h, 0, false) }
func (s *Snapshot) findAt(h string, port int, secure bool) *site {
	var wildcard *site
	for _, v := range s.sites {
		if !v.Enabled || port > 0 && ((!secure && v.HTTPPort != port) || (secure && (!v.HTTPS || v.HTTPSPort != port))) {
			continue
		}
		for _, d := range v.Domains {
			if d == h {
				return v
			}
			if config.MatchDomain(d, h) {
				wildcard = v
			}
		}
	}
	return wildcard
}
func (s *Snapshot) Match(h, p, m string) (*config.Site, *config.Route) {
	return s.MatchAt(h, p, m, 0, false)
}
func (s *Snapshot) MatchAt(h, p, m string, port int, secure bool) (*config.Site, *config.Route) {
	v := s.findAt(host(h), port, secure)
	if v == nil {
		return nil, nil
	}
	if v.Maintenance {
		return &v.Site, nil
	}
	for _, r := range v.routes {
		if r.Action == config.LoginAction && (p == r.Path || strings.HasPrefix(p, r.Path+"/")) {
			if secure && (m == "GET" || m == "POST" || m == "HEAD") {
				return &v.Site, &r
			}
			return &v.Site, nil
		}
	}
	for _, r := range v.routes {
		if config.MatchPath(r, p) && config.MatchMethod(r, m) {
			return &v.Site, &r
		}
	}
	return &v.Site, nil
}

// TLSConfig rejects unknown, absent, disabled and cross-port SNI before any certificate is sent.
func (e *Engine) TLSConfig(port int) *tls.Config {
	get := func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) { return e.certificateForPort(hello, port) }
	return &tls.Config{MinVersion: tls.VersionTLS12, SessionTicketsDisabled: true, GetCertificate: get,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { _, err := get(hello); return nil, err }}
}
func (e *Engine) certificateForPort(hello *tls.ClientHelloInfo, port int) (*tls.Certificate, error) {
	if hello.ServerName == "" {
		return nil, errors.New("拒绝未提供 SNI 的 TLS 握手")
	}
	v := e.current.Load().findAt(host(hello.ServerName), port, true)
	if v == nil || !v.HTTPS || v.cert == nil {
		return nil, errors.New("拒绝未匹配站点的 TLS 握手")
	}
	cert := v.cert
	if e.certs != nil {
		if latest, err := e.certs.Load(v.CertificateID); err == nil {
			cert = latest
		}
	}
	if time.Now().Before(cert.Leaf.NotBefore) || !time.Now().Before(cert.Leaf.NotAfter) {
		return nil, errors.New("证书不在有效期内，拒绝 TLS 握手")
	}
	if err := cert.Leaf.VerifyHostname(hello.ServerName); err != nil {
		return nil, errors.New("证书不匹配，拒绝 TLS 握手")
	}
	return cert, nil
}
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil && e.certs != nil && e.certs.HTTPChallenge(w, r) {
		return
	}
	start := time.Now()
	snap := e.current.Load()
	accessSubmission := strings.Contains(r.URL.RawQuery, "_v2way_access") && r.URL.Query().Has("_v2way_access")
	sourceIP, _ := accessgate.SourceIP(r.RemoteAddr)
	entry := Entry{ID: store.Token()[:24], Time: start, IP: sourceIP, Method: r.Method, Path: r.URL.Path, Version: snap.Version}
	if strings.HasPrefix(r.URL.Path, "/__access") {
		entry.Path = "[访问授权入口]"
	}
	if r.URL.Query().Has("_v2way_access") {
		entry.Path = "[本站 IP 授权登录页]"
	}
	for _, site := range snap.sites {
		for _, login := range site.routes {
			if login.Action == config.LoginAction && (r.URL.Path == login.Path || strings.HasPrefix(r.URL.Path, login.Path+"/")) {
				entry.Path = "[本站 IP 授权登录页]"
			}
		}
	}

	rw := &recorder{ResponseWriter: w, status: 200, engine: e}
	w = rw
	w.Header().Set("X-Request-ID", entry.ID)
	e.requests.Add(1)
	active := e.active.Add(1)
	defer e.active.Add(-1)
	defer func() {
		entry.Status = rw.status
		entry.Bytes = rw.bytes.Load()
		entry.DurationMs = time.Since(start).Milliseconds()
		e.record(entry)
	}()
	if active > 10000 {
		http.Error(w, "连接数已达上限", 503)
		return
	}
	lower := strings.ToLower(r.URL.EscapedPath())
	if strings.ContainsAny(r.URL.Path, "\\\x00") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%2e") || strings.Contains(lower, "%25") || path.Clean(r.URL.Path) != strings.TrimSuffix(r.URL.Path, "/") && r.URL.Path != "/" {
		http.Error(w, "请求路径无效", 400)
		return
	}
	binding, _ := r.Context().Value(listenerContextKey{}).(listenerIdentity)
	s := snap.findAt(host(r.Host), binding.Port, r.TLS != nil)
	if s == nil {
		serveStatusPage(w, r, http.StatusNotFound)
		return
	}
	entry.Site = s.Name
	rw.private = s.RequireIPLogin
	if r.TLS != nil {
		sni := snap.findAt(host(r.TLS.ServerName), binding.Port, true)
		if !s.HTTPS || sni != s {
			http.Error(w, "域名与 TLS 站点不匹配", 421)
			return
		}
	}
	if !allowed(s, r.RemoteAddr) {
		http.Error(w, "访问被拒绝", 403)
		return
	}
	if s.Maintenance {
		serveStatusPage(w, r, http.StatusServiceUnavailable)
		return
	}
	if !s.take() {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "请求过于频繁", 429)
		return
	}
	ip, ipErr := accessgate.SourceIP(r.RemoteAddr)
	for _, login := range s.routes {
		if login.Action != config.LoginAction || !(r.URL.Path == login.Path || strings.HasPrefix(r.URL.Path, login.Path+"/")) {
			continue
		}
		entry.Path = "[本站 IP 授权登录页]"
		if r.TLS == nil || e.Access == nil {
			serveStatusPage(w, r, 404)
			return
		}
		e.Access.Handle(w, r, accessgate.Entry{SiteID: s.ID, SiteName: s.Name, LoginPath: login.Path})
		return
	}
	// Retired forms must not submit access credentials to an application fallback.
	if accessSubmission || ((snap.legacyAccess || snap.hasLoginRoutes) && strings.HasPrefix(r.URL.Path, "/__access/")) {
		serveStatusPage(w, r, 404)
		return
	}
	var grant store.AccessGrant
	if s.RequireIPLogin {
		allowed := false
		if ipErr == nil && e.Access != nil {
			grant, allowed = e.Access.Check(s.ID, ip)
		}
		if !allowed {
			serveStatusPage(w, r, 404)
			return
		}
	}
	if s.RedirectHTTPS && r.TLS == nil {
		h := host(r.Host)
		if strings.Contains(h, ":") {
			h = "[" + h + "]"
		}
		if s.HTTPSPort != 443 {
			h = net.JoinHostPort(host(r.Host), strconv.Itoa(s.HTTPSPort))
		}
		http.Redirect(w, r, "https://"+h+r.URL.RequestURI(), 308)
		return
	}
	var route *config.Route
	for i := range s.routes {
		candidate := &s.routes[i]
		if config.MatchPath(*candidate, r.URL.Path) && config.MatchMethod(*candidate, r.Method) {
			route = candidate
			break
		}
	}
	if route == nil {
		serveStatusPage(w, r, http.StatusNotFound)
		return
	}
	if s.RequireIPLogin {
		name, port, secure, remote := host(r.Host), binding.Port, r.TLS != nil, r.RemoteAddr
		rw.renew = func() {
			current := e.current.Load()
			site := current.findAt(name, port, secure)
			if site != nil && site.ID == s.ID && site.RequireIPLogin && !site.Maintenance && allowed(site, remote) {
				e.Access.Touch(s.ID, ip, grant.ID)
			}
		}
	}
	entry.Route = route.Name
	if entry.Route == "" {
		entry.Route = route.Path
	}
	requestHeaders, responseHeaders := route.RequestHeaders, route.ResponseHeaders
	if route.HeaderVariables {
		context := headerContext(r, entry.ID, s.ID, route.ID)
		templates := s.headerTemplates[route.ID]
		var err error
		requestHeaders, err = templates.request.Expand(context)
		if err == nil {
			responseHeaders, err = templates.response.Expand(context)
		}
		if err != nil {
			http.Error(w, "请求信息无法用于 Header 变量", http.StatusBadRequest)
			return
		}
	}
	for k, v := range responseHeaders {
		w.Header().Set(k, v)
	}
	if route.HeaderVariables && !s.ForwardAdminCookie {
		stripAdminResponseCookie(w.Header())
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.MaxBodyMiB)<<20)
	if r.ContentLength > int64(s.MaxBodyMiB)<<20 {
		http.Error(w, "请求体超过限制", 413)
		return
	}
	if !route.Streaming && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		budget := route.TimeoutSeconds
		if budget == 0 {
			budget = 60
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(budget)*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
	}
	switch route.Action {
	case "response":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(route.Status)
		io.WriteString(w, route.Body)
	case "redirect":
		http.Redirect(w, r, route.Location, route.Status)
	case "static":
		e.serveStatic(w, r, *route)
	case "proxy":
		p := snap.pools[route.UpstreamID]
		p.counters.requests.Add(1)
		t := p.pick()
		if t == nil {
			http.Error(w, "没有可用后端", 503)
			return
		}
		transport := p.transport
		if custom := p.customTransports[routeTimeouts(*route)]; custom != nil {
			transport = custom
		}
		proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0), Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(t.url)
			if pr.Out.Body != nil && pr.Out.Body != http.NoBody {
				pr.Out.Body = &meteredBody{pr.Out.Body, &p.counters.upload}
			}
			if route.PreserveHost {
				pr.Out.Host = pr.In.Host
			}
			pr.Out.Header.Del("X-Real-IP")
			pr.SetXForwarded()
			ip, _, _ := net.SplitHostPort(pr.In.RemoteAddr)
			pr.Out.Header.Set("X-Real-IP", ip)
			pr.Out.Header.Set("X-Request-ID", entry.ID)
			if route.StripPrefix && route.Path != "/" {
				pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, route.Path)
				if pr.Out.URL.Path == "" {
					pr.Out.URL.Path = "/"
				}
				if !strings.HasPrefix(pr.Out.URL.Path, "/") {
					pr.Out.URL.Path = "/" + pr.Out.URL.Path
				}
				pr.Out.URL.RawPath = ""
			}
			for k, v := range requestHeaders {
				pr.Out.Header.Set(k, v)
			}
			if !s.ForwardAdminCookie {
				stripAdminRequestCookie(pr.Out.Header)
			}
		}, ModifyResponse: func(resp *http.Response) error {
			if route.HeaderVariables {
				for k, v := range responseHeaders {
					// Remove the early error-response fallback before ReverseProxy
					// copies upstream headers, so configured values appear once.
					w.Header().Del(k)
					resp.Header.Set(k, v)
				}
			}
			if !s.ForwardAdminCookie {
				stripAdminResponseCookie(resp.Header)
			}
			if s.RequireIPLogin {
				resp.Header.Set("Cache-Control", "private, no-store")
			}
			t.failures.Store(0)
			t.healthy.Store(true)
			if resp.Body != nil && resp.Body != http.NoBody {
				resp.Body = meterResponseBody(resp.Body, p.counters)
			}
			return nil
		}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				if t.failures.Add(1) >= 3 {
					t.healthy.Store(false)
				}
			}
			code := 502
			var bodyErr *http.MaxBytesError
			if errors.As(err, &bodyErr) {
				code = 413
			} else if timeoutError(err) {
				code = 504
			}
			http.Error(w, http.StatusText(code), code)
		}}
		proxy.ServeHTTP(w, r)
	}
}
func (s *site) take() bool {
	if s.RatePerSecond == 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.tokens += now.Sub(s.last).Seconds() * float64(s.RatePerSecond)
	s.last = now
	if s.tokens > float64(s.RatePerSecond) {
		s.tokens = float64(s.RatePerSecond)
	}
	if s.tokens < 1 {
		return false
	}
	s.tokens--
	return true
}
func allowed(s *site, remote string) bool {
	h, _, _ := net.SplitHostPort(remote)
	ip := net.ParseIP(h)
	for _, c := range s.DenyCIDRs {
		_, n, _ := net.ParseCIDR(c)
		if n.Contains(ip) {
			return false
		}
	}
	if len(s.AllowCIDRs) == 0 {
		return true
	}
	for _, c := range s.AllowCIDRs {
		_, n, _ := net.ParseCIDR(c)
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
func (p *pool) pick() *target {
	total := 0
	for _, t := range p.targets {
		if t.healthy.Load() {
			total += t.weight
		}
	}
	if total == 0 {
		return nil
	}
	n := int(p.next.Add(1)-1) % total
	for _, t := range p.targets {
		if !t.healthy.Load() {
			continue
		}
		if n < t.weight {
			return t
		}
		n -= t.weight
	}
	return nil
}
func timeoutError(err error) bool {
	var networkError net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout()
}
func newTransport() *http.Transport { return newTransportWithTimeouts(routeTimeouts(config.Route{})) }
func newTransportWithTimeouts(policy transportTimeouts) *http.Transport {
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return safeDialWithTimeout(ctx, network, address, policy.connect)
	}
	return &http.Transport{Proxy: nil, DialContext: dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: policy.handshake, ResponseHeaderTimeout: policy.header, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 200, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 512, ForceAttemptHTTP2: true, ExpectContinueTimeout: time.Second}
}
func SafeDial(ctx context.Context, network, address string) (net.Conn, error) {
	return safeDialWithTimeout(ctx, network, address, 5*time.Second)
}
func safeDialWithTimeout(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	h, p, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, h)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.IP.IsUnspecified() || ip.IP.IsMulticast() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.Equal(net.ParseIP("100.100.100.200")) || ip.IP.Equal(net.ParseIP("168.63.129.16")) {
			return nil, errors.New("拒绝特殊用途后端地址")
		}
	}
	var last error
	for _, ip := range ips {
		conn, e := (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), p))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	if last == nil {
		last = errors.New("后端无可用地址")
	}
	return nil, last
}
func (e *Engine) serveStatic(w http.ResponseWriter, r *http.Request, route config.Route) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "方法不允许", 405)
		return
	}
	root, err := e.openRoot(route.Root)
	if err != nil {
		http.Error(w, "静态目录不可用", 503)
		return
	}
	defer root.Close()
	name := strings.TrimPrefix(r.URL.Path, "/")
	if route.StripPrefix {
		name = strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, route.Path), "/")
	}
	if name == "" {
		name = "index.html"
	}
	file, err := root.Open(name)
	if err == nil {
		info, e := file.Stat()
		if e != nil || info.IsDir() {
			file.Close()
			file = nil
			err = os.ErrNotExist
		}
	}
	if err != nil && route.SPA && path.Ext(name) == "" {
		file, err = root.Open("index.html")
		name = "index.html"
	}
	if err != nil {
		serveStatusPage(w, r, http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		serveStatusPage(w, r, http.StatusNotFound)
		return
	}
	if w.Header().Get("Cache-Control") == "" {
		if strings.HasSuffix(name, ".html") || path.Base(name) == "sw.js" || strings.HasSuffix(name, ".webmanifest") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	if strings.HasSuffix(name, ".webmanifest") {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
}
func (e *Engine) StartHealth(s *Snapshot) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, p := range s.pools {
		p := p
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					for _, t := range p.targets {
						if p.healthPath == "" && t.healthy.Load() {
							continue
						}
						u := *t.url
						u.Path = p.healthPath
						if u.Path == "" {
							u.Path = "/"
						}
						probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
						req, _ := http.NewRequestWithContext(probeCtx, "GET", u.String(), nil)
						resp, err := p.transport.RoundTrip(req)
						ok := err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400
						if resp != nil {
							io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
							resp.Body.Close()
						}
						cancel()
						if ok {
							t.failures.Store(0)
							t.healthy.Store(true)
						} else if t.failures.Add(1) >= 3 {
							t.healthy.Store(false)
						}
					}
				}
			}
		}()
	}
}
func (e *Engine) record(entry Entry) {
	if entry.Status >= 500 {
		e.failures.Add(1)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.logs) >= 1000 {
		copy(e.logs, e.logs[1:])
		e.logs = e.logs[:999]
	}
	e.logs = append(e.logs, entry)
}
func (e *Engine) Logs() []Entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := append([]Entry{}, e.logs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}
func (e *Engine) Stats() map[string]any {
	return map[string]any{"requests": e.requests.Load(), "errors": e.failures.Load(), "activeConnections": e.active.Load(), "version": e.current.Load().Version}
}
func (e *Engine) Health() map[string][]TargetState {
	out := map[string][]TargetState{}
	for id, p := range e.current.Load().pools {
		for _, t := range p.targets {
			out[id] = append(out[id], TargetState{t.url.String(), t.healthy.Load(), int(t.failures.Load())})
		}
	}
	return out
}
func (e *Engine) Close() {
	e.current.Load().Close()
	e.mu.Lock()
	conns := []net.Conn{}
	for c := range e.conns {
		conns = append(conns, c)
	}
	e.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	for _, root := range e.rootHandles {
		root.Close()
	}
}

type recorder struct {
	http.ResponseWriter
	status  int
	bytes   atomic.Int64
	written bool
	engine  *Engine
	private bool
	renew   func()
}

func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *recorder) WriteHeader(code int) {
	if w.written {
		return
	}
	if code >= 100 && code < 200 && code != 101 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.status = code
	w.written = true
	if w.private {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	if w.renew != nil && code >= 200 && code < 400 {
		w.renew()
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *recorder) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes.Add(int64(n))
	if n > 0 && w.renew != nil && w.status >= 200 && w.status < 400 {
		w.renew()
	}
	return n, err
}
func (w *recorder) Flush() {
	if !w.written {
		w.WriteHeader(200)
	}
	http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.status = 101
	w.written = true
	if w.renew != nil {
		w.renew()
	}
	// The original buffered writer sends the upgrade headers. Subsequent tunnel
	// writes use wrapped, so only post-upgrade downstream bytes enter the log.
	wrapped := &trackedConn{Conn: c, engine: w.engine, renew: w.renew, bytes: &w.bytes}
	w.engine.mu.Lock()
	w.engine.conns[wrapped] = struct{}{}
	w.engine.mu.Unlock()
	return wrapped, rw, nil
}

type trackedConn struct {
	net.Conn
	engine  *Engine
	once    sync.Once
	renew   func()
	bytes   *atomic.Int64
	writeMu sync.Mutex
}

func (c *trackedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.renew != nil {
		c.renew()
	}
	return n, err
}
func (c *trackedConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	n, err := c.Conn.Write(p)
	c.bytes.Add(int64(n))
	if n > 0 && c.renew != nil {
		c.renew()
	}
	return n, err
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	// Closing the socket releases a blocked write. Wait for its byte count before
	// returning to the handler that records the final access-log entry.
	c.writeMu.Lock()
	c.writeMu.Unlock()
	c.once.Do(func() { c.engine.mu.Lock(); delete(c.engine.conns, c); c.engine.mu.Unlock() })
	return err
}
