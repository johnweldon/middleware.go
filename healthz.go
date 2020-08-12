package middleware

import "net/http"

// HealthzHandler returns an http.Handler for the // `/healthz` endpoint and a
// debugging endpoint at `/healthz/toggle` // that will toggle the health report.
func HealthzHandler() http.Handler {
	h := &healthz{ok: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/toggle", h.handleToggle)
	mux.HandleFunc("/", h.handleCheck)
	return mux
}

type healthz struct {
	ok bool
}

func (h *healthz) handleCheck(w http.ResponseWriter, r *http.Request) {
	if !h.ok {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (h *healthz) handleToggle(w http.ResponseWriter, r *http.Request) {
	h.ok = !h.ok
	status := "good"
	if !h.ok {
		status = "bad"
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("status is: " + status))
}
