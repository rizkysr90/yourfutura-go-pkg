// Package apperror maps handler failures to one JSON shape and reads JSON bodies.
// It does not read the environment and it does not log.
package apperror

const (
	// CodeInvalidBody is a request body the server refused to decode.
	CodeInvalidBody = "invalid_body"
	// CodeBodyTooLarge is a request body over the decode limit.
	CodeBodyTooLarge = "body_too_large"
	// CodeNotFound is a path with no route.
	CodeNotFound = "not_found"
	// CodeMethodNotAllowed is a path whose method has no route.
	CodeMethodNotAllowed = "method_not_allowed"
	// CodeInternal is a failure that is not safe to describe to the client.
	CodeInternal = "internal"
)

// Field is one client-safe validation failure.
type Field struct {
	Name    string `json:"field"`
	Message string `json:"message"`
}

// Error is a client-safe HTTP error.
// Status, Code, Message, and Fields are written to the response.
// The wrapped error is for logs and is left out of the JSON.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  []Field
	err     error
}

// New returns an error with no wrapped cause.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Wrap returns an error that keeps err for logs.
func Wrap(err error, status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message, err: err}
}

// Error returns the client message, followed by the wrapped cause when one is set.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.err == nil {
		return e.Message
	}
	if e.Message == "" {
		return e.err.Error()
	}
	return e.Message + ": " + e.err.Error()
}

// Unwrap returns the wrapped cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *Error) public() (status int, body errorBody, ok bool) {
	if e == nil || e.Status < 400 || e.Status > 599 || e.Code == "" || e.Message == "" {
		return 0, errorBody{}, false
	}
	return e.Status, errorBody{
		Code:    e.Code,
		Message: e.Message,
		Fields:  e.Fields,
	}, true
}
