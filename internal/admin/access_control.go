package admin

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"v2way/internal/store"

	"v2way/internal/config"
)

var siteAccountID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (s *Server) accessReady(c config.Config) error {
	for _, site := range c.Sites {
		if !site.Enabled || site.Maintenance {
			continue
		}
		needed := site.RequireIPLogin
		for _, route := range site.Routes {
			needed = needed || route.Action == config.LoginAction
		}
		if needed {
			status := s.store.SiteAccessStatus(site.ID, time.Now())
			if !status.Initialized || !status.Enabled || status.Pending || status.Problem != "" {
				return fmt.Errorf("请先在站点「%s」配置中生成访问码，并确认授权存储状态", site.Name)
			}
		}
	}
	return nil
}
func (s *Server) accessControl(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/access-control" && r.Method == "GET":
		respond(w, 200, s.store.AccessStatus(time.Now()))
	case strings.HasPrefix(path, "/access-control/sites/") && strings.HasSuffix(path, "/account"):
		siteID := strings.TrimSuffix(strings.TrimPrefix(path, "/access-control/sites/"), "/account")
		if !siteAccountID.MatchString(siteID) {
			fail(w, 400, "站点标识无效")
			return
		}
		if r.Method == "GET" {
			status := s.store.SiteAccessStatus(siteID, time.Now())
			respond(w, 200, map[string]any{"initialized": status.Initialized, "enabled": status.Enabled, "revision": status.Revision, "pending": status.Pending, "problem": status.Problem, "warning": status.Warning})
			return
		}
		if r.Method != "POST" {
			methodNotAllowed(w, "GET, POST")
			return
		}
		var in struct {
			Revision *int64 `json:"revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Revision == nil || *in.Revision < 0 {
			fail(w, 400, "需要当前站点账号修订")
			return
		}
		if !s.limiter.take(r.RemoteAddr) {
			fail(w, 429, "生成过于频繁，请稍后重试")
			return
		}
		s.limiter.login.Lock()
		defer s.limiter.login.Unlock()
		if s.store.SiteAccessStatus(siteID, time.Now()).Revision != *in.Revision {
			storageFailure(w, store.ErrConflict)
			return
		}
		passwordNumber, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
		if err != nil {
			fail(w, 500, "生成访问码失败")
			return
		}
		plain := fmt.Sprintf("%08d", passwordNumber)
		hash, err := HashPassword(plain)
		if err != nil {
			fail(w, 500, "生成访问码失败")
			return
		}
		if err := s.store.UpdateAccessAccountFrom(siteID, *in.Revision, "访问码", hash, time.Now(), requestSourceIP(r)); err != nil {
			storageFailure(w, err)
			return
		}
		respond(w, 200, map[string]any{"code": plain, "revision": s.store.SiteAccessStatus(siteID, time.Now()).Revision})

	case path == "/access-control/grants" && r.Method == "GET":
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if offset < 0 {
			offset = 0
		}
		if limit <= 0 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}
		values, revision := s.store.AccessGrantSnapshot(time.Now())
		query := strings.ToLower(r.URL.Query().Get("q"))
		if len(query) > 128 {
			fail(w, 400, "搜索内容过长")
			return
		}
		filtered := values[:0]
		for _, value := range values {
			if (r.URL.Query().Get("siteId") == "" || r.URL.Query().Get("siteId") == value.SiteID) && strings.Contains(strings.ToLower(value.IP+" "+value.Username+" "+value.SiteID), query) {
				filtered = append(filtered, value)
			}
		}
		total := len(filtered)
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		respond(w, 200, map[string]any{"items": filtered[offset:end], "total": total, "revision": revision})
	case path == "/access-control/grants/clear" && r.Method == "POST":
		var in struct {
			Revision int64 `json:"revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Revision < 1 {
			fail(w, 400, "需要授权集合修订")
			return
		}
		if err := s.store.RevokeAccess("", in.Revision, requestSourceIP(r)); err != nil {
			storageFailure(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	case strings.HasPrefix(path, "/access-control/grants/") && r.Method == "DELETE":
		id := strings.TrimPrefix(path, "/access-control/grants/")
		if len(id) != 64 {
			fail(w, 400, "授权记录标识无效")
			return
		}
		// The immutable random grant ID is the expected revision for this record.
		if err := s.store.RevokeAccess(id, -1, requestSourceIP(r)); err != nil {
			storageFailure(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "接口或请求方法不存在")
	}
}
