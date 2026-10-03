# httpserver

HTTP server with safe timeouts and graceful shutdown.

This package does not write logs. The caller records `listening` before `Run` and `stopped` when `Run` returns.

## Options

- `WithPort` sets the TCP port. The default is `8080`. The server binds every interface (`:port`). That is what a container needs. On a laptop the OS may show a firewall prompt. The application already has this port and can log it before calling `Run`.
- `WithTimeouts` sets deadlines. A zero field keeps the default. A negative field makes `New` return an error.
- `WithHandler` sets the handler. The default responds 404. A nil handler makes `New` return an error. The server does not use `http.DefaultServeMux`.

## Timeouts

| Field | Default | Role |
| --- | --- | --- |
| `ReadHeader` | 5s | time to send headers (Slowloris) |
| `Read` | 10s | whole request, including the body |
| `Write` | 10s | response, including the handler |
| `Idle` | 60s | keep-alive wait for the next request |
| `Shutdown` | 25s | time for in-flight requests after stop |

`Write` covers handler time. The default is 10 seconds, so an export or a large response that takes longer is cut off. Raise `Write` for that handler.

`Shutdown` is shorter than Kubernetes' default `terminationGracePeriodSeconds` (30s), so the process can exit before `SIGKILL`.

## Run

`Run` serves until the context is canceled or the process receives `SIGINT` or `SIGTERM`, then waits for in-flight requests until `Shutdown` elapses. `Run` installs those signal handlers itself. `main` should not call `signal.Notify` for them again.

`Run` is single-use. A second call, including one that overlaps the first, returns `ErrAlreadyStarted`. A listen failure consumes the server too: the port was never served, and the next `Run` still returns `ErrAlreadyStarted`. Build a new `Server` to try again. A nil context or an already-canceled context returns before the server is marked started.

Canceling `ctx` while the server is running returns nil after in-flight requests finish. `http.ErrServerClosed` is not returned to the caller.

```go
appLog.Info().Int("port", port).Msg("listening")
err := srv.Run(ctx)
if err != nil {
    appLog.Error().Err(err).Msg("stopped")
    return err
}
appLog.Info().Msg("stopped")
```

## Shutdown on Kubernetes

`Run` begins graceful shutdown as soon as `SIGTERM` arrives. A pod can stay in the Service endpoints for a few seconds after that, so during a rolling deploy some requests reach a process that has already stopped accepting them.

Put a `preStop` sleep of a few seconds in the pod manifest so the pod leaves the endpoints before the server stops accepting connections. A `ShutdownDelay` option waits until a client runs on Kubernetes.

Readiness should start failing when shutdown begins. That needs a hook from this package, such as a channel that closes at the start of shutdown, for the health check to read. The hook lands with the health check, not in this server.

## Hijacked connections

`Shutdown` does not wait for hijacked connections, such as WebSockets. That matters when a client needs them.
