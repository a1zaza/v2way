package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeaderVariableConfiguration(t *testing.T) {
	c := valid()
	r := &c.Sites[0].Routes[0]
	r.RequestHeaders = map[string]string{"X-Literal": "${unknown} $$"}
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal("旧配置不应解析模板", issues)
	}
	r.HeaderVariables = true
	if len(Validate(c)) == 0 {
		t.Fatal("非代理动作允许变量")
	}
	r.Action = "proxy"
	r.UpstreamID = "upstream"
	c.Upstreams = []Upstream{{ID: "upstream", Name: "后端", Targets: []Target{{URL: "http://127.0.0.1:8000", Weight: 1}}}}
	issues := Validate(c)
	if len(issues) == 0 || !strings.Contains(issues[0].Field, "requestHeaders") || !strings.Contains(issues[0].Message, "站点") || strings.Contains(issues[0].Message, "unknown") {
		t.Fatalf("模板错误定位或脱敏错误：%+v", issues)
	}
	r.RequestHeaders = map[string]string{"X-Client-IP": "${remote_addr}"}
	r.ResponseHeaders = map[string]string{"X-Trace": "${request_id}"}
	if issues := Validate(c); len(issues) != 0 {
		t.Fatal(issues)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var copied Config
	if err := json.Unmarshal(raw, &copied); err != nil || !copied.Sites[0].Routes[0].HeaderVariables || copied.Sites[0].Routes[0].RequestHeaders["X-Client-IP"] != "${remote_addr}" {
		t.Fatal("JSON 往返丢失模板", err)
	}
	for _, headers := range []map[string]string{
		{"X-Trace": "${request_id}", "x-trace": "duplicate"},
		{"Host": "${host}"}, {"X-Forwarded-For": "${remote_addr}"}, {"X-Trace": "secret\x00"},
	} {
		r.ResponseHeaders = headers
		if len(Validate(c)) == 0 {
			t.Fatalf("无效响应头被接受：%v", headers)
		}
	}
}
