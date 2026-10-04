package apperror

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rizkysr90/yourfutura-go-pkg/router/middleware"
)

type errorBody struct {
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	RequestID string  `json:"request_id"`
	Fields    []Field `json:"fields,omitempty"`
}

// Write sends v as JSON with the given status.
// Status 204 and 304 are sent with no body.
// Write returns an error when encoding fails. The status is already sent by then.
func Write(w http.ResponseWriter, status int, v any) error {
	if status == http.StatusNoContent || status == http.StatusNotModified {
		return writeJSON(w, status, nil)
	}
	return writeJSON(w, status, v)
}

// WriteError sends the JSON error for err.
// An *Error with a 4xx or 5xx status, a code, and a message is written as given.
// Any other error is 500 with code "internal" and message "internal error".
// The wrapped cause is not included. request_id comes from the request context.
func WriteError(w http.ResponseWriter, r *http.Request, err error) error {
	status := http.StatusInternalServerError
	body := errorBody{Code: CodeInternal, Message: "internal error"}
	var ae *Error
	if errors.As(err, &ae) {
		if st, public, ok := ae.public(); ok {
			status = st
			body = public
		}
	}
	if r != nil {
		body.RequestID = middleware.RequestID(r.Context())
	}
	return writeJSON(w, status, body)
}

// NotFound writes a JSON 404. Register it with chi.Mux.NotFound.
func NotFound(w http.ResponseWriter, r *http.Request) {
	_ = WriteError(w, r, New(http.StatusNotFound, CodeNotFound, "not found"))
}

// MethodNotAllowed writes a JSON 405. Register it with chi.Mux.MethodNotAllowed.
// Chi does not pass the allowed methods into a custom handler, so this response has no Allow header.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	_ = WriteError(w, r, New(http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed"))
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	if w == nil {
		return errors.New("nil response writer")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
