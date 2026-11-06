package admin

import (
	"net/http"
	"time"
)

// HTTPServer bounds header and body reads on the short-request management API.
// Business listeners retain their separate streaming and upload policies.
func HTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}
