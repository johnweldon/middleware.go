package middleware

import (
	"bytes"
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

// DetailLevel type
type DetailLevel int

// DetailLevel options
const (
	NoneLevel DetailLevel = iota
	MinimalLevel
	NormalLevel
	VerboseLevel
	DebugLevel
)

const (
	minimalRequestTemplateDef  = "  (request) {{.Host}} {{.Method}} {{.URL.Path}}\n"
	minimalResponseTemplateDef = " (response) {{.Code}} {{ status .Code }}\n"
	normalRequestTemplateDef   = "  (request) {{.Host}} {{.Method}} {{.URL.Path}}\n{{ headers .Header }}\n"
	normalResponseTemplateDef  = " (response) {{.Code}} {{ status .Code }}\n{{ headers .Header }}\n"
	verboseRequestTemplateDef  = minimalRequestTemplateDef + `---------- BEGIN REQUEST ----------
{{ dump . }}
----------  END  REQUEST ----------
`
	verboseResponseTemplateDef = minimalResponseTemplateDef + `========== BEGIN RESPONSE ==========
{{ headers .Header }}
{{ .Body.String }}
==========  END  RESPONSE ==========
`
)

var (
	details = map[DetailLevel]string{
		MinimalLevel: "minimal",
		NormalLevel:  "normal",
		VerboseLevel: "verbose",
		DebugLevel:   "debug",
	}
	levels = map[string]DetailLevel{
		"minimal": MinimalLevel,
		"normal":  NormalLevel,
		"verbose": VerboseLevel,
		"debug":   DebugLevel,
	}
	requestLevelTemplates = map[DetailLevel]*template.Template{
		MinimalLevel: parseTemplate(MinimalLevel, minimalRequestTemplateDef),
		NormalLevel:  parseTemplate(NormalLevel, normalRequestTemplateDef),
		VerboseLevel: parseTemplate(VerboseLevel, verboseRequestTemplateDef),
		DebugLevel:   parseTemplate(DebugLevel, verboseRequestTemplateDef),
	}
	responseLevelTemplates = map[DetailLevel]*template.Template{
		MinimalLevel: parseTemplate(MinimalLevel, minimalResponseTemplateDef),
		NormalLevel:  parseTemplate(NormalLevel, normalResponseTemplateDef),
		VerboseLevel: parseTemplate(VerboseLevel, verboseResponseTemplateDef),
		DebugLevel:   parseTemplate(DebugLevel, verboseResponseTemplateDef),
	}

	// RedactedHeaders are the list of headers that are normally redacted
	RedactedHeaders = []string{"Authorization"}
	redactHeaders   = map[DetailLevel][]string{
		MinimalLevel: RedactedHeaders,
		NormalLevel:  RedactedHeaders,
		VerboseLevel: RedactedHeaders,
		DebugLevel:   []string{},
	}
)

// LevelText returns the detail level for the given name
func LevelText(level string) DetailLevel {
	if l, ok := levels[strings.ToLower(level)]; ok {
		return l
	}
	return MinimalLevel
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
	}
	return template.Must(template.New(name).Funcs(fnMap).Parse(def))
}

// Logger returns a logger configured with the given level and output
func Logger(level DetailLevel, output io.Writer) *RequestResponseLogger {
	return &RequestResponseLogger{Level: level, Writer: output}
}

// MinimalLogger returns a logger configured for minimal detail
func MinimalLogger(output io.Writer) *RequestResponseLogger { return Logger(MinimalLevel, output) }

// RequestResponseLogger provides detailed HTTP request/response logging.
type RequestResponseLogger struct {
	Writer io.Writer
	Log    *log.Logger
	Level  DetailLevel
}

func (l *RequestResponseLogger) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	l.Wrap(next).ServeHTTP(w, r)
}

// Wrap inserts the RequestResponseLogger into the middleware chain.
func (l *RequestResponseLogger) Wrap(h http.Handler) http.Handler {
	l.initialize()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch l.Level {
		case NoneLevel:
			h.ServeHTTP(w, r)
			return
		default:
			l.logRequest(r)
			rw, logResponse := l.responseLogger(w)
			defer logResponse()
			h.ServeHTTP(rw, r)
		}
	})
}

func (l *RequestResponseLogger) logRequest(r *http.Request) {
	if t, ok := requestLevelTemplates[l.Level]; ok {
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
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			l.Log.Fatalf("Error reading body: %v", err)
		}

		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(rw.Code)
		w.Write(body)

		if t, ok := responseLevelTemplates[l.Level]; ok {
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
