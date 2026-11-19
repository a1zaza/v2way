package config

import (
	"errors"
	"net/url"
	"strings"

	"v2way/internal/headertemplate"
)

// ValidRedirectLocation checks the final URL, including after variable expansion.
func ValidRedirectLocation(value string) bool {
	if value == "" || len(value) > headertemplate.MaxBytes || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 32 || value[i] == 127 {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil {
		return false
	}
	if strings.HasPrefix(value, "/") {
		return parsed.Scheme == "" && parsed.Host == ""
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
}

// CompileRedirectLocation validates the template shape without using a live request.
func CompileRedirectLocation(value string) (headertemplate.Template, error) {
	template, err := headertemplate.CompileLocation(value)
	if err != nil {
		return template, err
	}
	sample := headertemplate.Context{}
	for _, name := range headertemplate.Names() {
		sample[name] = "value"
	}
	sample["remote_addr"], sample["remote_port"] = "192.0.2.1", "1234"
	sample["scheme"], sample["host"], sample["http_host"] = "https", "example.com", "example.com:443"
	sample["uri"], sample["request_uri"] = "/path", "/path?a=1"
	sample["args"], sample["is_args"] = "a=1", "?"
	expanded, err := template.Expand(sample)
	if err != nil || !ValidRedirectLocation(expanded) {
		return headertemplate.Template{}, errors.New("跳转目标必须展开为站内绝对路径或不含用户信息的 HTTP/HTTPS 地址")
	}
	return template, nil
}
