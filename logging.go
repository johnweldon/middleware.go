package middleware

import (
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
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
	normalRequestTemplateDef   =  minimalRequestTemplateDef + `>>>
{{ dump . }}
<<<
`
	normalResponseTemplateDef =  minimalResponseTemplateDef + `==>
{{- range $k, $v := .HeaderMap}}
{{ $k }}: {{ $v }}{{ end }}

{{ .Body.String -}}
<==
`
)

var (
	requestLevelTemplates = map[DetailLevel]*template.Template{
		MinimalLevel: parseTemplate(minimalRequestTemplateDef),
		NormalLevel:  parseTemplate(normalRequestTemplateDef),
	}
	responseLevelTemplates = map[DetailLevel]*template.Template{
		MinimalLevel: parseTemplate(minimalResponseTemplateDef),
		NormalLevel:  parseTemplate(normalResponseTemplateDef),
	}
)

func parseTemplate(def string) *template.Template {
	fnMap := map[string]interface{}{
		"status": http.StatusText,
		"dump": func(r *http.Request) string {
			b, err := httputil.DumpRequest(r, true)
			if err != nil {
				return err.Error()
			}
			return string(b)
		},
	}
	return template.Must(template.New("minimalRequest").Funcs(fnMap).Parse(def))
}

func Logger(level DetailLevel, output io.Writer) *RequestResponseLogger {
	return &RequestResponseLogger{Level: level, Writer: output}
}

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
