package middleware

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

// AccessLogBody controls copying request and response bodies onto the access log.
// Enable is the flag. RequestMaxBytes and ResponseMaxBytes are separate limits.
// A payload larger than its own limit is omitted.
type AccessLogBody struct {
	Enable           bool
	RequestMaxBytes  int64
	ResponseMaxBytes int64
}

// AccessLog writes one "http request" line after next returns.
// skip lists exact URL paths that are not logged.
// A nil body or Enable false leaves body logging off. Each max is independent.
// A payload larger than its own limit is omitted.
func AccessLog(skip map[string]struct{}, body *AccessLogBody) func(http.Handler) http.Handler {
	reqMax, resMax := int64(0), int64(0)
	if body != nil && body.Enable {
		reqMax, resMax = body.RequestMaxBytes, body.ResponseMaxBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			lg := zerolog.Ctx(r.Context())
			reqBody, logReqBody := peekRequestBody(r, reqMax)

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			var resBody cappedBody
			if resMax > 0 {
				resBody.max = resMax
				ww.Tee(&resBody)
			}
			start := time.Now()
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil {
				if pattern := rc.RoutePattern(); pattern != "" {
					route = pattern
				}
			}

			// 5xx is error, 4xx is warn, anything else is info.
			// A logger built at warn drops the info line. A logger built at
			// error drops info and warn. The level comes from the logger the
			// app passed in, not from this package.
			var ev *zerolog.Event
			switch {
			case status >= 500:
				ev = lg.Error()
			case status >= 400:
				ev = lg.Warn()
			default:
				ev = lg.Info()
			}
			if ev == nil {
				return
			}
			ev = ev.Str("method", r.Method).
				Str("route", route).
				Str("path", r.URL.Path).
				Int("status", status).
				Int64("duration_ms", time.Since(start).Milliseconds())
			if logReqBody {
				ev = ev.Str("req_body", reqBody)
			}
			if text, ok := resBody.text(); ok {
				ev = ev.Str("res_body", text)
			}
			ev.Msg("http request")
		})
	}
}

// peekRequestBody reads at most max+1 bytes. The body is put back for the handler.
// The bool is false when body logging is off, the body is empty, or it is larger than max.
func peekRequestBody(r *http.Request, max int64) (string, bool) {
	if max <= 0 || r.Body == nil || r.Body == http.NoBody || r.ContentLength > max {
		return "", false
	}
	buf := make([]byte, max+1)
	n, err := io.ReadFull(r.Body, buf)
	orig := r.Body
	switch err {
	case io.EOF:
		// body was empty
		r.Body = bodyCloser{Reader: bytes.NewReader(nil), Closer: orig}
		return "", false
	case io.ErrUnexpectedEOF:
		// body fit in the buffer
		r.Body = bodyCloser{Reader: bytes.NewReader(buf[:n]), Closer: orig}
		if n == 0 {
			return "", false
		}
		return string(buf[:n]), true
	default:
		// buffer filled or the read failed
		r.Body = bodyCloser{Reader: io.MultiReader(bytes.NewReader(buf[:n]), orig), Closer: orig}
		return "", false
	}
}

type bodyCloser struct {
	io.Reader
	io.Closer
}

// cappedBody stores a response body only while it stays within max.
type cappedBody struct {
	buf  bytes.Buffer
	max  int64
	n    int64
	over bool
}

func (c *cappedBody) Write(p []byte) (int, error) {
	if !c.over {
		c.n += int64(len(p))
		if c.n > c.max {
			c.over = true
			c.buf.Reset()
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBody) text() (string, bool) {
	if c == nil || c.max <= 0 || c.over || c.buf.Len() == 0 {
		return "", false
	}
	return c.buf.String(), true
}
