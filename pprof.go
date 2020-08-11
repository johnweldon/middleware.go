package middleware

import (
	"fmt"
	"net/http"
	"net/http/pprof"
)

// RegisterPprof registers default pprof endpoints at `/debug/pprof/`
func RegisterPprof(m *http.ServeMux) {
	m.HandleFunc("/debug/pprof/", pprof.Index)
	m.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	m.HandleFunc("/debug/pprof/profile", pprof.Profile)
	m.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	m.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, extra := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		m.Handle(fmt.Sprintf("/debug/pprof/%s", extra), pprof.Handler(extra))
	}
}
