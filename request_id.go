package middleware

import (
	"context"
	"net/http"
)

type requestIDKey string

const (
	requestIDKeyName requestIDKey = "x-request-id-key"
	xRequestIDKey    string       = "X-Request-ID"
)

// WithRequestID adds a value for X-Request-ID into the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKeyName, requestID)
}

// GetRequestID returns the X-Request-ID from the context and true if it exists.
func GetRequestID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKeyName).(string)

	return id, ok
}

// NewRequestIDHandler returns a handler that can inject X-Request-ID
// header, or re-use an existing one.
func NewRequestIDHandler(generator func() string) *RequestIDHandler {
	return &RequestIDHandler{generator: generator}
}

// RequestIDHandler is the handler responsible for X-Request-ID management.
type RequestIDHandler struct {
	generator func() string
}

// Handler implements the middleware interface.
func (h *RequestIDHandler) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id string
		if id = r.Header.Get(xRequestIDKey); len(id) == 0 {
			id = h.generator()
			r.Header.Set(xRequestIDKey, id)
		}
		w.Header().Add("Trailer", xRequestIDKey)
		defer w.Header().Set(xRequestIDKey, id)

		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

func (h *RequestIDHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.Handler) {
	h.Handler(next).ServeHTTP(w, r)
}
