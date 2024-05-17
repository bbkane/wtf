package http

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/benbjohnson/wtf"
	"github.com/benbjohnson/wtf/http/html"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Generic HTTP metrics.
var (
	errorCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wtf_http_error_count",
		Help: "Total number of errors by error code",
	}, []string{"code"})
)

// Client represents an HTTP client.
type Client struct {
	URL string
}

// NewClient returns a new instance of Client.
func NewClient(u string) *Client {
	return &Client{URL: u}
}

// SessionCookieName is the name of the cookie used to store the session.
const SessionCookieName = "session"

// Session represents session data stored in a secure cookie.
type Session struct {
	UserID      int    `json:"userID"`
	RedirectURL string `json:"redirectURL"`
	State       string `json:"state"`
}

// SetFlash sets the flash cookie for the next request to read.
func SetFlash(w http.ResponseWriter, s string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    s,
		Path:     "/",
		HttpOnly: true,
	})
}

// Error prints & optionally logs an error message.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	// Extract error code & message.
	code, message := wtf.ErrorCode(err), wtf.ErrorMessage(err)

	// Track metrics by code.
	errorCount.WithLabelValues(code).Inc()

	// Log & report internal errors.
	if code == wtf.EINTERNAL {
		wtf.ReportError(r.Context(), err, r)
		LogError(r, err)
	}

	// Print user message to response based on reqeust accept header.
	switch r.Header.Get("Accept") {
	case "application/json":
		w.Header().Set("Content-type", "application/json")
		w.WriteHeader(ErrorStatusCode(code))
		json.NewEncoder(w).Encode(&ErrorResponse{Error: message})

	default:
		w.WriteHeader(ErrorStatusCode(code))
		tmpl := html.ErrorTemplate{
			StatusCode: ErrorStatusCode(code),
			Header:     "An error has occurred.",
			Message:    message,
		}
		tmpl.Render(r.Context(), w)
	}
}

// ErrorResponse represents a JSON structure for error output.
type ErrorResponse struct {
	Error string `json:"error"`
}

// LogError logs an error with the HTTP route information.
func LogError(r *http.Request, err error) {
	log.Printf("[http] error: %s %s: %s", r.Method, r.URL.Path, err)
}

// lookup of application error codes to HTTP status codes.
var codes = map[string]int{
	wtf.ECONFLICT:       http.StatusConflict,
	wtf.EINVALID:        http.StatusBadRequest,
	wtf.ENOTFOUND:       http.StatusNotFound,
	wtf.ENOTIMPLEMENTED: http.StatusNotImplemented,
	wtf.EUNAUTHORIZED:   http.StatusUnauthorized,
	wtf.EINTERNAL:       http.StatusInternalServerError,
}

// ErrorStatusCode returns the associated HTTP status code for a WTF error code.
func ErrorStatusCode(code string) int {
	if v, ok := codes[code]; ok {
		return v
	}
	return http.StatusInternalServerError
}

// FromErrorStatusCode returns the associated WTF code for an HTTP status code.
func FromErrorStatusCode(code int) string {
	for k, v := range codes {
		if v == code {
			return k
		}
	}
	return wtf.EINTERNAL
}
