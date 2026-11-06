package gateway

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

//go:embed status_page.html
var statusPageHTML string

// Status pages are self-contained: no scripts, external assets, or reflected URLs.
var statusPage = template.Must(template.New("status").Parse(statusPageHTML))

func serveStatusPage(w http.ResponseWriter, r *http.Request, status int) {
	maintenance := status == http.StatusServiceUnavailable
	title, description := "页面未找到", "当前地址暂时没有可访问的内容，请检查地址是否正确，或返回首页继续访问。"
	if maintenance {
		title, description = "站点维护中", "站点暂时关闭，服务恢复后即可继续访问，请稍后再试。"
		w.Header().Set("Retry-After", "300")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	var body bytes.Buffer
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(&body).Encode(map[string]any{"status": status, "error": title, "message": description, "requestId": w.Header().Get("X-Request-ID")})
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = statusPage.Execute(&body, struct {
			Code                          int
			Title, Description, RequestID string
			Maintenance                   bool
		}{status, title, description, w.Header().Get("X-Request-ID"), maintenance})
	}
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}
