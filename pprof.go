package middleware

import (
	"fmt"
	"net/http"
	"net/http/pprof"
)

// PprofHandler returns an http.Handler for default pprof endpoints at `/debug/pprof/`.
func PprofHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/pprof", pprof.Index)
	m.HandleFunc("/pprof/cmdline", pprof.Cmdline)
	m.HandleFunc("/pprof/profile", pprof.Profile)
	m.HandleFunc("/pprof/symbol", pprof.Symbol)
	m.HandleFunc("/pprof/trace", pprof.Trace)
	for _, extra := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		m.Handle(fmt.Sprintf("/pprof/%s", extra), pprof.Handler(extra))
	}
	return m
}
