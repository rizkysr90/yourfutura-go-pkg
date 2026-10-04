package apperror

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Decode reads one JSON value from r.Body into dst.
// maxBytes is the most the body may contain. A larger body returns 413.
// Unknown fields are rejected. Decode does not write a response.
func Decode(r *http.Request, maxBytes int64, dst any) error {
	if r == nil {
		return errors.New("nil request")
	}
	if dst == nil {
		return errors.New("nil decode destination")
	}
	if maxBytes <= 0 {
		return fmt.Errorf("invalid body limit %d: must be greater than 0", maxBytes)
	}
	if r.Body == nil || r.Body == http.NoBody {
		return invalidBody("request body is empty")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if dec.More() {
		return invalidBody("request body must contain a single JSON value")
	}
	return nil
}

func invalidBody(message string) *Error {
	return New(http.StatusBadRequest, CodeInvalidBody, message)
}

func decodeError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return New(http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "request body is too large")
	}
	if errors.Is(err, io.EOF) {
		return invalidBody("request body is empty")
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return invalidBody("invalid json: unexpected end of input")
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return invalidBody(fmt.Sprintf("invalid json at position %d", syntax.Offset))
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field != "" {
			return invalidBody(fmt.Sprintf("field %q has the wrong type", typeErr.Field))
		}
		return invalidBody("request body has the wrong type")
	}
	if field, ok := unknownField(err); ok {
		return invalidBody(fmt.Sprintf("unknown field %q", field))
	}
	return invalidBody("invalid json")
}

func unknownField(err error) (string, bool) {
	const prefix = "json: unknown field "
	rest, ok := strings.CutPrefix(err.Error(), prefix)
	if !ok {
		return "", false
	}
	field, err := strconv.Unquote(rest)
	if err != nil {
		return "", false
	}
	return field, true
}
