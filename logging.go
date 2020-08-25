package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"strings"
	"text/template"
)

// DetailLevel type.
type DetailLevel int

// DetailLevel options.
const (
	NoneLevel DetailLevel = iota
	MinimalLevel
	NormalLevel
	VerboseLevel
	DebugLevel
)

// nolint:lll
const (
	minimalRequestTemplateDef  = "  (request) {{ with .requestid }}[{{ . }}] {{ end }}{{ .request.Host }} {{ .request.Method }} {{ .request.URL.Path }}\n"
	minimalResponseTemplateDef = " (response) {{ with .requestid }}[{{ . }}] {{ end }}{{ .response.StatusCode }} {{ status .response.StatusCode }}\n"
	normalRequestTemplateDef   = minimalRequestTemplateDef + "{{ headers .request.Header }}\n"
	normalResponseTemplateDef  = minimalResponseTemplateDef + "{{ headers .response.Header }}\n"
	verboseRequestTemplateDef  = minimalRequestTemplateDef + `---------- BEGIN REQUEST ----------
{{ dump .request }}
----------  END  REQUEST ----------
`
	verboseResponseTemplateDef = minimalResponseTemplateDef + `========== BEGIN RESPONSE ==========
{{ headers .response.Header }}
{{ if statusBad .response.StatusCode }}{{ .body }}{{ end }}
==========  END  RESPONSE ==========
`
	debugResponseTemplateDef = minimalResponseTemplateDef + `========== BEGIN RESPONSE ==========
{{ headers .response.Header }}
{{ .body }}
==========  END  RESPONSE ==========
`
)

// nolint:gochecknoglobals
var (
	details = map[DetailLevel]string{
		NoneLevel:    "none",
		MinimalLevel: "minimal",
		NormalLevel:  "normal",
		VerboseLevel: "verbose",
		DebugLevel:   "debug",
	}
	levels = map[string]DetailLevel{
		"none":    NoneLevel,
		"minimal": MinimalLevel,
		"normal":  NormalLevel,
		"verbose": VerboseLevel,
		"debug":   DebugLevel,
	}
	requestLevelTemplates = map[DetailLevel]*template.Template{
		NoneLevel:    nil,
		MinimalLevel: parseTemplate(MinimalLevel, minimalRequestTemplateDef),
		NormalLevel:  parseTemplate(NormalLevel, normalRequestTemplateDef),
		VerboseLevel: parseTemplate(VerboseLevel, verboseRequestTemplateDef),
		DebugLevel:   parseTemplate(DebugLevel, verboseRequestTemplateDef),
	}
	responseLevelTemplates = map[DetailLevel]*template.Template{
		NoneLevel:    nil,
		MinimalLevel: parseTemplate(MinimalLevel, minimalResponseTemplateDef),
		NormalLevel:  parseTemplate(NormalLevel, normalResponseTemplateDef),
		VerboseLevel: parseTemplate(VerboseLevel, verboseResponseTemplateDef),
		DebugLevel:   parseTemplate(DebugLevel, debugResponseTemplateDef),
	}

	// RedactedHeaders are the list of headers that are normally redacted.
	RedactedHeaders = []string{"Authorization", "Cookie"}
	redactHeaders   = map[DetailLevel][]string{
		NoneLevel:    RedactedHeaders,
		MinimalLevel: RedactedHeaders,
		NormalLevel:  RedactedHeaders,
		VerboseLevel: RedactedHeaders,
		DebugLevel:   {},
	}
)

// LevelText returns the detail level for the given name.
func LevelText(level DetailLevel) string {
	if l, ok := details[level]; ok {
		return l
	}

	return ""
}

// TextLevel returns the detail level for the given name.
func TextLevel(level string) DetailLevel {
	if l, ok := levels[strings.ToLower(level)]; ok {
		return l
	}

	return NoneLevel
}

func parseTemplate(level DetailLevel, def string) *template.Template {
	name := details[level]

	redactedHeaders := func(h http.Header) (orig, redacted http.Header) {
		orig = h.Clone()

		for _, k := range redactHeaders[level] {
			if _, ok := h[k]; ok {
				h[k] = []string{"[redacted]"}
			}
		}

		redacted = h

		return
	}

	fnMap := map[string]interface{}{
		"status": http.StatusText,
		"headers": func(h http.Header) string {
			var buf bytes.Buffer
			_, red := redactedHeaders(h)
			for k, v := range red {
				fmt.Fprintf(&buf, "%s: %s\n", k, strings.Join(v, ","))
			}

			return buf.String()
		},
		"requestid": func(h http.Header) string { return h.Get(xRequestIDKey) },
		"dump": func(r *http.Request) string {
			orig, redacted := redactedHeaders(r.Header)
			r.Header = redacted
			b, err := httputil.DumpRequest(r, true)
			r.Header = orig
			if err != nil {
				return err.Error()
			}

			return string(b)
		},
		"statusGood": func(code int) bool { return http.StatusOK <= code && code < http.StatusBadRequest },
		"statusBad":  func(code int) bool { return http.StatusBadRequest <= code },
	}

	return template.Must(template.New(name).Funcs(fnMap).Parse(def))
}

// Logger returns a logger configured with the given level and output.
func Logger(level DetailLevel, output io.Writer) *RequestResponseLogger {
	return &RequestResponseLogger{coreLogger{Level: level, Writer: output}}
}

// MinimalLogger returns a logger configured for minimal detail.
func MinimalLogger(output io.Writer) *RequestResponseLogger {
	return Logger(MinimalLevel, output)
}

// RequestResponseLogger provides detailed HTTP request/response logging.
type RequestResponseLogger struct {
	coreLogger
}

// nolint:interfacer
func (l *RequestResponseLogger) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	l.Handler(next).ServeHTTP(w, r)
}

// LevelHandler updates the logging level.
func (l *RequestResponseLogger) LevelHandler() http.Handler {
	m := http.NewServeMux()

	m.HandleFunc("/set", l.handleLevelChange)
	m.HandleFunc("/", l.handleGetLevel)

	return m
}

// Handler inserts the RequestResponseLogger into the middleware chain.
func (l *RequestResponseLogger) Handler(h http.Handler) http.Handler {
	l.initialize()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := GetRequestID(r.Context())
		switch l.Level {
		case NoneLevel:
			h.ServeHTTP(w, r)

			return
		case MinimalLevel, NormalLevel, VerboseLevel, DebugLevel:
			r := l.logRequest(r, id)

			rw, logResponse := l.responseLogger(w, id)
			defer logResponse()

			h.ServeHTTP(rw, r)
		}
	})
}

func (l *RequestResponseLogger) handleGetLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	res := &struct {
		Level string `json:"level"`
	}{Level: LevelText(l.Level)}

	if err := json.NewEncoder(w).Encode(res); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}
}

func (l *RequestResponseLogger) handleLevelChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)

		return
	}

	if !hasContentType(r.Header, "application/json") {
		w.Header().Set("Accept", "application/json")
		http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)

		return
	}

	req := &struct {
		Level string `json:"level"`
	}{Level: LevelText(l.Level)}

	defer r.Body.Close()

	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		http.Error(w, `expect JSON body like: {"level":"none|minimal|normal|verbose|debug"}`, http.StatusUnprocessableEntity)

		return
	}

	newLevel, ok := levels[req.Level]
	if !ok {
		http.Error(w, `expect JSON body like: {"level":"none|minimal|normal|verbose|debug"}`, http.StatusUnprocessableEntity)

		return
	}

	switch newLevel {
	case l.Level:
		w.WriteHeader(http.StatusAlreadyReported)

		return
	case NoneLevel, MinimalLevel, NormalLevel, VerboseLevel, DebugLevel:
		l.Level = newLevel

		w.WriteHeader(http.StatusAccepted)

		return
	default:
		http.Error(w, `expect JSON body like: {"level":"none|minimal|normal|verbose|debug"}`, http.StatusUnprocessableEntity)

		return
	}
}

func (l *RequestResponseLogger) responseLogger(w http.ResponseWriter, id string) (http.ResponseWriter, func()) {
	rw := httptest.NewRecorder()

	return rw, func() {
		resp := rw.Result()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			l.Log.Printf("Error reading body: %v", err)
		}

		if err = resp.Body.Close(); err != nil {
			l.Log.Printf("Error closing body: %v", err)
		}

		resp.Body = ioutil.NopCloser(bytes.NewReader(body))
		defer resp.Body.Close()

		for k, v := range resp.Header {
			w.Header()[k] = v
		}

		w.WriteHeader(rw.Code)
		w.Write(body) // nolint:errcheck

		// nolint:bodyclose
		l.logResponse(rw.Result(), id)
	}
}

// NewRoundTripLogger returns an http.RoundTripper that logs requests and responses.
// nolint:lll
func NewRoundTripLogger(inner http.RoundTripper, level DetailLevel, out io.Writer, logger *log.Logger) *RoundTripLogger {
	l := &RoundTripLogger{
		coreLogger: coreLogger{
			Level:  level,
			Log:    logger,
			Writer: out,
		},
		inner: inner,
	}
	l.initialize()

	return l
}

type RoundTripLogger struct {
	coreLogger
	inner http.RoundTripper
}

func (l *RoundTripLogger) RoundTrip(r *http.Request) (*http.Response, error) {
	id, _ := GetRequestID(r.Context())

	l.logRequest(r, id)

	resp, err := l.inner.RoundTrip(r)
	if err != nil {
		return nil, err
	}

	l.logResponse(resp, id)

	return resp, nil
}

type coreLogger struct {
	Level  DetailLevel
	Log    *log.Logger
	Writer io.Writer
}

func (l *coreLogger) logRequest(r *http.Request, id string) *http.Request {
	if l.Level == NoneLevel {
		return r
	}

	t, ok := requestLevelTemplates[l.Level]
	if !ok {
		l.Log.Printf("Error missing request template for %v", l.Level)

		return r
	}

	if t == nil {
		return r
	}

	var (
		err  error
		body = []byte("<nil>")
	)

	if r.Body != nil {
		body, err = ioutil.ReadAll(r.Body)
		if err != nil {
			l.Log.Printf("Error reading body: %v", err)

			return r
		}

		if err = r.Body.Close(); err != nil {
			l.Log.Printf("Error closing body: %v", err)

			return r
		}

		r.Body = ioutil.NopCloser(bytes.NewReader(body))
	}

	data := map[string]interface{}{
		"request":   r,
		"requestid": id,
		"body":      body,
	}

	if err := t.Execute(l.Writer, data); err != nil {
		l.Log.Printf("Error executing template %v: %v", l.Level, err)
	}

	return r
}

func (l *coreLogger) logResponse(r *http.Response, id string) {
	t, ok := responseLevelTemplates[l.Level]
	if !ok {
		l.Log.Printf("Error missing response template for %v", l.Level)

		return
	}

	if t == nil {
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		l.Log.Printf("Error reading body: %v", err)

		return
	}

	if err = r.Body.Close(); err != nil {
		l.Log.Printf("Error closing body: %v", err)

		return
	}

	data := map[string]interface{}{
		"response":  r,
		"requestid": id,
		"body":      string(body),
	}

	if err := t.Execute(l.Writer, data); err != nil {
		l.Log.Printf("Error executing template %v: %v", l.Level, err)
	}

	r.Body = ioutil.NopCloser(bytes.NewReader(body))
}

func (l *coreLogger) initialize() {
	if l.Log == nil {
		l.Log = log.New(l.Writer, " [request/response logger] ", log.LstdFlags)
	}

	if l.Writer == nil {
		l.Writer = os.Stdout
	}
}
