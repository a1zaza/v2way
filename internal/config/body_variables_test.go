package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBodyVariableConfiguration(t *testing.T) {
	c := valid()
	r := &c.Sites[0].Routes[0]
	r.Action, r.Status, r.Body = "response", 200, "${unknown}\n固定文本"
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal("旧配置应原样返回", issues)
	}
	r.BodyVariables = true
	issues := Validate(c)
	if len(issues) == 0 || !strings.HasSuffix(issues[0].Field, ".body") || strings.Contains(issues[0].Message, "unknown") {
		t.Fatal("错误定位或脱敏不正确", issues)
	}
	r.Body = "来源=${remote_addr}\n路径=${uri}"
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal(issues)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var copy Config
	if err := json.Unmarshal(raw, &copy); err != nil || !copy.Sites[0].Routes[0].BodyVariables || copy.Sites[0].Routes[0].Body != r.Body {
		t.Fatal("JSON 往返丢失正文变量", err)
	}
	r.Action, r.Location, r.Status = "redirect", "/target", 302
	if len(Validate(c)) == 0 {
		t.Fatal("非固定响应动作允许正文变量")
	}
	r.Action, r.Status = "response", 204
	if len(Validate(c)) == 0 {
		t.Fatal("204 允许含正文模板")
	}
}
