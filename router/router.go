// Package router builds a chi router with a fixed middleware chain.
// It does not read the environment and it registers no routes.
package router

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rizkysr90/yourfutura-go-pkg/router/middleware"
	"github.com/rs/zerolog"
)

const (
	defaultRequestTimeout = 8 * time.Second
	defaultMaxBodyBytes   = 1024 * 1024
)

// Option configures a router.
type Option func(*settings)

type settings struct {
	logger        zerolog.Logger
	hasLogger     bool
	timeout       time.Duration
	maxBody       int64
	cors          *middleware.CORS
	skip          map[string]struct{}
	accessLogBody *middleware.AccessLogBody
}

// WithLogger sets the logger stored on each request. It is required.
func WithLogger(l zerolog.Logger) Option {
	return func(s *settings) {
		s.logger = l
		s.hasLogger = true
	}
}

// WithRequestTimeout sets how long a request may run. Zero keeps the default of 8s.
func WithRequestTimeout(d time.Duration) Option {
	return func(s *settings) {
		s.timeout = d
	}
}

// WithMaxBodyBytes sets the maximum request body size. Zero keeps the default of 1048576 bytes.
func WithMaxBodyBytes(n int64) Option {
	return func(s *settings) {
		s.maxBody = n
	}
}

// WithCORS enables CORS. Without this option no Access-Control-* headers are sent.
func WithCORS(c middleware.CORS) Option {
	return func(s *settings) {
		cp := c
		s.cors = &cp
	}
}

// WithAccessLogBody sets body logging from cfg. A nil cfg leaves body logging off.
// When Enable is true, each payload is copied only up to its own max.
// Both max fields must be greater than 0 when Enable is true.
func WithAccessLogBody(cfg *middleware.AccessLogBody) Option {
	return func(s *settings) {
		s.accessLogBody = cfg
	}
}

// WithAccessLogSkip replaces the exact URL paths that are not access-logged.
func WithAccessLogSkip(paths ...string) Option {
	return func(s *settings) {
		skip := make(map[string]struct{}, len(paths))
		for _, p := range paths {
			skip[p] = struct{}{}
		}
		s.skip = skip
	}
}

// New returns a router with the middleware chain installed.
// The caller registers application routes on the returned mux.
func New(opts ...Option) (*chi.Mux, error) {
	var s settings
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("nil option")
		}
		opt(&s)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}

	var corsMW func(http.Handler) http.Handler
	if s.cors != nil {
		mw, err := middleware.CORSMiddleware(*s.cors)
		if err != nil {
			return nil, err
		}
		corsMW = mw
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestIDMiddleware(s.logger))
	r.Use(middleware.AccessLog(s.skip, s.accessLogBody))
	r.Use(chimiddleware.Recoverer)
	if corsMW != nil {
		r.Use(corsMW)
	}
	r.Use(chimiddleware.Timeout(s.timeout))
	r.Use(middleware.BodyLimit(s.maxBody))
	return r, nil
}

func (s *settings) validate() error {
	if !s.hasLogger {
		return errors.New("missing logger: WithLogger is required")
	}
	if s.timeout < 0 {
		return fmt.Errorf("invalid request timeout %s: must be greater than or equal to 0", s.timeout)
	}
	if s.timeout == 0 {
		s.timeout = defaultRequestTimeout
	}
	if s.maxBody < 0 {
		return fmt.Errorf("invalid max body bytes %d: must be greater than or equal to 0", s.maxBody)
	}
	if s.maxBody == 0 {
		s.maxBody = defaultMaxBodyBytes
	}
	if s.accessLogBody == nil || !s.accessLogBody.Enable {
		return nil
	}
	if s.accessLogBody.RequestMaxBytes <= 0 {
		return fmt.Errorf("invalid access log request body limit %d: must be greater than 0 when body logging is enabled", s.accessLogBody.RequestMaxBytes)
	}
	if s.accessLogBody.ResponseMaxBytes <= 0 {
		return fmt.Errorf("invalid access log response body limit %d: must be greater than 0 when body logging is enabled", s.accessLogBody.ResponseMaxBytes)
	}
	return nil
}
