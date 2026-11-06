package config

import "testing"

func TestSiteLoginRouteValidation(t *testing.T) {
	c := Config{Sites: []Site{{ID: "s", Name: "site", Enabled: true, HTTPS: true, HTTPSPort: 18443, CertificateID: "cert", Domains: []string{"app.example.com"}, MaxBodyMiB: 20, RequireIPLogin: true, Routes: []Route{{ID: "r", Path: "/", Match: "prefix", Action: "response", Status: 200}}}}, Upstreams: []Upstream{}}
	if len(Validate(c)) == 0 {
		t.Fatal("保护站点缺少登录路由")
	}
	login := Route{ID: "login", Path: "/access", Match: "exact", Action: LoginAction, Methods: []string{"GET", "POST"}}
	c.Sites[0].Routes = append(c.Sites[0].Routes, login)
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, change := range []func(*Config){func(c *Config) { c.Sites[0].HTTPS = false; c.Sites[0].HTTPSPort = 0; c.Sites[0].CertificateID = "" }, func(c *Config) { c.Sites[0].Routes[1].Match = "prefix" }, func(c *Config) { c.Sites[0].Routes[1].Path = "/" }, func(c *Config) { c.Sites[0].Routes[1].Methods = []string{"GET"} }, func(c *Config) { c.Sites[0].Routes[0].Path = "/access/status" }, func(c *Config) {
		other := login
		other.ID = "second"
		other.Path = "/another"
		c.Sites[0].Routes = append(c.Sites[0].Routes, other)
	}} {
		copy := c
		copy.Sites = append([]Site{}, c.Sites...)
		copy.Sites[0].Routes = append([]Route{}, c.Sites[0].Routes...)
		change(&copy)
		if len(Validate(copy)) == 0 {
			t.Fatal("不安全路由未被拒绝", copy)
		}
	}
	c.Sites[0].Maintenance = true
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal("本站维护不应阻止发布", issues)
	}
}
func TestLegacyGlobalEntryCanBeReadButNotPublished(t *testing.T) {
	c := Config{Sites: []Site{{ID: "s", Name: "site", Enabled: true, HTTPPort: 18080, Domains: []string{"app.example.com"}, MaxBodyMiB: 20, RequireIPLogin: true}}, AccessGate: &AccessGate{Enabled: true}}
	if len(Validate(c)) == 0 {
		t.Fatal("新发布接受了旧全局入口")
	}
	if issues := ValidateStored(c); len(issues) != 0 {
		t.Fatal("旧运行文件无法加载", issues)
	}
}
