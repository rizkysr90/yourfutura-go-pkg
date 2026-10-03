package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/cors"
)

const defaultCORSMaxAge = 5 * time.Minute

// CORS is the cross-origin policy installed by the router WithCORS option.
// Empty Methods, Headers, ExposedHeaders, and MaxAge keep their defaults.
type CORS struct {
	Origins          []string
	Methods          []string
	Headers          []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// CORSMiddleware validates c and returns CORS middleware.
// Origins must be non-empty. An invalid policy returns an error and no middleware.
func CORSMiddleware(c CORS) (func(http.Handler) http.Handler, error) {
	if len(c.Origins) == 0 {
		return nil, errors.New("invalid CORS origins: must not be empty")
	}
	if c.MaxAge < 0 {
		return nil, fmt.Errorf("invalid CORS max age %s: must be greater than or equal to 0", c.MaxAge)
	}
	for _, origin := range c.Origins {
		if err := validateOrigin(origin); err != nil {
			return nil, err
		}
	}
	if c.AllowCredentials {
		if slices.Contains(c.Origins, "*") {
			return nil, errors.New(`invalid CORS origins: "*" cannot be used with credentials`)
		}
	}
	if len(c.Methods) == 0 {
		c.Methods = []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodHead,
		}
	}
	if len(c.Headers) == 0 {
		c.Headers = []string{"Accept", "Authorization", "Content-Type", HeaderRequestID}
	}
	if len(c.ExposedHeaders) == 0 {
		c.ExposedHeaders = []string{HeaderRequestID}
	}
	if c.MaxAge == 0 {
		c.MaxAge = defaultCORSMaxAge
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   c.Origins,
		AllowedMethods:   c.Methods,
		AllowedHeaders:   c.Headers,
		ExposedHeaders:   c.ExposedHeaders,
		AllowCredentials: c.AllowCredentials,
		MaxAge:           int(c.MaxAge / time.Second),
	}), nil
}

func validateOrigin(origin string) error {
	const msg = `invalid CORS origin %q: must be "*" or scheme://host[:port] with scheme http or https and no path, query, fragment, or userinfo`
	if origin == "*" {
		return nil
	}
	if strings.Contains(origin, "*") {
		return fmt.Errorf("invalid CORS origin %q: wildcard patterns are not allowed", origin)
	}
	u, err := url.Parse(origin)
	if err != nil ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.Host == "" ||
		u.User != nil ||
		u.Path != "" ||
		u.RawQuery != "" ||
		u.Fragment != "" ||
		u.String() != origin {
		return fmt.Errorf(msg, origin)
	}
	return nil
}
