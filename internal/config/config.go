// Package config defines the portable, versioned gateway configuration.
package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/idna"
	"v2way/internal/headertemplate"
)

type Config struct {
	Sites      []Site      `json:"sites"`
	Upstreams  []Upstream  `json:"upstreams"`
	AccessGate *AccessGate `json:"accessGate,omitempty"`
}

// AccessGate is retained only to decode historical snapshots. New entries use LoginAction routes.
type AccessGate struct {
	Enabled     bool   `json:"enabled"`
	EntrySiteID string `json:"entrySiteId"`
	EntryDomain string `json:"entryDomain"`
	LoginPath   string `json:"loginPath"`
}
type Site struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Domains            []string `json:"domains"`
	Enabled            bool     `json:"enabled"`
	Maintenance        bool     `json:"maintenance,omitempty"`
	RequireIPLogin     bool     `json:"requireIpLogin,omitempty"`
	ForwardAdminCookie bool     `json:"forwardAdminCookie,omitempty"`
	HTTPPort           int      `json:"httpPort,omitempty"`
	HTTPSPort          int      `json:"httpsPort,omitempty"`
	HTTPS              bool     `json:"https"`
	RedirectHTTPS      bool     `json:"redirectHttps"`
	CertificateID      string   `json:"certificateId,omitempty"`
	MaxBodyMiB         int      `json:"maxBodyMiB"`
	RatePerSecond      int      `json:"ratePerSecond"`
	AllowCIDRs         []string `json:"allowCidrs"`
	DenyCIDRs          []string `json:"denyCidrs"`
	Routes             []Route  `json:"routes"`
}
type Route struct {
	ID                           string            `json:"id"`
	Name                         string            `json:"name"`
	Path                         string            `json:"path"`
	Match                        string            `json:"match"`
	Methods                      []string          `json:"methods"`
	Action                       string            `json:"action"`
	UpstreamID                   string            `json:"upstreamId,omitempty"`
	StripPrefix                  bool              `json:"stripPrefix"`
	PreserveHost                 bool              `json:"preserveHost"`
	Root                         string            `json:"root,omitempty"`
	SPA                          bool              `json:"spa"`
	Status                       int               `json:"status"`
	Body                         string            `json:"body,omitempty"`
	Location                     string            `json:"location,omitempty"`
	TimeoutSeconds               int               `json:"timeoutSeconds"`
	ConnectTimeoutSeconds        int               `json:"connectTimeoutSeconds,omitempty"`
	TLSHandshakeTimeoutSeconds   int               `json:"tlsHandshakeTimeoutSeconds,omitempty"`
	ResponseHeaderTimeoutSeconds int               `json:"responseHeaderTimeoutSeconds,omitempty"`
	Streaming                    bool              `json:"streaming"`
	RequestHeaders               map[string]string `json:"requestHeaders,omitempty"`
	ResponseHeaders              map[string]string `json:"responseHeaders,omitempty"`
	HeaderVariables              bool              `json:"headerVariables,omitempty"`
}
type Upstream struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Targets       []Target `json:"targets"`
	HealthPath    string   `json:"healthPath,omitempty"`
	SkipTLSVerify bool     `json:"skipTlsVerify,omitempty"`
}
type Target struct {
	URL    string `json:"url"`
	Weight int    `json:"weight"`
}
type Issue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var method = regexp.MustCompile(`^[A-Z]{1,24}$`)
var protected = map[string]bool{"connection": true, "upgrade": true, "host": true, "content-length": true, "transfer-encoding": true, "te": true, "trailer": true, "proxy-authorization": true, "proxy-authenticate": true, "forwarded": true, "x-real-ip": true, "x-forwarded-for": true, "x-forwarded-host": true, "x-forwarded-proto": true}

func Empty() Config { return Config{Sites: []Site{}, Upstreams: []Upstream{}} }

// Normalize keeps array fields usable by JSON clients even in incomplete drafts.
func Normalize(c Config) Config {
	if c.AccessGate != nil {
		gate := *c.AccessGate
		if domain, err := Domain(gate.EntryDomain); err == nil {
			gate.EntryDomain = domain
		}
		c.AccessGate = &gate
	}
	if c.Sites == nil {
		c.Sites = []Site{}
	}
	if c.Upstreams == nil {
		c.Upstreams = []Upstream{}
	}
	for i := range c.Sites {
		s := &c.Sites[i]
		if s.Domains == nil {
			s.Domains = []string{}
		}
		if s.AllowCIDRs == nil {
			s.AllowCIDRs = []string{}
		}
		if s.DenyCIDRs == nil {
			s.DenyCIDRs = []string{}
		}
		if s.Routes == nil {
			s.Routes = []Route{}
		}
		for j := range s.Routes {
			if s.Routes[j].Methods == nil {
				s.Routes[j].Methods = []string{}
			}
		}
	}
	for i := range c.Upstreams {
		if c.Upstreams[i].Targets == nil {
			c.Upstreams[i].Targets = []Target{}
		}
	}
	return c
}
func Domain(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	prefix := ""
	if strings.HasPrefix(raw, "*.") {
		prefix = "*."
		raw = raw[2:]
	}
	if net.ParseIP(raw) != nil {
		if prefix != "" {
			return "", fmt.Errorf("IP 不支持通配符")
		}
		return raw, nil
	}
	ascii, err := idna.Lookup.ToASCII(raw)
	if err != nil || len(ascii) == 0 || len(ascii) > 253 || strings.ContainsAny(ascii, "/:@* \\[]") {
		return "", fmt.Errorf("域名格式无效")
	}
	for _, label := range strings.Split(ascii, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("域名格式无效")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("域名格式无效")
			}
		}
	}
	return prefix + ascii, nil
}
func MatchDomain(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) {
		left := strings.TrimSuffix(host, pattern[1:])
		return left != "" && !strings.Contains(left, ".")
	}
	return false
}
func MatchPath(r Route, path string) bool {
	if r.Match == "exact" {
		return path == r.Path
	}
	return r.Path == "/" || path == r.Path || strings.HasPrefix(path, strings.TrimSuffix(r.Path, "/")+"/")
}
func MatchMethod(r Route, m string) bool {
	if len(r.Methods) == 0 {
		return true
	}
	for _, v := range r.Methods {
		if v == m {
			return true
		}
	}
	return false
}
func SortRoutes(routes []Route) {
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		if a.Match != b.Match {
			return a.Match == "exact"
		}
		return len(a.Path) > len(b.Path)
	})
}

func Validate(c Config) []Issue {
	issues := []Issue{}
	add := func(f, m string) { issues = append(issues, Issue{f, m}) }
	validateAccessGate(c, add)
	if len(c.Sites) > 500 || len(c.Upstreams) > 500 {
		add("config", "站点与后端池各最多 500 个")
	}
	ids := map[string]bool{}
	checkID := func(id, field string) {
		if !identifier.MatchString(id) || ids[id] {
			add(field, "标识必须唯一，且由 1–64 位字母、数字、下划线或短横线组成")
		}
		ids[id] = true
	}
	upstreams := map[string]bool{}
	for i, u := range c.Upstreams {
		f := fmt.Sprintf("upstreams[%d]", i)
		checkID(u.ID, f+".id")
		upstreams[u.ID] = true
		if strings.TrimSpace(u.Name) == "" {
			add(f+".name", "请填写名称")
		}
		if len(u.Targets) == 0 || len(u.Targets) > 100 {
			add(f+".targets", "后端池需包含 1–100 个地址")
		}
		seen := map[string]bool{}
		for j, t := range u.Targets {
			v, e := url.Parse(t.URL)
			if e != nil || v == nil || v.Host == "" || (v.Scheme != "http" && v.Scheme != "https") || v.User != nil || v.RawQuery != "" || v.Fragment != "" || (v.Path != "" && v.Path != "/") {
				add(fmt.Sprintf("%s.targets[%d]", f, j), "请输入不含凭据、查询或业务路径的 HTTP/HTTPS 地址")
			}
			if seen[t.URL] {
				add(f+".targets", "后端地址重复")
			}
			seen[t.URL] = true
			if t.Weight < 1 || t.Weight > 100 {
				add(f+".targets", "权重范围为 1–100")
			}
		}
		if u.HealthPath != "" && (!strings.HasPrefix(u.HealthPath, "/") || strings.HasPrefix(u.HealthPath, "//") || strings.ContainsAny(u.HealthPath, "\r\n")) {
			add(f+".healthPath", "健康检查路径必须以单个 / 开头")
		}
	}
	domains := map[string]string{}
	ports := map[int]string{}
	total := 0
	for i, s := range c.Sites {
		f := fmt.Sprintf("sites[%d]", i)
		checkID(s.ID, f+".id")
		if strings.TrimSpace(s.Name) == "" {
			add(f+".name", "请填写名称")
		}
		if s.HTTPPort < 0 || s.HTTPPort > 65535 {
			add(f+".httpPort", "HTTP 端口范围为 1–65535，0 表示不启用")
		}
		if s.HTTPSPort < 0 || s.HTTPSPort > 65535 || s.HTTPS && s.HTTPSPort == 0 {
			add(f+".httpsPort", "启用 HTTPS 时必须填写 1–65535 的端口")
		}
		if !s.HTTPS && s.HTTPSPort != 0 {
			add(f+".httpsPort", "请先启用 HTTPS，或清空 HTTPS 端口")
		}
		if s.Enabled && s.HTTPPort == 0 && !s.HTTPS {
			add(f+".httpPort", "启用站点必须设置 HTTP 或 HTTPS 端口")
		}
		if s.RedirectHTTPS && (!s.HTTPS || s.HTTPPort == 0) {
			add(f+".redirectHttps", "自动跳转需要同时启用 HTTP 和 HTTPS 端口")
		}
		if s.Enabled && s.HTTPPort > 0 {
			ports[s.HTTPPort] = "http"
		}
		if len(s.Domains) == 0 || len(s.Domains) > 100 {
			add(f+".domains", "站点需包含 1–100 个域名")
		}
		for _, raw := range s.Domains {
			d, e := Domain(raw)
			if e != nil {
				add(f+".domains", "域名格式无效："+raw)
				continue
			}
			if s.Enabled {
				for _, port := range []int{s.HTTPPort, s.HTTPSPort} {
					if port == 0 {
						continue
					}
					key := fmt.Sprintf("%d/%s", port, d)
					if other, ok := domains[key]; ok {
						add(f+".domains", fmt.Sprintf("端口 %d 的域名与站点 %s 重复", port, other))
					}
					domains[key] = s.Name
				}
			}
		}
		if s.HTTPS && !identifier.MatchString(s.CertificateID) {
			add(f+".certificateId", "HTTPS 站点必须选择有效证书")
		}
		if s.RedirectHTTPS && !s.HTTPS {
			add(f+".redirectHttps", "强制 HTTPS 需要先启用 HTTPS")
		}
		if s.MaxBodyMiB < 1 || s.MaxBodyMiB > 1024 {
			add(f+".maxBodyMiB", "请求体上限范围为 1–1024 MiB")
		}
		if s.RatePerSecond < 0 || s.RatePerSecond > 100000 {
			add(f+".ratePerSecond", "每秒请求限制范围为 0–100000，0 表示不限")
		}
		for _, cidr := range append(append([]string{}, s.AllowCIDRs...), s.DenyCIDRs...) {
			if _, _, e := net.ParseCIDR(cidr); e != nil {
				add(f+".cidrs", "CIDR 格式无效："+cidr)
			}
		}
		total += len(s.Routes)
		for j, r := range s.Routes {
			rf := fmt.Sprintf("%s.routes[%d]", f, j)
			checkID(r.ID, rf+".id")
			if r.Match != "exact" && r.Match != "prefix" {
				add(rf+".match", "路径类型必须为精确或前缀")
			}
			if !strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, "?#%\\\r\n") || strings.Contains(r.Path, "//") || filepath.Clean(r.Path) != r.Path {
				add(rf+".path", "请填写规范的绝对路径，不含查询、编码或尾斜杠（根路径除外）")
			}
			for _, m := range r.Methods {
				if !method.MatchString(m) {
					add(rf+".methods", "HTTP 方法必须为大写字母")
				}
			}
			if r.TimeoutSeconds < 0 || r.TimeoutSeconds > 86400 {
				add(rf+".timeoutSeconds", "超时范围为 0–86400 秒")
			}
			for _, field := range []struct {
				name, label string
				value       int
			}{
				{"connectTimeoutSeconds", "后端连接超时", r.ConnectTimeoutSeconds},
				{"tlsHandshakeTimeoutSeconds", "TLS 握手超时", r.TLSHandshakeTimeoutSeconds},
				{"responseHeaderTimeoutSeconds", "响应头等待超时", r.ResponseHeaderTimeoutSeconds},
			} {
				if field.value < 0 || field.value > 86400 {
					add(rf+"."+field.name, field.label+"范围为 0–86400 秒，0 使用默认值")
				}
			}
			switch r.Action {
			case LoginAction:
				// Entry constraints are validated per site.
			case "proxy":
				if !upstreams[r.UpstreamID] {
					add(rf+".upstreamId", "后端池不存在")
				}
			case "static":
				if !filepath.IsAbs(r.Root) {
					add(rf+".root", "静态根目录必须为绝对路径")
				}
			case "response":
				if r.Status < 200 || r.Status > 599 || r.Status == 204 && r.Body != "" {
					add(rf+".status", "响应状态无效或 204 含有正文")
				}
			case "redirect":
				if r.Status != 301 && r.Status != 302 && r.Status != 307 && r.Status != 308 {
					add(rf+".status", "跳转状态必须为 301、302、307 或 308")
				}
				v, e := url.Parse(r.Location)
				if e != nil || v == nil || r.Location == "" || strings.HasPrefix(r.Location, "//") || strings.ContainsAny(r.Location, "\r\n") || !(strings.HasPrefix(r.Location, "/") || (v.Scheme == "http" || v.Scheme == "https") && v.Host != "" && v.User == nil) {
					add(rf+".location", "跳转目标必须为站内绝对路径或 HTTP/HTTPS 地址")
				}
			default:
				add(rf+".action", "不支持的路由动作")
			}
			if r.HeaderVariables && r.Action != "proxy" {
				add(rf+".headerVariables", "Header 变量仅支持反向代理路由")
			}
			for index, headers := range []map[string]string{r.RequestHeaders, r.ResponseHeaders} {
				if r.HeaderVariables {
					if _, err := headertemplate.CompileHeaders(headers); err != nil {
						direction := "requestHeaders"
						if index == 1 {
							direction = "responseHeaders"
						}
						add(rf+"."+direction, fmt.Sprintf("站点「%s」路由「%s」（%s）：%s", s.Name, r.Name, r.ID, err))
					}
				}
				for k, v := range headers {
					if !validHeader(k) || protected[strings.ToLower(k)] || strings.ContainsAny(v, "\r\n") || len(v) > 8192 {
						add(rf+".headers", "请求头名称、值无效或使用了协议保留字段")
					}
				}
			}
			for k, prev := range s.Routes[:j] {
				if prev.Match != r.Match || prev.Path != r.Path {
					continue
				}
				methods := overlappingMethods(prev.Methods, r.Methods)
				if len(methods) == 0 {
					continue
				}
				match := "路径前缀"
				if r.Match == "exact" {
					match = "精确路径"
				}
				siteName := s.Name
				if strings.TrimSpace(siteName) == "" {
					siteName = "未命名站点"
				}
				add(rf, fmt.Sprintf("站点「%s」（ID：%s，域名：%s）存在冲突路由：\n%s\n%s\n匹配方式：%s；路径：%s；重叠方法：%s",
					siteName, s.ID, strings.Join(s.Domains, "、"),
					routeConflictLabel(r, j), routeConflictLabel(prev, k),
					match, r.Path, strings.Join(methods, "、")))
			}
		}
	}
	for i, s := range c.Sites {
		if s.Enabled && s.HTTPS && s.HTTPSPort > 0 && ports[s.HTTPSPort] == "http" {
			add(fmt.Sprintf("sites[%d].httpsPort", i), "同一端口不能混用 HTTP 与 HTTPS")
		}
	}
	if total > 5000 {
		add("routes", "路由总数最多 5000 条")
	}
	return issues
}
func validHeader(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

// Empty method sets match every HTTP method, including extension methods.
func overlappingMethods(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return []string{"全部方法"}
	}
	if len(a) == 0 {
		a, b = b, a
	}
	seen := map[string]bool{}
	methods := []string{}
	for _, method := range a {
		if len(b) == 0 {
			seen[method] = true
			continue
		}
		for _, other := range b {
			if method == other {
				seen[method] = true
				break
			}
		}
	}
	for method := range seen {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}
func routeConflictLabel(r Route, index int) string {
	name := r.Name
	if strings.TrimSpace(name) == "" {
		name = "未命名路由"
	}
	methods := "全部方法"
	if len(r.Methods) > 0 {
		methods = strings.Join(r.Methods, "、")
	}
	return fmt.Sprintf("第 %d 条「%s」（ID：%s；方法：%s）", index+1, name, r.ID, methods)
}
