package middleware

import "net/http"

// RegisterHealthz registers a very simple Health checker at the
// `/healthz` endpoint and a debugging endpoint at `/healthz/toggle`
// that will toggle the health report
func RegisterHealthz(m *http.ServeMux) {
	h := &healthz{ok: true}
	m.HandleFunc("/healthz", h.handleCheck)
	m.HandleFunc("/healthz/toggle", h.handleToggle)
}

type healthz struct {
	ok bool
}

func (h *healthz) stop() {
	h.ok = false
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
