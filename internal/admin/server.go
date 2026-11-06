package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"v2way/internal/certificate"
	"v2way/internal/config"
	"v2way/internal/gateway"
	"v2way/internal/jsonfile"
	"v2way/internal/store"
)

type Options struct {
	Origins      []string
	StaticRoots  []string
	Listeners    *gateway.Listeners
	AdminAddress string
	UI           http.Handler
}
type Server struct {
	store      *store.Store
	engine     *gateway.Engine
	certs      *certificate.Manager
	options    Options
	mu         sync.Mutex
	limiter    limiter
	started    time.Time
	loginSlots chan struct{}
	sessions   sessionTable
}

func New(s *store.Store, e *gateway.Engine, c *certificate.Manager, o Options) *Server {
	return &Server{store: s, engine: e, certs: c, options: o, started: time.Now(), loginSlots: make(chan struct{}, 16)}
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, (16<<20)+4096)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(r.Body)
	if err != nil || jsonfile.DecodeLimit(raw, v, int(limit)) != nil {
		fail(w, 400, "请求 JSON 无效、字段重复/未知、嵌套过深或过大")
		return false
	}
	return true
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	fail(w, 405, "请求方法不被允许")
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	if !s.hostAllowed(r) {
		fail(w, 421, "管理入口 Host 不被允许，请配置明确的 -origin")
		return
	}
	if r.URL.Path == "/api/admin/v1/session" {
		s.session(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/") {
		if s.options.UI != nil {
			s.options.UI.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	auth, err := s.authenticate(r)
	if err != nil {
		fail(w, 401, "请先登录")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if !s.originAllowed(r) || subtle.ConstantTimeCompare([]byte(auth.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			fail(w, 403, "请求来源或 CSRF 校验失败")
			return
		}
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/admin/v1")
	if r.Method != "GET" && r.Method != "HEAD" && p != "/storage/confirm" && s.store.Status().Pending {
		storageFailure(w, store.ErrPending)
		return
	}
	switch {
	case strings.HasPrefix(p, "/access-control"):
		s.accessControl(w, r, p)
	case p == "/draft/import" && r.Method == "POST":
		s.importConfig(w, r)
	case p == "/draft/reset" && r.Method == "POST":
		s.mu.Lock()
		d, err := s.store.ResetDraft()
		s.mu.Unlock()
		result(w, d, err)
	case p == "/storage/confirm" && r.Method == "POST":
		s.confirmStorage(w, r)
	case p == "/draft":
		s.draft(w, r)
	case p == "/draft/validate" && r.Method == "POST":
		var in struct {
			Config config.Config `json:"config"`
		}
		if !decode(w, r, &in) {
			return
		}
		issues := config.Validate(in.Config)
		if len(issues) == 0 {
			if err := s.accessReady(in.Config); err != nil {
				issues = append(issues, config.Issue{Field: "accessGate", Message: err.Error()})
			}
		}
		if len(issues) == 0 {
			snap, err := s.engine.Compile(in.Config)
			if err != nil {
				issues = append(issues, config.Issue{Field: "resources", Message: err.Error()})
			} else {
				snap.Close()
			}
		}
		respond(w, 200, map[string]any{"issues": issues, "valid": len(issues) == 0})
	case p == "/releases" && r.Method == "POST":
		s.publish(w, r)
	case p == "/versions" && r.Method == "GET":
		v, err := s.store.Versions()
		result(w, v, err)
	case strings.HasPrefix(p, "/versions/") && r.Method == "GET":
		id, err := strconv.ParseInt(strings.TrimPrefix(p, "/versions/"), 10, 64)
		if err != nil {
			fail(w, 400, "版本号无效")
			return
		}
		v, err := s.store.Version(id)
		if errors.Is(err, store.ErrNotFound) {
			fail(w, 404, "版本不存在")
			return
		}
		result(w, v, err)
	case strings.HasPrefix(p, "/releases/") && r.Method == "GET":
		v, err := s.store.ByRequest(strings.TrimPrefix(p, "/releases/"))
		if errors.Is(err, store.ErrNotFound) {
			fail(w, 404, "发布记录不存在或尚未完成")
			return
		}
		result(w, v, err)
	case p == "/overview" && r.Method == "GET":
		stats := s.engine.Stats()
		stats["uptimeSeconds"] = int(time.Since(s.started).Seconds())
		stats["health"] = s.engine.Health()
		stats["upstreamStats"] = s.engine.UpstreamStats()
		stats["listeners"] = []gateway.ListenerInfo{}
		if s.options.Listeners != nil {
			stats["listeners"] = s.options.Listeners.Info()
		}
		stats["adminAddress"] = s.options.AdminAddress
		stats["staticRoots"] = s.options.StaticRoots
		stats["storage"] = s.store.Status()
		stats["capabilities"] = map[string]bool{"manualTls": true, "acme": true, "http01": true, "dns01": true, "encryptedBackup": false, "jsonStorage": true, "configImport": true}
		respond(w, 200, stats)
	case p == "/logs" && r.Method == "GET":
		respond(w, 200, s.engine.Logs())
	case p == "/audit" && r.Method == "GET":
		v, err := s.store.Audits()
		result(w, v, err)
	case p == "/route-tests" && r.Method == "POST":
		s.routeTest(w, r)
	case p == "/certificates":
		s.certificates(w, r)
	case p == "/acme/providers" && r.Method == "GET":
		respond(w, 200, certificate.DNSProviders())
	case p == "/acme/jobs" && r.Method == "GET":
		respond(w, 200, s.certs.Automation().Jobs())
	case p == "/acme/issue" && r.Method == "POST":
		var in certificate.IssueRequest
		if !decode(w, r, &in) {
			return
		}
		job, err := s.certs.Automation().Issue(in)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		respond(w, 202, job)
	case strings.HasPrefix(p, "/certificates/") && strings.HasSuffix(p, "/renew") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/certificates/"), "/renew")
		job, err := s.certs.Automation().Renew(id)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		respond(w, 202, job)
	case p == "/export" && r.Method == "GET":
		v, err := s.store.Active()
		if err != nil {
			fail(w, 500, "导出失败")
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="v2way-config.json"`)
		respond(w, 200, v.Config)
	case p == "/password" && r.Method == "POST":
		s.password(w, r)
	default:
		fail(w, 404, "接口或请求方法不存在")
	}
}
func result(w http.ResponseWriter, v any, err error) {
	if err != nil {
		storageFailure(w, err)
		return
	}
	respond(w, 200, v)
}
func (s *Server) draft(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		d, err := s.store.Draft()
		result(w, d, err)
	case "PUT":
		var in store.Draft
		if !decode(w, r, &in) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		d, err := s.store.SaveDraft(in.Config, in.Revision, requestSourceIP(r))
		if errors.Is(err, store.ErrConflict) {
			fail(w, 409, err.Error())
			return
		}
		result(w, d, err)
	default:
		methodNotAllowed(w, "GET, PUT")
	}
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision              int64  `json:"revision"`
		ExpectedActiveVersion *int64 `json:"expectedActiveVersion"`
		RequestID             string `json:"requestId"`
		Note                  string `json:"note"`
		RollbackVersion       int64  `json:"rollbackVersion"`
	}
	if !decode(w, r, &request) {
		return
	}
	if request.ExpectedActiveVersion == nil || len(request.RequestID) < 8 || len(request.RequestID) > 128 || len(request.Note) > 512 {
		fail(w, 400, "发布需要有效请求标识、运行版本和备注")
		return
	}
	account, err := s.store.Account()
	if err != nil {
		storageFailure(w, err)
		return
	}
	in := store.PublishInput{Revision: request.Revision, ExpectedActiveVersion: *request.ExpectedActiveVersion, RequestID: request.RequestID, Note: request.Note, RollbackVersion: request.RollbackVersion, Actor: account.Username, SourceIP: requestSourceIP(r)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, replay, err := s.store.CheckReplay(in); err != nil {
		storageFailure(w, err)
		return
	} else if replay {
		respond(w, 200, previous)
		return
	}
	draft, err := s.store.Draft()
	if err != nil {
		storageFailure(w, err)
		return
	}
	if draft.Revision != in.Revision {
		storageFailure(w, store.ErrConflict)
		return
	}
	c := draft.Config
	if in.RollbackVersion > 0 {
		version, err := s.store.Version(in.RollbackVersion)
		if err != nil {
			storageFailure(w, err)
			return
		}
		c = version.Config
	}
	if issues := config.Validate(c); len(issues) > 0 {
		respond(w, 422, map[string]any{"error": "配置校验失败", "issues": issues})
		return
	}
	if err := s.accessReady(c); err != nil {
		fail(w, 422, err.Error())
		return
	}
	snap, err := s.engine.Compile(c)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	var plan *gateway.ListenerPlan
	if s.options.Listeners != nil {
		plan, err = s.options.Listeners.Prepare(snap)
		if err != nil {
			snap.Close()
			fail(w, 422, err.Error())
			return
		}
		defer plan.Abort()
	}
	version, err := s.store.Commit(c, in)
	if err != nil {
		snap.Close()
		storageFailure(w, err)
		return
	}
	// gateway.json is durable before the only runtime activation point.
	snap.Version = version.ID
	if plan != nil {
		plan.Activate(snap)
	} else {
		s.engine.Activate(snap)
	}
	respond(w, 200, version)
}

func (s *Server) routeTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host   string `json:"host"`
		Path   string `json:"path"`
		Method string `json:"method"`
		Draft  bool   `json:"draft"`
		Port   int    `json:"port"`
		HTTPS  bool   `json:"https"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Port < 1 || in.Port > 65535 {
		fail(w, 422, "请填写 1–65535 的监听端口")
		return
	}
	snap := s.engine.Current()
	if in.Draft {
		d, err := s.store.Draft()
		if err != nil {
			fail(w, 500, "读取草稿失败")
			return
		}
		snap, err = s.engine.Compile(d.Config)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		defer snap.Close()
	}
	site, route := snap.MatchAt(in.Host, in.Path, in.Method, in.Port, in.HTTPS)
	respond(w, 200, map[string]any{"site": site, "route": route, "version": snap.Version, "sentRequest": false})
}
func (s *Server) certificates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		v, err := s.certs.List()
		result(w, v, err)
	case "POST":
		var in struct {
			Name        string `json:"name"`
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
		}
		if !decode(w, r, &in) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		v, err := s.certs.Upload(in.Name, in.Certificate, in.PrivateKey)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		respond(w, 201, v)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}
func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string  `json:"current"`
		Password string  `json:"password"`
		Username *string `json:"username"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !s.limiter.take(r.RemoteAddr) {
		fail(w, 429, "尝试过多，请稍后重试")
		return
	}
	s.limiter.login.Lock()
	defer s.limiter.login.Unlock()
	account, err := s.store.Account()
	if err != nil || !verify(in.Current, account.PasswordHash) {
		fail(w, 403, "当前密码不正确")
		return
	}
	username := account.Username
	if in.Username != nil {
		username = strings.TrimSpace(*in.Username)
		if username == "" || len(username) > 64 {
			fail(w, 422, "管理员名称应为 1–64 字节")
			return
		}
	}
	hashed := account.PasswordHash
	if in.Password != "" {
		if in.Password == in.Current {
			fail(w, 422, "新密码不能与当前密码相同")
			return
		}
		hashed, err = HashPassword(in.Password)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
	} else if username == account.Username {
		fail(w, 422, "请修改用户名或填写新密码")
		return
	}
	if err = s.store.UpdateAccount(account.Revision, username, hashed, requestSourceIP(r)); err != nil {
		storageFailure(w, err)
		return
	}
	s.sessions.clear()
	respond(w, 200, map[string]bool{"ok": true})
}
