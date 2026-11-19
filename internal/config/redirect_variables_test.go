package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedirectVariableConfiguration(t *testing.T) {
	c := valid()
	r := &c.Sites[0].Routes[0]
	r.Action, r.Status, r.Location = "redirect", 302, "/literal/${unknown}$$"
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal("关闭模式不应解析模板", issues)
	}
	r.LocationVariables = true
	if issues := Validate(c); len(issues) == 0 || !strings.HasSuffix(issues[0].Field, ".location") || strings.Contains(issues[0].Message, "unknown") {
		t.Fatal("模板错误未定位或泄漏原文", issues)
	}
	for _, location := range []string{"https://new.example.com${request_uri}", "/new${uri}${is_args}${args}", "${scheme}://${http_host}${uri}", "${uri}", "/literal/$${unknown}"} {
		r.Location = location
		if issues := Validate(c); len(issues) != 0 {
			t.Fatal(location, issues)
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var copied Config
	if err = json.Unmarshal(raw, &copied); err != nil || !copied.Sites[0].Routes[0].LocationVariables || copied.Sites[0].Routes[0].Location != r.Location {
		t.Fatal("JSON 往返丢失模板", err)
	}
	for _, location := range []string{"", "${unknown}", "https://new.example.com${uri", "//evil.example/${uri}", "/\\evil.example/${uri}", "javascript:${uri}", "https://user:pass@example.com${uri}", "/bad\r\nX:1", strings.Repeat("x", 8193)} {
		r.Location = location
		if len(Validate(c)) == 0 {
			t.Fatal("无效模板被接受", location)
		}
	}
	r.Action, r.Status, r.Body = "response", 200, "ok"
	if len(Validate(c)) == 0 {
		t.Fatal("非重定向动作允许跳转变量")
	}
}

func TestExpandedRedirectLocationValidation(t *testing.T) {
	for _, value := range []string{"https://example.com/a%2Fb?x=1&x=2", "/a?x=%2F", "/", "http://[::1]:8080/a"} {
		if !ValidRedirectLocation(value) {
			t.Fatal("合法地址被拒绝", value)
		}
	}
	for _, value := range []string{"//evil.example", "/\\evil.example", "javascript:alert(1)", "https://user@example.com/", "/bad\nInjected:1", "/bad\tvalue", "https:///empty", "/bad%zz", strings.Repeat("/", 8193)} {
		if ValidRedirectLocation(value) {
			t.Fatal("非法地址被接受", value)
		}
	}
}
