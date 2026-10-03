// Package httpserver runs an HTTP server with safe timeouts and graceful shutdown.
//
// The standard library leaves every [http.Server] timeout at zero, and zero means
// the timeout is disabled. A disabled ReadHeaderTimeout lets a client hold the
// connection open without finishing its headers (Slowloris). Timeouts here stay
// at a positive default unless the caller sets another positive duration.
//
// [Server.Run] listens until the context is canceled or the process receives
// SIGINT or SIGTERM, which is what Kubernetes sends before it stops a pod. Run
// then waits for in-flight requests to finish, up to the shutdown timeout, so a
// request that is still writing a database transaction can complete. When that
// deadline passes, Run closes the remaining connections and returns, so the
// process can exit before the kubelet sends SIGKILL.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrAlreadyStarted is returned when [Server.Run] is called again.
// One [http.Server] cannot listen twice: after shutdown, Serve returns
// [http.ErrServerClosed] immediately, and a second bind on port 0 can attach
// another listener to the same server.
var ErrAlreadyStarted = errors.New("server already started")

const (
	// DefaultPort is used when [WithPort] is not passed.
	DefaultPort = 8080

	// DefaultReadHeaderTimeout is how long a client may take to send headers.
	// This is the Slowloris limit.
	DefaultReadHeaderTimeout = 5 * time.Second

	// DefaultReadTimeout is how long a client may take to send the whole request.
	DefaultReadTimeout = 10 * time.Second

	// DefaultWriteTimeout is how long the server may take to write the response.
	// The deadline includes the handler, so a long export needs a larger value.
	DefaultWriteTimeout = 10 * time.Second

	// DefaultIdleTimeout is how long a keep-alive connection may wait for the next request.
	DefaultIdleTimeout = 60 * time.Second

	// DefaultShutdownTimeout is how long [Server.Run] waits for in-flight requests
	// after SIGINT, SIGTERM, or context cancellation. It is shorter than
	// Kubernetes' default terminationGracePeriodSeconds (30s), so the process
	// can leave before the kubelet sends SIGKILL.
	DefaultShutdownTimeout = 25 * time.Second
)

// Timeouts are the deadlines for connections and for process shutdown.
// A zero duration keeps the safe default. A negative duration is rejected.
// [http.Server] treats zero as "no timeout".
type Timeouts struct {
	// ReadHeader is how long the client may take to send request headers.
	ReadHeader time.Duration
	// Read is how long the client may take to send the whole request, including the body.
	Read time.Duration
	// Write is how long the server may take to write the response.
	// The deadline includes the handler.
	Write time.Duration
	// Idle is how long a keep-alive connection may wait for the next request.
	Idle time.Duration
	// Shutdown is how long [Server.Run] waits for in-flight requests after
	// SIGINT, SIGTERM, or a canceled context.
	Shutdown time.Duration
}

// Option configures a [Server].
type Option func(*config)

type config struct {
	port     int
	timeouts Timeouts
	handler  http.Handler
}

// WithPort sets the TCP port. Port 0 asks the kernel for an ephemeral port.
func WithPort(port int) Option {
	return func(c *config) {
		c.port = port
	}
}

// WithTimeouts sets connection and shutdown deadlines.
// Zero fields keep their defaults. Negative fields make [New] return an error.
func WithTimeouts(t Timeouts) Option {
	return func(c *config) {
		c.timeouts = t
	}
}

// WithHandler sets the request handler. A nil handler makes [New] return an error.
// Without this option the server responds with 404, and it does not use
// [http.DefaultServeMux].
func WithHandler(h http.Handler) Option {
	return func(c *config) {
		c.handler = h
	}
}

// Server is an HTTP server with timeouts and graceful shutdown.
// Create it with [New]. [Server.Run] serves until shutdown and returns.
type Server struct {
	srv             *http.Server
	shutdownTimeout time.Duration
	started         atomic.Bool
}

// New builds a server. With no options it listens on [DefaultPort], applies
// the default timeouts, and responds 404.
func New(opts ...Option) (*Server, error) {
	cfg := config{
		port: DefaultPort,
		timeouts: Timeouts{
			ReadHeader: DefaultReadHeaderTimeout,
			Read:       DefaultReadTimeout,
			Write:      DefaultWriteTimeout,
			Idle:       DefaultIdleTimeout,
			Shutdown:   DefaultShutdownTimeout,
		},
		handler: http.NotFoundHandler(),
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("nil option")
		}
		opt(&cfg)
	}
	if cfg.port < 0 || cfg.port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be between 0 and 65535", cfg.port)
	}
	timeouts, err := resolveTimeouts(cfg.timeouts)
	if err != nil {
		return nil, err
	}
	if cfg.handler == nil {
		return nil, errors.New("nil handler")
	}
	return &Server{
		srv: &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.port),
			Handler:           cfg.handler,
			ReadHeaderTimeout: timeouts.ReadHeader,
			ReadTimeout:       timeouts.Read,
			WriteTimeout:      timeouts.Write,
			IdleTimeout:       timeouts.Idle,
		},
		shutdownTimeout: timeouts.Shutdown,
	}, nil
}

func resolveTimeouts(in Timeouts) (Timeouts, error) {
	out := in
	fields := []struct {
		name string
		val  *time.Duration
		def  time.Duration
	}{
		{name: "read header", val: &out.ReadHeader, def: DefaultReadHeaderTimeout},
		{name: "read", val: &out.Read, def: DefaultReadTimeout},
		{name: "write", val: &out.Write, def: DefaultWriteTimeout},
		{name: "idle", val: &out.Idle, def: DefaultIdleTimeout},
		{name: "shutdown", val: &out.Shutdown, def: DefaultShutdownTimeout},
	}
	for _, f := range fields {
		switch {
		case *f.val < 0:
			return Timeouts{}, fmt.Errorf("invalid %s timeout %s: must be greater than or equal to 0", f.name, *f.val)
		case *f.val == 0:
			*f.val = f.def
		}
	}
	return out, nil
}

// Run serves HTTP until ctx is canceled or the process receives SIGINT or SIGTERM.
// It then stops accepting connections and waits up to the shutdown timeout for
// in-flight requests to finish.
//
// Run is single-use. A second call, including one that overlaps the first,
// returns [ErrAlreadyStarted]. A listen failure consumes the server too:
// the next call returns [ErrAlreadyStarted], so retry with a new [Server].
// A nil context or an already-canceled context returns before the server is
// marked started, and does not bind a port.
//
// The shutdown deadline is not taken from ctx. When shutdown starts, ctx is
// already canceled, and a derived deadline would drop active requests at once.
// If in-flight requests are still running when the shutdown timeout expires,
// Run closes those connections and returns an error that wraps the timeout.
//
// Run returns nil after a clean shutdown, including when ctx is canceled while
// the server is running. [http.ErrServerClosed] is not returned to the caller.
func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		serveErr := s.srv.Serve(ln)
		if errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- nil
			return
		}
		errCh <- serveErr
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		// Drop the signal registration so a second SIGINT or SIGTERM during
		// the grace period uses the default action and can stop the process.
		stop()
	}

	// Own deadline: ctx is already canceled by the signal or the caller.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	shutErr := s.srv.Shutdown(shutdownCtx)
	if shutErr != nil {
		// Drop active connections so a stuck handler cannot keep Run waiting.
		_ = s.srv.Close()
		if errors.Is(shutErr, context.DeadlineExceeded) {
			return fmt.Errorf("shutdown after %s: %w", s.shutdownTimeout, shutErr)
		}
		return fmt.Errorf("shutdown: %w", shutErr)
	}
	if err := <-errCh; err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
