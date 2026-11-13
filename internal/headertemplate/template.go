// Package headertemplate compiles opt-in HTTP header value templates.
package headertemplate

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const MaxBytes = 8192

var names = [...]string{"remote_addr", "remote_port", "scheme", "host", "http_host", "request_method", "uri", "request_id", "site_id", "route_id"}

func Names() []string { return append([]string(nil), names[:]...) }

type part struct{ text, variable string }
type Template struct{ parts []part }
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
	var t Template
	if !validValue(value) {
		return t, errors.New("Header 值含非法字符或超过 8192 字节")
	}
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
				return Template{}, errors.New("Header 变量缺少结束括号")
			}
			name := value[i+2 : i+2+end]
			known := false
			for _, candidate := range names {
				known = known || name == candidate
			}
			if !known {
				// Never echo an arbitrary substring of a potentially secret value.
				return Template{}, errors.New("Header 变量为空或不受支持，请使用变量列表中的名称")
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
	var result strings.Builder
	for _, p := range t.parts {
		value := p.text
		if p.variable != "" {
			value = context[p.variable]
			if value == "" {
				return "", fmt.Errorf("Header 变量 %s 不可用", p.variable)
			}
		}
		if result.Len()+len(value) > MaxBytes || !validValue(value) {
			return "", errors.New("Header 展开结果含非法字符或超过 8192 字节")
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
