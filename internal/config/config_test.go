package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func valid() Config {
	return Config{Sites: []Site{{ID: "site", Name: "站点", Domains: []string{"app.example.com"}, Enabled: true, HTTPPort: 18080, MaxBodyMiB: 20, Routes: []Route{{ID: "route", Name: "响应", Match: "prefix", Path: "/", Action: "response", Status: 200}}}}, Upstreams: []Upstream{}}
}
func TestDomainAndPath(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{{"APP.Example.com.", "app.example.com"}, {"*.example.com", "*.example.com"}, {"本地.example", "xn--cesv58a.example"}, {"::1", "::1"}} {
		got, err := Domain(tt.raw)
		if err != nil || got != tt.want {
			t.Errorf("Domain(%q)=%q,%v", tt.raw, got, err)
		}
	}
	for _, raw := range []string{"https://example.com", "foo:8080", "*.*.example.com", "-app.example", "app..example"} {
		if _, err := Domain(raw); err == nil {
			t.Errorf("接受非法域名 %q", raw)
		}
	}
	if !MatchDomain("*.example.com", "one.example.com") || MatchDomain("*.example.com", "one.two.example.com") || MatchDomain("*.example.com", "example.com") {
		t.Fatal("通配边界错误")
	}
	r := Route{Path: "/api", Match: "prefix"}
	if !MatchPath(r, "/api") || !MatchPath(r, "/api/users") || MatchPath(r, "/apix") {
		t.Fatal("前缀边界错误")
	}
}
func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"重复域名", func(c *Config) {
			c.Sites = append(c.Sites, Site{HTTPPort: 18080, Enabled: true, ID: "other", Name: "其他", Domains: []string{"APP.example.com"}, MaxBodyMiB: 20})
		}},
		{"缺失后端", func(c *Config) { c.Sites[0].Routes[0].Action = "proxy" }},
		{"路径混淆", func(c *Config) { c.Sites[0].Routes[0].Path = "/api/../admin" }},
		{"方法重叠", func(c *Config) {
			r := c.Sites[0].Routes[0]
			r.ID = "other"
			r.Methods = []string{"GET"}
			c.Sites[0].Routes = append(c.Sites[0].Routes, r)
		}},
		{"注入头部", func(c *Config) {
			c.Sites[0].Routes[0].ResponseHeaders = map[string]string{"Good": "hello\r\nBad: injected"}
		}},
		{"保留头部", func(c *Config) { c.Sites[0].Routes[0].RequestHeaders = map[string]string{"X-Forwarded-For": "fake"} }},
		{"无证书跳转", func(c *Config) { c.Sites[0].RedirectHTTPS = true }},
		{"含凭据后端", func(c *Config) {
			c.Upstreams = []Upstream{{ID: "u", Name: "服务", Targets: []Target{{URL: "http://user:pass@localhost", Weight: 1}}}}
		}},
		{"脚本跳转", func(c *Config) {
			r := &c.Sites[0].Routes[0]
			r.Action = "redirect"
			r.Status = 302
			r.Location = "javascript:alert(1)"
		}},
	}
	if issues := Validate(valid()); len(issues) > 0 {
		t.Fatalf("有效配置被拒绝: %+v", issues)
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.mutate(&c)
			if len(Validate(c)) == 0 {
				t.Fatal("无效配置被接受")
			}
		})
	}
	c := valid()
	r := c.Sites[0].Routes[0]
	r.ID = "get"
	r.Methods = []string{"GET"}
	c.Sites[0].Routes[0].Methods = []string{"POST"}
	c.Sites[0].Routes = append(c.Sites[0].Routes, r)
	if issues := Validate(c); len(issues) > 0 {
		t.Fatalf("不重叠方法被拒绝: %+v", issues)
	}
}
func TestExampleConfiguration(t *testing.T) {
	raw, err := os.ReadFile("../../examples/gateway.json")
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if issues := Validate(c); len(issues) > 0 {
		t.Fatalf("示例配置校验失败: %+v", issues)
	}
}

func TestSitePortsValidation(t *testing.T) {
	c := valid()
	second := c.Sites[0]
	second.ID = "second"
	second.Routes = nil
	second.HTTPPort++
	c.Sites = append(c.Sites, second)
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("相同域名不同端口被拒绝: %+v", issues)
	}
	c.Sites[1].HTTPPort = c.Sites[0].HTTPPort
	c.Sites[1].Domains = []string{"other.example.com"}
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("不同域名共享端口被拒绝: %+v", issues)
	}
	for _, change := range []func(*Site){
		func(s *Site) { s.HTTPPort = 0 }, func(s *Site) { s.HTTPPort = -1 }, func(s *Site) { s.HTTPPort = 65536 },
		func(s *Site) { s.HTTPS = true; s.CertificateID = "certificate"; s.HTTPSPort = s.HTTPPort },
		func(s *Site) { s.HTTPS = true; s.CertificateID = "certificate"; s.HTTPSPort = 0 },
		func(s *Site) { s.HTTPSPort = 8443 },
	} {
		v := valid()
		change(&v.Sites[0])
		if len(Validate(v)) == 0 {
			t.Fatal("无效端口配置被接受")
		}
	}
	c = valid()
	c.Sites[0].HTTPPort = 0
	c.Sites[0].HTTPS = true
	c.Sites[0].HTTPSPort = 65535
	c.Sites[0].CertificateID = "certificate"
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("仅 HTTPS 被拒绝: %+v", issues)
	}
	c.Sites[0].RedirectHTTPS = true
	if len(Validate(c)) == 0 {
		t.Fatal("无 HTTP 入口仍允许跳转")
	}
}

func TestRouteConflictDetails(t *testing.T) {
	for _, tt := range []struct {
		name          string
		first, second []string
		overlap       string
	}{
		{"指定方法交集", []string{"GET", "POST"}, []string{"POST", "DELETE", "POST"}, "POST"},
		{"前者全部方法", nil, []string{"GET", "POST"}, "GET、POST"},
		{"后者全部方法", []string{"GET", "POST"}, nil, "GET、POST"},
		{"全部方法", nil, nil, "全部方法"},
		{"自定义方法", []string{"PROPFIND"}, []string{"PROPFIND"}, "PROPFIND"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			first := &c.Sites[0].Routes[0]
			first.Name, first.Path, first.Methods = "旧接口", "/api", tt.first
			second := *first
			second.ID, second.Name, second.Methods = "new-route", "新接口", tt.second
			c.Sites[0].Routes = append(c.Sites[0].Routes, second)
			issues := Validate(c)
			if len(issues) != 1 || issues[0].Field != "sites[0].routes[1]" {
				t.Fatalf("未具体报告冲突：%+v", issues)
			}
			for _, detail := range []string{"站点「站点」", "app.example.com", "第 2 条「新接口」", "ID：new-route", "第 1 条「旧接口」", "ID：route", "匹配方式：路径前缀", "路径：/api", "重叠方法：" + tt.overlap} {
				if !strings.Contains(issues[0].Message, detail) {
					t.Fatalf("缺少 %q：%s", detail, issues[0].Message)
				}
			}
		})
	}
}
func TestRouteConflictsListEveryPair(t *testing.T) {
	c := valid()
	c.Sites[0].Routes[0].Match = "exact"
	for _, id := range []string{"second", "third"} {
		r := c.Sites[0].Routes[0]
		r.ID = id
		r.Name = ""
		c.Sites[0].Routes = append(c.Sites[0].Routes, r)
	}
	issues := Validate(c)
	if len(issues) != 3 {
		t.Fatalf("未列出全部冲突对：%+v", issues)
	}
	if !strings.Contains(issues[2].Message, "未命名路由") || !strings.Contains(issues[2].Message, "匹配方式：精确路径") {
		t.Fatal(issues[2])
	}
}

func TestRouteTimeoutValidation(t *testing.T) {
	for _, field := range []struct {
		name string
		set  func(*Route, int)
	}{
		{"connectTimeoutSeconds", func(r *Route, v int) { r.ConnectTimeoutSeconds = v }},
		{"tlsHandshakeTimeoutSeconds", func(r *Route, v int) { r.TLSHandshakeTimeoutSeconds = v }},
		{"responseHeaderTimeoutSeconds", func(r *Route, v int) { r.ResponseHeaderTimeoutSeconds = v }},
	} {
		for _, v := range []int{-1, 0, 86400, 86401} {
			c := valid()
			field.set(&c.Sites[0].Routes[0], v)
			issues := Validate(c)
			if v == 0 || v == 86400 {
				if len(issues) > 0 {
					t.Fatalf("有效超时被拒绝：%s %d %v", field.name, v, issues)
				}
			} else if len(issues) != 1 || issues[0].Field != "sites[0].routes[0]."+field.name {
				t.Fatalf("无效超时未被拒绝：%s %d %v", field.name, v, issues)
			}
		}
	}
}
