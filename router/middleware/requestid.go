package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/rs/zerolog"
)

// HeaderRequestID is the request id header on the request and the response.
const HeaderRequestID = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = iota

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestID returns the raw request id stored by [RequestIDMiddleware], or "" if none.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// RequestIDMiddleware accepts a valid incoming X-Request-Id or generates one,
// sets the response header, and stores the logger and raw id on the request context.
func RequestIDMiddleware(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := requestIDFromHeader(r.Header.Get(HeaderRequestID))
			if id == "" {
				var err error
				id, err = newRequestID()
				if err != nil {
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
			}
			w.Header().Set(HeaderRequestID, id)

			l := base.With().Str("request_id", id).Logger()
			ctx := context.WithValue(l.WithContext(r.Context()), requestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requestIDFromHeader(v string) string {
	if requestIDPattern.MatchString(v) {
		return v
	}
	return ""
}

func newRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
