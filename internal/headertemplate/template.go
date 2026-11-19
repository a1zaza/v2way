// Package headertemplate compiles opt-in request variable templates for headers, response bodies and redirects.
package headertemplate

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const MaxBytes = 8192
const MaxBodyBytes = 64 << 10

var names = [...]string{"remote_addr", "remote_port", "scheme", "host", "http_host", "request_method", "uri", "request_uri", "args", "is_args", "request_id", "site_id", "route_id"}

func Names() []string { return append([]string(nil), names[:]...) }

type part struct{ text, variable string }
type Template struct {
	parts []part
	body  bool
	label string
}
type Headers map[string]Template
type Context map[string]string

func validValue(value string) bool {
	if len(value) > MaxBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 32 && value[i] != '\t' || value[i] == 127 {
			return false
		}
	}
	return true
}

func Compile(value string) (Template, error) {
	if !validValue(value) {
		return Template{}, errors.New("Header 值含非法字符或超过 8192 字节")
	}
	return compile(value, false, "Header")
}

// CompileBody allows multiline text while retaining the fixed variable registry.
func CompileBody(value string) (Template, error) {
	if len(value) > MaxBodyBytes {
		return Template{}, errors.New("响应正文模板超过 65536 字节")
	}
	return compile(value, true, "响应正文")
}

func CompileLocation(value string) (Template, error) {
	if !validValue(value) {
		return Template{}, errors.New("跳转目标含非法字符或超过 8192 字节")
	}
	return compile(value, false, "跳转目标")
}

func compile(value string, body bool, label string) (Template, error) {
	t := Template{body: body, label: label}
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			t.parts = append(t.parts, part{text: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(value); {
		if strings.HasPrefix(value[i:], "$$") {
			literal.WriteByte('$')
			i += 2
		} else if strings.HasPrefix(value[i:], "${") {
			end := strings.IndexByte(value[i+2:], '}')
			if end < 0 {
				return Template{}, fmt.Errorf("%s 变量缺少结束括号", label)
			}
			name := value[i+2 : i+2+end]
			known := false
			for _, candidate := range names {
				known = known || name == candidate
			}
			if !known {
				// Never echo an arbitrary substring of a potentially secret value.
				return Template{}, fmt.Errorf("%s 变量为空或不受支持，请使用变量列表中的名称", label)
			}
			flush()
			t.parts = append(t.parts, part{variable: name})
			i += end + 3
		} else {
			literal.WriteByte(value[i])
			i++
		}
	}
	flush()
	return t, nil
}

func (t Template) Expand(context Context) (string, error) {
	label, limit := t.label, MaxBytes
	if label == "" {
		label = "Header"
	}
	if t.body {
		limit = MaxBodyBytes
	}
	var result strings.Builder
	for _, p := range t.parts {
		value := p.text
		if p.variable != "" {
			var exists bool
			value, exists = context[p.variable]
			if !exists || value == "" && p.variable != "args" && p.variable != "is_args" {
				return "", fmt.Errorf("%s 变量 %s 不可用", label, p.variable)
			}
		}
		if result.Len()+len(value) > limit || !t.body && !validValue(value) {
			return "", fmt.Errorf("%s 展开结果含非法字符或超过 %d 字节", label, limit)
		}
		result.WriteString(value)
	}
	return result.String(), nil
}

func CompileHeaders(values map[string]string) (Headers, error) {
	compiled := make(Headers, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		canonical := http.CanonicalHeaderKey(key)
		if _, exists := compiled[canonical]; exists {
			return nil, fmt.Errorf("Header %s 大小写重复", canonical)
		}
		t, err := Compile(values[key])
		if err != nil {
			return nil, fmt.Errorf("Header %s：%w", key, err)
		}
		compiled[canonical] = t
	}
	return compiled, nil
}

func (headers Headers) Expand(context Context) (map[string]string, error) {
	values := make(map[string]string, len(headers))
	for key, t := range headers {
		value, err := t.Expand(context)
		if err != nil {
			return nil, fmt.Errorf("Header %s：%w", key, err)
		}
		values[key] = value
	}
	return values, nil
}
