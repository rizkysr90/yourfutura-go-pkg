package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestNewDefaultsAreSafe(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if s.srv.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", s.srv.Addr)
	}
	got := Timeouts{
		ReadHeader: s.srv.ReadHeaderTimeout,
		Read:       s.srv.ReadTimeout,
		Write:      s.srv.WriteTimeout,
		Idle:       s.srv.IdleTimeout,
		Shutdown:   s.shutdownTimeout,
	}
	want := Timeouts{
		ReadHeader: DefaultReadHeaderTimeout,
		Read:       DefaultReadTimeout,
		Write:      DefaultWriteTimeout,
		Idle:       DefaultIdleTimeout,
		Shutdown:   DefaultShutdownTimeout,
	}
	if got != want {
		t.Fatalf("timeouts = %+v, want %+v", got, want)
	}
	if got.ReadHeader <= 0 || got.Read <= 0 || got.Write <= 0 || got.Idle <= 0 || got.Shutdown <= 0 {
		t.Fatalf("timeout must be positive, got %+v", got)
	}
}

func TestExplicitZeroTimeoutKeepsDefault(t *testing.T) {
	s, err := New(WithTimeouts(Timeouts{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if s.srv.ReadHeaderTimeout != DefaultReadHeaderTimeout || s.shutdownTimeout != DefaultShutdownTimeout {
		t.Fatalf("zero timeouts were applied: header=%s shutdown=%s", s.srv.ReadHeaderTimeout, s.shutdownTimeout)
	}
}

func TestPartialTimeoutOverride(t *testing.T) {
	const shutdown = 5 * time.Second
	s, err := New(WithTimeouts(Timeouts{Shutdown: shutdown}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if s.shutdownTimeout != shutdown {
		t.Fatalf("shutdown = %s, want %s", s.shutdownTimeout, shutdown)
	}
	if s.srv.ReadHeaderTimeout != DefaultReadHeaderTimeout {
		t.Fatalf("read header = %s, want default %s", s.srv.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	}
	if s.srv.ReadTimeout != DefaultReadTimeout || s.srv.WriteTimeout != DefaultWriteTimeout || s.srv.IdleTimeout != DefaultIdleTimeout {
		t.Fatalf("connection timeouts changed: read=%s write=%s idle=%s", s.srv.ReadTimeout, s.srv.WriteTimeout, s.srv.IdleTimeout)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	neg := -1 * time.Second
	tests := []struct {
		name string
		opts []Option
	}{
		{name: "port negative", opts: []Option{WithPort(-1)}},
		{name: "port too high", opts: []Option{WithPort(65536)}},
		{name: "nil option", opts: []Option{nil}},
		{name: "nil handler", opts: []Option{WithHandler(nil)}},
		{name: "negative read header", opts: []Option{WithTimeouts(Timeouts{ReadHeader: neg})}},
		{name: "negative read", opts: []Option{WithTimeouts(Timeouts{Read: neg})}},
		{name: "negative write", opts: []Option{WithTimeouts(Timeouts{Write: neg})}},
		{name: "negative idle", opts: []Option{WithTimeouts(Timeouts{Idle: neg})}},
		{name: "negative shutdown", opts: []Option{WithTimeouts(Timeouts{Shutdown: neg})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.opts...); err == nil {
				t.Fatal("New() error = nil, want error")
			}
		})
	}
}

func TestPortBoundaries(t *testing.T) {
	for _, port := range []int{0, 65535} {
		s, err := New(WithPort(port))
		if err != nil {
			t.Fatalf("New(%d) error = %v", port, err)
		}
		if s.srv.Addr != ":"+strconv.Itoa(port) {
			t.Fatalf("Addr = %q, want :%d", s.srv.Addr, port)
		}
	}
}

func TestRunNilContext(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 nil is the value under test
	if err := s.Run(nil); err == nil {
		t.Fatal("Run(nil) error = nil, want error")
	}
}

func TestRunCanceledContextDoesNotBind(t *testing.T) {
	port := freePort(t)
	s, err := New(WithPort(port))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}

func TestRunListenError(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	s, err := New(WithPort(port))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = s.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want listen error")
	}
	if errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Run() = %v, want a listen error", err)
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Run() = %v, want a listen error", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("listen error took %s", time.Since(start))
	}
	if err := s.Run(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Run() = %v, want ErrAlreadyStarted", err)
	}
}

func TestRunValidationDoesNotConsumeServer(t *testing.T) {
	s, err := New(WithPort(freePort(t)))
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 nil is the value under test
	if err := s.Run(nil); err == nil {
		t.Fatal("Run(nil) error = nil, want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	_, url, errCh, stop := startServer(t, s)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	stop()
	waitErr(t, errCh, nil)
}

func TestRunOnce(t *testing.T) {
	s, url, errCh, cancel := start(t, WithPort(freePort(t)), WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	if err := s.Run(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("overlapping Run() = %v, want ErrAlreadyStarted", err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	cancel()
	waitErr(t, errCh, nil)

	if err := s.Run(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Run() after shutdown = %v, want ErrAlreadyStarted", err)
	}
}

func TestConcurrentRunUsesOneListener(t *testing.T) {
	s, err := New(WithPort(freePort(t)), WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- s.Run(ctx) }()
	}

	url := waitListen(t, s)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	select {
	case err := <-errs:
		if !errors.Is(err, ErrAlreadyStarted) {
			t.Fatalf("second Run() = %v, want ErrAlreadyStarted", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second Run did not return")
	}

	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestRunServesThenShutsDown(t *testing.T) {
	s, url, errCh, cancel := start(t, WithPort(freePort(t)))
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	cancel()
	waitErr(t, errCh, nil)
	if _, err := net.DialTimeout("tcp", hostport(t, s.srv.Addr), 200*time.Millisecond); err == nil {
		t.Fatal("listener still open after shutdown")
	}
}

func TestGracefulShutdownCompletesInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)

	var ctxErr error
	var mu sync.Mutex

	s, err := New(
		WithPort(freePort(t)),
		WithTimeouts(Timeouts{
			Shutdown: 3 * time.Second,
			Read:     5 * time.Second,
			Write:    5 * time.Second,
		}),
		WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var first bool
			startOnce.Do(func() {
				first = true
				close(started)
			})
			if first {
				<-release
				mu.Lock()
				ctxErr = r.Context().Err()
				mu.Unlock()
			}
			_, _ = io.WriteString(w, "ok")
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()
	url := waitListen(t, s)

	type result struct {
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(url)
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			resCh <- result{err: errors.New(resp.Status)}
			return
		}
		resCh <- result{body: string(body), err: err}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach handler")
	}

	cancel()
	waitRefused(t, url)

	select {
	case res := <-resCh:
		t.Fatalf("in-flight request finished before release: %+v", res)
	default:
	}

	letGo()

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("request error = %v", res.err)
		}
		if res.body != "ok" {
			t.Fatalf("body = %q, want ok", res.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not finish")
	}

	waitErr(t, errCh, nil)

	mu.Lock()
	defer mu.Unlock()
	if ctxErr != nil {
		t.Fatalf("request context canceled while the handler was still running: %v", ctxErr)
	}
}

func TestShutdownTimeoutDoesNotBlock(t *testing.T) {
	entered := make(chan struct{})
	block := make(chan struct{})
	var enterOnce sync.Once
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(block) }) })

	const shutdown = 200 * time.Millisecond
	s, err := New(
		WithPort(freePort(t)),
		WithTimeouts(Timeouts{Shutdown: shutdown}),
		WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enterOnce.Do(func() { close(entered) })
			<-block
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()
	url := waitListen(t, s)

	go func() {
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(url)
		if err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach handler")
	}

	start := time.Now()
	cancel()
	select {
	case err := <-errCh:
		elapsed := time.Since(start)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() = %v, want deadline exceeded", err)
		}
		if !strings.Contains(err.Error(), "shutdown after "+shutdown.String()) {
			t.Fatalf("Run() = %v, want the shutdown deadline in the message", err)
		}
		if elapsed+50*time.Millisecond < shutdown {
			t.Fatalf("Run returned in %s, before shutdown timeout %s", elapsed, shutdown)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("Run blocked for %s", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after shutdown timeout")
	}
}

func TestReadHeaderTimeoutClosesSlowClient(t *testing.T) {
	const headerTimeout = 200 * time.Millisecond
	s, err := New(
		WithPort(freePort(t)),
		WithTimeouts(Timeouts{ReadHeader: headerTimeout}),
		WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, url, errCh, cancel := startServer(t, s)
	t.Cleanup(func() {
		cancel()
		waitErr(t, errCh, nil)
	})

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if s.srv.ReadTimeout != DefaultReadTimeout {
		t.Fatalf("read timeout = %s, want default %s so the header timeout is what closes the slow client", s.srv.ReadTimeout, DefaultReadTimeout)
	}

	conn, err := net.Dial("tcp", hostport(t, s.srv.Addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("slow client is still reading")
	}
	if elapsed > time.Second {
		t.Fatalf("connection stayed open for %s with read header timeout %s: %v", elapsed, headerTimeout, err)
	}
}

func TestSignalShutdown(t *testing.T) {
	// Signals are process-wide. Keep this test sequential with every other
	// test that calls Run, and do not mark it parallel.
	tests := []struct {
		name string
		sig  syscall.Signal
	}{
		{name: "sigint", sig: syscall.SIGINT},
		{name: "sigterm", sig: syscall.SIGTERM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(
				WithPort(freePort(t)),
				WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})),
			)
			if err != nil {
				t.Fatal(err)
			}
			errCh := make(chan error, 1)
			go func() { errCh <- s.Run(context.Background()) }()
			url := waitListen(t, s)

			resp, err := http.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			if err := syscall.Kill(os.Getpid(), tt.sig); err != nil {
				t.Fatal(err)
			}
			waitErr(t, errCh, nil)
			if _, err := net.DialTimeout("tcp", hostport(t, s.srv.Addr), 200*time.Millisecond); err == nil {
				t.Fatal("listener still open after signal")
			}
		})
	}
}

func start(t *testing.T, opts ...Option) (s *Server, url string, errCh <-chan error, cancel context.CancelFunc) {
	t.Helper()
	s, err := New(opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return startServer(t, s)
}

func startServer(t *testing.T, s *Server) (srv *Server, url string, errCh <-chan error, cancel context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		ch <- s.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("Run did not return")
		}
	})
	return s, waitListen(t, s), ch, cancel
}

func waitListen(t *testing.T, s *Server) string {
	t.Helper()
	hp := hostport(t, s.srv.Addr)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", hp, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return "http://" + hp
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not accept connections")
	return ""
}

func waitRefused(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			return
		}
		resp.Body.Close()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server still accepting connections")
}

func waitErr(t *testing.T, errCh <-chan error, want error) {
	t.Helper()
	select {
	case err := <-errCh:
		if want == nil && err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
		if want != nil && !errors.Is(err, want) {
			t.Fatalf("Run() = %v, want %v", err, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func hostport(t *testing.T, addr string) string {
	t.Helper()
	hp, ok := splitHostPort(addr)
	if !ok {
		t.Fatalf("unusable address %q", addr)
	}
	return hp
}

func splitHostPort(addr string) (string, bool) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" {
		return "", false
	}
	return net.JoinHostPort("127.0.0.1", port), true
}
