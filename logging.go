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
	minimalRequestTemplateDef  = "  (request) {{ with requestid .Header }}[{{ . }}]{{ end }} {{.Host}} {{.Method}} {{.URL.Path}}\n"
	minimalResponseTemplateDef = " (response) {{ with requestid .Header }}[{{ . }}]{{ end }} {{.Code}} {{ status .Code }}\n"
	normalRequestTemplateDef   = minimalRequestTemplateDef + "{{ headers .Header }}\n"
	normalResponseTemplateDef  = minimalResponseTemplateDef + "{{ headers .Header }}\n"
	verboseRequestTemplateDef  = minimalRequestTemplateDef + `---------- BEGIN REQUEST ----------
{{ dump . }}
----------  END  REQUEST ----------
`
	verboseResponseTemplateDef = minimalResponseTemplateDef + `========== BEGIN RESPONSE ==========
{{ headers .Header }}
{{ if statusBad .Result.StatusCode }}{{ .Body.String }}{{ end }}
==========  END  RESPONSE ==========
`
	debugResponseTemplateDef = minimalResponseTemplateDef + `========== BEGIN RESPONSE ==========
{{ headers .Header }}
{{ .Body.String }}
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
	RedactedHeaders = []string{"Authorization"}
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
	return &RequestResponseLogger{Level: level, Writer: output}
}

// MinimalLogger returns a logger configured for minimal detail.
func MinimalLogger(output io.Writer) *RequestResponseLogger {
	return Logger(MinimalLevel, output)
}

// RegisterLevelChanger updates the logging level.
func (l *RequestResponseLogger) LevelHandler() http.Handler {
	m := http.NewServeMux()

	m.HandleFunc("/set", l.handleLevelChange)
	m.HandleFunc("/", l.handleGetLevel)

	return m
}

// RequestResponseLogger provides detailed HTTP request/response logging.
type RequestResponseLogger struct {
	Writer io.Writer
	Log    *log.Logger
	Level  DetailLevel
}

// nolint:interfacer
func (l *RequestResponseLogger) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	l.Handler(next).ServeHTTP(w, r)
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

// Wrap inserts the RequestResponseLogger into the middleware chain.
func (l *RequestResponseLogger) Handler(h http.Handler) http.Handler {
	l.initialize()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch l.Level {
		case NoneLevel:
			h.ServeHTTP(w, r)

			return
		case MinimalLevel, NormalLevel, VerboseLevel, DebugLevel:
			l.logRequest(r)

			rw, logResponse := l.responseLogger(w)
			defer logResponse()

			h.ServeHTTP(rw, r)
		}
	})
}

func (l *RequestResponseLogger) logRequest(r *http.Request) {
	if l.Level == NoneLevel {
		return
	}

	if t, ok := requestLevelTemplates[l.Level]; ok {
		if t == nil {
			return
		}

		if err := t.Execute(l.Writer, r); err != nil {
			l.Log.Printf("Error executing template %v: %v", l.Level, err)
		}
	} else {
		l.Log.Fatalf("Missing template for %v", l.Level)
	}
}

func (l *RequestResponseLogger) responseLogger(w http.ResponseWriter) (http.ResponseWriter, func()) {
	rw := httptest.NewRecorder()

	return rw, func() {
		resp := rw.Result()
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			l.Log.Fatalf("Error reading body: %v", err)
		}

		for k, v := range resp.Header {
			w.Header()[k] = v
		}

		w.WriteHeader(rw.Code)
		w.Write(body) // nolint:errcheck

		if t, ok := responseLevelTemplates[l.Level]; ok {
			if t == nil {
				return
			}

			if err := t.Execute(l.Writer, rw); err != nil {
				l.Log.Printf("Error executing template %v: %v", l.Level, err)
			}
		} else {
			l.Log.Fatalf("Missing template for %v", l.Level)
		}
	}
}

func (l *RequestResponseLogger) initialize() {
	if l.Writer == nil {
		l.Writer = os.Stdout
	}

	if l.Log == nil {
		l.Log = log.New(l.Writer, " [RW] ", log.LstdFlags)
	}
}
