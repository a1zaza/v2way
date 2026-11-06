package config

import (
	"fmt"
	"strings"
)

const LoginAction = "access_login"

// ValidateStored allows reading signed legacy snapshots, but never restores the
// removed global entry. New publications must pass Validate instead.
func ValidateStored(c Config) []Issue {
	if c.AccessGate == nil {
		return Validate(c)
	}
	c.AccessGate = nil
	c.Sites = append([]Site{}, c.Sites...)
	for i := range c.Sites {
		c.Sites[i].RequireIPLogin = false
	}
	return Validate(c)
}
func validateAccessGate(c Config, add func(string, string)) {
	if c.AccessGate != nil {
		add("accessGate", "全局隐藏入口已移除，请改用站点路由的 IP 授权登录页动作")
	}
	for i, site := range c.Sites {
		count := 0
		for j, route := range site.Routes {
			if route.Action != LoginAction {
				continue
			}
			count++
			field := fmt.Sprintf("sites[%d].routes[%d]", i, j)
			if !site.HTTPS {
				add(field, "IP 授权登录页需要为所属站点启用 HTTPS")
			}
			if route.Match != "exact" || route.Path == "/" {
				add(field, "IP 授权登录页必须使用非根路径的精确匹配，例如 /access")
			}
			if !MatchMethod(route, "GET") || !MatchMethod(route, "POST") {
				add(field+".methods", "IP 授权登录页必须同时允许 GET 和 POST")
			}
			for k, other := range site.Routes {
				if k == j {
					continue
				}
				if other.Path == route.Path || strings.HasPrefix(other.Path, route.Path+"/") {
					add(field+".path", "登录路径及其子路径需保留给登录页，请移除冲突业务路由")
				}
			}
		}
		if count > 1 {
			add(fmt.Sprintf("sites[%d].routes", i), "每个站点只能配置一个 IP 授权登录页")
		}
		if site.RequireIPLogin && count == 0 {
			add(fmt.Sprintf("sites[%d].routes", i), "开启 IP 授权保护前，请为本站添加 IP 授权登录页路由")
		}
	}
}
