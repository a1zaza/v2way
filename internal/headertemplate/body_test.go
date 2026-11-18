package headertemplate

import (
	"strings"
	"testing"
)

func TestBodyTemplateMultilineAndHeaderIsolation(t *testing.T) {
	input := "来源=${remote_addr}\r\n路径=${uri}\n转义=$${unknown} $$\t${http_host}"
	template, err := CompileBody(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Expand(Context{"remote_addr": "192.0.2.1", "uri": "/a%20b", "http_host": "${remote_addr}"})
	if err != nil || got != "来源=192.0.2.1\r\n路径=/a%20b\n转义=${unknown} $\t${remote_addr}" {
		t.Fatal(got, err)
	}
	if _, err := Compile(input); err == nil {
		t.Fatal("正文模板放宽了 Header 换行限制")
	}
	for _, input := range []string{"${private-secret}", "${remote_addr", strings.Repeat("x", MaxBodyBytes+1)} {
		if _, err := CompileBody(input); err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("未拒绝非法正文模板或泄漏原文", err)
		}
	}
	template, err = CompileBody(strings.Repeat("中", MaxBodyBytes/3) + "x")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := template.Expand(nil); err != nil || len(got) != MaxBodyBytes {
		t.Fatal("字节边界错误", len(got), err)
	}
	template, _ = CompileBody("${uri}${uri}")
	if _, err := template.Expand(Context{"uri": strings.Repeat("x", MaxBodyBytes/2+1)}); err == nil {
		t.Fatal("展开结果超过上限")
	}
	if _, err := template.Expand(nil); err == nil {
		t.Fatal("缺失变量应拒绝")
	}
}
