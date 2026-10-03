# router

Chi router with a fixed middleware chain. `New` returns an `http.Handler`. The application registers its own routes. This package registers none, including health and readiness. Those endpoints are a planned addition.

This package does not read environment variables and does not import a config package. The caller passes values through options. A starter project can parse its own config from the environment and map the fields in.

## Options

| Option | Default |
| --- | --- |
| `WithLogger` | required; `New` fails if it is missing |
| `WithRequestTimeout` | 8s. Zero keeps the default. Negative is an error. |
| `WithMaxBodyBytes` | 1048576 bytes. Zero keeps the default. Negative is an error. |
| `WithCORS` | CORS is off until this option is passed |
| `WithAccessLogSkip` | empty. Passing the option replaces the list |
| `WithAccessLogBody` | off. `middleware.AccessLogBody.Enable` turns it on. `RequestMaxBytes` and `ResponseMaxBytes` are separate limits. A larger payload is omitted. `Enable` with either max at 0 or less is an error. |

A nil option makes `New` return an error.

## Middleware order

The handlers live in `router/middleware`. `New` installs them with `Use`, outermost first:

1. Request ID
2. Access log
3. `middleware.Recoverer`
4. CORS, only when `WithCORS` was passed
5. `middleware.Timeout`
6. Body size limit

Request ID is first so every later line and the response header can carry an id. The access log wraps the rest of the chain, including `Recoverer`, so a panic is logged as status 500. CORS sits in front of application routes so a preflight is answered before the handler runs.

## Request ID

Header name: `X-Request-Id` (`middleware.HeaderRequestID`).

An incoming value is kept only when it matches `^[A-Za-z0-9._-]{8,64}$`. Anything else is discarded and replaced. The new id is 16 random bytes, hex-encoded (32 characters). The response header is set before the rest of the chain runs, so it is present on a timeout or a recovered panic.

The base logger is stored on the request context with `zerolog.Logger.WithContext`, with `request_id` already attached. Handlers read that logger with `zerolog.Ctx`. The raw id is `middleware.RequestID(ctx)`.

The logger package does not provide `FromContext`, `WithRequestID`, or `RequestID`.

## Access log

One line per request that is not in the skip list, message `http request`. Stable field names: `method`, `route`, `path`, `status`, `duration_ms`. `path` is `r.URL.Path`. The query string and headers are never logged. `route` is the chi pattern, or `unmatched` when the pattern is empty. Status 0 is logged as 200. 5xx is error, 4xx is warn, anything else is info.

The line uses the level of the logger passed to `WithLogger`. That is the app's `LOG_LEVEL`. A warn logger does not write info lines (2xx and 3xx). An error logger writes only 5xx.

`WithAccessLogBody` adds `req_body` and `res_body` only when `middleware.AccessLogBody.Enable` is true and that payload is non-empty and no larger than its own max. `RequestMaxBytes` limits the request body. `ResponseMaxBytes` limits the response body. A bigger payload is left off the line. The handler still receives the full body. Leave `Enable` false unless you need it: bodies can contain secrets.

The line uses the logger already stored on the request. `UpdateContext` on that same logger is included. A child logger stored on a new context inside the handler is not.

## Limitations

`middleware.Recoverer` is used as chi ships it. The 500 body is empty. The panic stack is written to stderr by the standard library logger, without `request_id`, and not through zerolog.

Unmatched routes and method mismatches use chi's default 404 and 405 bodies, which are plain text.

The timeout middleware only cancels the request context. It writes 504 after the handler returns. Handlers and database calls have to stop when `r.Context()` is done. Keep this timeout shorter than the HTTP server write timeout. `httpserver` defaults that write timeout to 10s. This router defaults to 8s.

The body limit wraps `r.Body` with `http.MaxBytesReader`. Reading past the limit returns `*http.MaxBytesError`. This middleware does not write a response. Mapping that error to 413 belongs in the handler until a JSON error layer exists.

## CORS

`WithCORS` installs `github.com/go-chi/cors`. Without that option, no `Access-Control-*` header is sent.

Empty fields use these defaults: methods `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`; request headers `Accept`, `Authorization`, `Content-Type`, `X-Request-Id`; exposed headers `X-Request-Id`; max age 5 minutes.

Origins are matched exactly. Each entry is `*` or `scheme://host[:port]` with scheme `http` or `https`. No path, query, fragment, userinfo, or trailing slash. Patterns such as `https://*.example.com` are rejected. `*` together with `AllowCredentials` is rejected. A negative max age is rejected. Debug mode is off.

## Usage

`middleware` below is `github.com/rizkysr90/yourfutura-go-pkg/router/middleware`.

```go
// The starter owns this struct and parses it with github.com/caarlos0/env/v11.
// This router package does not read these variables.
var cfg struct {
    LogLevel       string        `env:"LOG_LEVEL" envDefault:"info"`
    LogBody                bool  `env:"LOG_BODY" envDefault:"false"`
    LogRequestBodyMaxBytes int64 `env:"LOG_REQUEST_BODY_MAX_BYTES" envDefault:"256"`
    LogResponseBodyMaxBytes int64 `env:"LOG_RESPONSE_BODY_MAX_BYTES" envDefault:"256"`
    RequestTimeout time.Duration `env:"REQUEST_TIMEOUT" envDefault:"8s"`
    MaxBodyBytes   int64         `env:"MAX_BODY_BYTES" envDefault:"1048576"`
    CORSOrigins    []string      `env:"CORS_ORIGINS" envSeparator:","`
}
if err := env.Parse(&cfg); err != nil {
    return err
}

log, err := logger.New(logger.Config{
    AppName: appName,
    Env:     env,
    Version: version,
    Level:   cfg.LogLevel,
    Format:  format,
    Output:  output,
})
if err != nil {
    return err
}

opts := []router.Option{
    router.WithLogger(log),
    router.WithAccessLogBody(&middleware.AccessLogBody{
        Enable:           cfg.LogBody,
        RequestMaxBytes:  cfg.LogRequestBodyMaxBytes,
        ResponseMaxBytes: cfg.LogResponseBodyMaxBytes,
    }),
    router.WithRequestTimeout(cfg.RequestTimeout),
    router.WithMaxBodyBytes(cfg.MaxBodyBytes),
}
if len(cfg.CORSOrigins) > 0 {
    opts = append(opts, router.WithCORS(middleware.CORS{Origins: cfg.CORSOrigins}))
}
h, err := router.New(opts...)
if err != nil {
    return err
}
h.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
    zerolog.Ctx(r.Context()).Info().Str("request_id", middleware.RequestID(r.Context())).Msg("ping")
    w.WriteHeader(http.StatusNoContent)
})

srv, err := httpserver.New(httpserver.WithHandler(h))
if err != nil {
    return err
}
return srv.Run(ctx)
```
