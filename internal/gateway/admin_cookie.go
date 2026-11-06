package gateway

import (
	"net/http"
	"strings"

	"v2way/internal/sessioncookie"
)

func cookieName(value string) string {
	name, _, _ := strings.Cut(value, "=")
	return strings.TrimSpace(name)
}

func stripAdminRequestCookie(header http.Header) {
	values := header.Values("Cookie")
	header.Del("Cookie")
	for _, value := range values {
		parts := strings.Split(value, ";")
		kept := parts[:0]
		for _, part := range parts {
			if cookieName(part) != sessioncookie.Name {
				kept = append(kept, part)
			}
		}
		if len(kept) > 0 {
			header.Add("Cookie", strings.Join(kept, ";"))
		}
	}
}

func stripAdminResponseCookie(header http.Header) {
	values := header.Values("Set-Cookie")
	header.Del("Set-Cookie")
	for _, value := range values {
		if cookieName(strings.SplitN(value, ";", 2)[0]) != sessioncookie.Name {
			header.Add("Set-Cookie", value)
		}
	}
}
