package headertemplate

import "testing"

func TestLocationTemplateEmptyQueryAndEscapes(t *testing.T) {
	template, err := CompileLocation("/next${uri}${is_args}${args}/$${uri}/$$")
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Expand(Context{"uri": "/path", "is_args": "", "args": ""})
	if err != nil || got != "/next/path/${uri}/$" {
		t.Fatal(got, err)
	}
	if _, err = template.Expand(Context{"uri": "/path"}); err == nil {
		t.Fatal("缺失查询上下文应拒绝")
	}
	for _, compile := range []func(string) (Template, error){Compile, CompileBody, CompileLocation} {
		tpl, err := compile("${request_uri}${is_args}${args}")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := tpl.Expand(Context{"request_uri": "/path", "is_args": "", "args": ""}); err != nil || got != "/path" {
			t.Fatal(got, err)
		}
	}
}
