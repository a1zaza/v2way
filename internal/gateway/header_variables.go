package gateway

import (
	"net"
	"net/http"
	"strconv"

	"v2way/internal/accessgate"
	"v2way/internal/headertemplate"
)

type routeHeaderTemplates struct {
	request, response headertemplate.Headers
}

func headerContext(r *http.Request, requestID, siteID, routeID string) headertemplate.Context {
	ip, _ := accessgate.SourceIP(r.RemoteAddr)
	_, port, _ := net.SplitHostPort(r.RemoteAddr)
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		port = ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return headertemplate.Context{
		"remote_addr": ip, "remote_port": port, "scheme": scheme,
		"host": host(r.Host), "http_host": r.Host, "request_method": r.Method,
		"uri": r.URL.EscapedPath(), "request_id": requestID, "site_id": siteID, "route_id": routeID,
	}
}
