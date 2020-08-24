package middleware

import (
	"mime"
	"net/http"
	"strings"
)

func matchAny(t string, options ...string) bool {
	for _, opt := range options {
		if opt == t {
			return true
		}
	}

	return false
}

func hasContentType(h http.Header, mimetypes ...string) bool {
	ct := h.Get("Content-Type")
	if len(ct) == 0 {
		return matchAny("application/octet-stream", mimetypes...)
	}

	for _, p := range strings.Split(ct, ",") {
		mt, _, err := mime.ParseMediaType(p)
		if err != nil {
			continue
		}

		if matchAny(mt, mimetypes...) {
			return true
		}
	}

	return false
}
