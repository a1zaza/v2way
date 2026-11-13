package headertemplate

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestTemplateSyntaxAndExpansion(t *testing.T) {
	context := Context{"remote_addr": "192.0.2.1", "route_id": "route", "http_host": "${remote_addr}"}
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {"fixed", "fixed"}, {"${remote_addr}", "192.0.2.1"},
		{"ip=${remote_addr}; route=${route_id}", "ip=192.0.2.1; route=route"},
		{"$remote_addr $ $$", "$remote_addr $ $"},
		{"$${remote_addr}", "${remote_addr}"},
		{"$$${remote_addr}", "$192.0.2.1"},
		{"${http_host}", "${remote_addr}"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			template, err := Compile(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := template.Expand(context)
			if err != nil || got != tc.want {
				t.Fatalf("展开错误：%q %v", got, err)
			}
		})
	}
}

func TestTemplateRejectsInvalidInputWithoutLeakingValues(t *testing.T) {
	for _, input := range []string{"${}", "${secret-password}", "${REMOTE_ADDR}", "${remote_addr", "${remote_addr${route_id}}", "secret\x00", "secret\r\nInjected: 1", "secret\x7f", strings.Repeat("x", MaxBytes+1)} {
		if _, err := Compile(input); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("未拒绝或泄露值：%v", err)
		}
	}
	template, _ := Compile("${http_host}${http_host}")
	for _, value := range []string{"", "secret\x01", "secret\r\nX: 1", strings.Repeat("a", MaxBytes/2+1), strings.Repeat("中", MaxBytes/6+1)} {
		if _, err := template.Expand(Context{"http_host": value}); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("非法展开未拒绝或泄露值：%v", err)
		}
	}
	limit, _ := Compile(strings.Repeat("a", MaxBytes))
	if got, err := limit.Expand(nil); err != nil || len(got) != MaxBytes {
		t.Fatal("边界值错误", err)
	}
	if _, err := CompileHeaders(map[string]string{"X-Key": "a", "x-key": "b"}); err == nil {
		t.Fatal("未拒绝同名 Header")
	}
}

func TestVariableRegistryMatchesConsole(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/header-variables.ts")
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`\[\s*'([a-z_]+)',`).FindAllSubmatch(raw, -1)
	var actual []string
	for _, match := range matches {
		actual = append(actual, string(match[1]))
	}
	want := Names()
	sort.Strings(actual)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("前后端变量列表不一致：%v / %v", actual, want)
	}
}
