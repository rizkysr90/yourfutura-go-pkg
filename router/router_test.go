package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rizkysr90/yourfutura-go-pkg/logger"
	"github.com/rizkysr90/yourfutura-go-pkg/router/middleware"
	"github.com/rs/zerolog"
)

func testLogger(t *testing.T) (zerolog.Logger, func() string) {
	t.Helper()
	return testLoggerAt(t, "debug")
}

func testLoggerAt(t *testing.T, level string) (zerolog.Logger, func() string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = pw
	t.Cleanup(func() {
		os.Stdout = orig
		pw.Close()
		pr.Close()
	})
	lg, err := logger.New(logger.Config{
		AppName: "billing",
		Env:     "test",
		Version: "dev",
		Level:   level,
		Format:  "json",
		Output:  "stdout",
	})
	os.Stdout = orig
	if err != nil {
		t.Fatal(err)
	}
	return lg, func() string {
		t.Helper()
		if err := pw.Close(); err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(pr)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

func parseLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

func httpRequests(lines []map[string]any) []map[string]any {
	var out []map[string]any
	for _, ev := range lines {
		if ev["message"] == "http request" {
			out = append(out, ev)
		}
	}
	return out
}

func serve(h http.Handler, method, target string, body io.Reader, hdr http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for k, vs := range hdr {
		req.Header[k] = append([]string(nil), vs...)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequestID(t *testing.T) {
	lg, read := testLogger(t)
	var got string
	r, err := New(WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/id", func(w http.ResponseWriter, r *http.Request) {
		got = middleware.RequestID(r.Context())
		zerolog.Ctx(r.Context()).Info().Msg("from-handler")
		w.WriteHeader(http.StatusNoContent)
	})

	rec := serve(r, http.MethodGet, "/id", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	id := rec.Header().Get(middleware.HeaderRequestID)
	if len(id) != 32 {
		t.Fatalf("generated id = %q", id)
	}
	if got != id {
		t.Fatalf("RequestID = %q, header = %q", got, id)
	}
	var handlerLine map[string]any
	for _, ev := range parseLines(t, read()) {
		if ev["message"] == "from-handler" {
			handlerLine = ev
		}
	}
	if handlerLine["request_id"] != id {
		t.Fatalf("handler log = %#v", handlerLine)
	}
}

func TestRequestIDReusedAndReplaced(t *testing.T) {
	lg, _ := testLogger(t)
	r, err := New(WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/id", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Seen-Id", middleware.RequestID(r.Context()))
	})

	valid := "req.id_OK-1"
	rec := serve(r, http.MethodGet, "/id", nil, http.Header{middleware.HeaderRequestID: {valid}})
	if rec.Header().Get(middleware.HeaderRequestID) != valid || rec.Header().Get("Seen-Id") != valid {
		t.Fatalf("valid id not reused: %#v", rec.Header())
	}

	for _, bad := range []string{"short", strings.Repeat("a", 65), "bad id!!"} {
		rec := serve(r, http.MethodGet, "/id", nil, http.Header{middleware.HeaderRequestID: {bad}})
		got := rec.Header().Get(middleware.HeaderRequestID)
		if got == "" || got == bad || len(got) != 32 {
			t.Fatalf("invalid %q replaced with %q", bad, got)
		}
	}
}

func TestAccessLog(t *testing.T) {
	lg, read := testLogger(t)
	r, err := New(WithLogger(lg), WithAccessLogSkip("/health"))
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	serve(r, http.MethodGet, "/ok", nil, nil)
	serve(r, http.MethodGet, "/missing", nil, nil)
	serve(r, http.MethodGet, "/fail", nil, nil)
	serve(r, http.MethodGet, "/health", nil, nil)
	serve(r, http.MethodGet, "/users/123", nil, nil)
	serve(r, http.MethodGet, "/search?q=supersecret", nil, nil)

	raw := read()
	if strings.Contains(raw, "supersecret") || strings.Contains(raw, "q=") {
		t.Fatalf("query leaked into logs: %s", raw)
	}
	lines := httpRequests(parseLines(t, raw))
	byPath := map[string]map[string]any{}
	for _, ev := range lines {
		byPath[ev["path"].(string)] = ev
	}
	if _, logged := byPath["/health"]; logged {
		t.Fatal("skip path was logged")
	}
	for _, path := range []string{"/ok", "/missing", "/fail", "/users/123", "/search"} {
		ev, ok := byPath[path]
		if !ok {
			t.Fatalf("missing log for %s in %#v", path, lines)
		}
		for _, key := range []string{"method", "route", "path", "status", "duration_ms"} {
			if _, ok := ev[key]; !ok {
				t.Errorf("%s missing %s in %#v", path, key, ev)
			}
		}
	}
	if byPath["/ok"]["level"] != "info" || statusOf(t, byPath["/ok"]) != 200 {
		t.Fatalf("ok log = %#v", byPath["/ok"])
	}
	if byPath["/missing"]["level"] != "warn" || statusOf(t, byPath["/missing"]) != 404 {
		t.Fatalf("missing log = %#v", byPath["/missing"])
	}
	if byPath["/missing"]["route"] != "unmatched" {
		t.Fatalf("unmatched route = %#v", byPath["/missing"]["route"])
	}
	if byPath["/fail"]["level"] != "error" || statusOf(t, byPath["/fail"]) != 500 {
		t.Fatalf("fail log = %#v", byPath["/fail"])
	}
	if byPath["/users/123"]["route"] != "/users/{id}" {
		t.Fatalf("route = %#v", byPath["/users/123"]["route"])
	}
	if byPath["/search"]["path"] != "/search" {
		t.Fatalf("path = %#v", byPath["/search"]["path"])
	}
	if len(lines) != 5 {
		t.Fatalf("got %d access lines, want 5", len(lines))
	}
	if _, ok := byPath["/ok"]["req_body"]; ok {
		t.Fatal("request body logged while body logging is off")
	}
	if _, ok := byPath["/ok"]["res_body"]; ok {
		t.Fatal("response body logged while body logging is off")
	}
}

func TestAccessLogBodyLimit(t *testing.T) {
	lg, read := testLogger(t)
	r, err := New(WithLogger(lg), WithAccessLogBody(&middleware.AccessLogBody{
		Enable:           true,
		RequestMaxBytes:  2,
		ResponseMaxBytes: 4,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	r.Post("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		got = string(b)
		_, _ = w.Write([]byte("pong"))
	})
	r.Post("/bigres", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("12345"))
	})

	serve(r, http.MethodPost, "/echo", strings.NewReader("hi"), nil)
	if got != "hi" {
		t.Fatalf("handler body = %q", got)
	}
	serve(r, http.MethodPost, "/echo", strings.NewReader("hello"), nil)
	if got != "hello" {
		t.Fatalf("oversize handler body = %q", got)
	}
	serve(r, http.MethodPost, "/bigres", strings.NewReader("yo"), nil)

	lines := httpRequests(parseLines(t, read()))
	if len(lines) != 3 {
		t.Fatalf("got %d lines: %#v", len(lines), lines)
	}
	if lines[0]["req_body"] != "hi" || lines[0]["res_body"] != "pong" {
		t.Fatalf("small payloads = %#v", lines[0])
	}
	if _, ok := lines[1]["req_body"]; ok {
		t.Fatalf("oversize request was logged: %#v", lines[1])
	}
	if lines[1]["res_body"] != "pong" {
		t.Fatalf("small response missing: %#v", lines[1])
	}
	if _, ok := lines[2]["res_body"]; ok {
		t.Fatalf("oversize response was logged: %#v", lines[2])
	}
	if lines[2]["req_body"] != "yo" {
		t.Fatalf("small request missing: %#v", lines[2])
	}
}

func TestAccessLogFollowsLoggerLevel(t *testing.T) {
	lg, read := testLoggerAt(t, "warn")
	r, err := New(WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	serve(r, http.MethodGet, "/ok", nil, nil)
	serve(r, http.MethodGet, "/missing", nil, nil)
	serve(r, http.MethodGet, "/fail", nil, nil)

	lines := httpRequests(parseLines(t, read()))
	byPath := map[string]map[string]any{}
	for _, ev := range lines {
		byPath[ev["path"].(string)] = ev
	}
	if _, ok := byPath["/ok"]; ok {
		t.Fatalf("info line written at warn level: %#v", byPath["/ok"])
	}
	if byPath["/missing"]["level"] != "warn" {
		t.Fatalf("404 log = %#v", byPath["/missing"])
	}
	if byPath["/fail"]["level"] != "error" {
		t.Fatalf("500 log = %#v", byPath["/fail"])
	}
}

func TestPanicIsLoggedAndServerContinues(t *testing.T) {
	lg, read := testLogger(t)
	r, err := New(WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	r.Get("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := serve(r, http.MethodGet, "/boom", nil, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("panic body = %q", rec.Body.String())
	}
	if rec.Header().Get(middleware.HeaderRequestID) == "" {
		t.Fatal("missing request id on panic response")
	}
	ok := serve(r, http.MethodGet, "/ok", nil, nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("follow-up status = %d", ok.Code)
	}

	lines := httpRequests(parseLines(t, read()))
	if len(lines) != 2 || statusOf(t, lines[0]) != 500 {
		t.Fatalf("logs = %#v", lines)
	}
}

func TestTimeout(t *testing.T) {
	lg, _ := testLogger(t)
	r, err := New(WithLogger(lg), WithRequestTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	var saw error
	r.Get("/slow", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		saw = r.Context().Err()
	})
	rec := serve(r, http.MethodGet, "/slow", nil, nil)
	if !errors.Is(saw, context.DeadlineExceeded) {
		t.Fatalf("context err = %v", saw)
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
}

func TestBodyLimit(t *testing.T) {
	lg, _ := testLogger(t)
	r, err := New(WithLogger(lg), WithMaxBodyBytes(8))
	if err != nil {
		t.Fatal(err)
	}
	var got error
	var body string
	r.Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		got = err
		body = string(b)
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	serve(r, http.MethodPost, "/upload", strings.NewReader("123456789"), nil)
	var maxErr *http.MaxBytesError
	if !errors.As(got, &maxErr) {
		t.Fatalf("err = %v, want *http.MaxBytesError", got)
	}

	serve(r, http.MethodPost, "/upload", strings.NewReader("12345678"), nil)
	if got != nil || body != "12345678" {
		t.Fatalf("body = %q, err = %v", body, got)
	}
}

func TestCORS(t *testing.T) {
	lg, _ := testLogger(t)
	plain, err := New(WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	plain.Get("/widgets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := serve(plain, http.MethodGet, "/widgets", nil, http.Header{"Origin": {"https://app.example"}})
	for k := range rec.Header() {
		if strings.HasPrefix(k, "Access-Control-") {
			t.Fatalf("unexpected CORS header %s", k)
		}
	}

	var hit bool
	allowed, err := New(WithLogger(lg), WithCORS(middleware.CORS{Origins: []string{"https://app.example"}}))
	if err != nil {
		t.Fatal(err)
	}
	allowed.Post("/widgets", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusCreated)
	})
	rec = serve(allowed, http.MethodPost, "/widgets", nil, http.Header{"Origin": {"https://app.example"}})
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("allow-origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), middleware.HeaderRequestID) {
		t.Fatalf("expose-headers = %q", rec.Header().Get("Access-Control-Expose-Headers"))
	}

	hit = false
	pre := serve(allowed, http.MethodOptions, "/widgets", nil, http.Header{
		"Origin":                        {"https://app.example"},
		"Access-Control-Request-Method": {"POST"},
	})
	if hit {
		t.Fatal("preflight reached the handler")
	}
	if pre.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("preflight allow-origin = %q", pre.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(pre.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("preflight allow-methods = %q", pre.Header().Get("Access-Control-Allow-Methods"))
	}

	denied := serve(allowed, http.MethodPost, "/widgets", nil, http.Header{"Origin": {"https://evil.example"}})
	if denied.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin got %q", denied.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSValidation(t *testing.T) {
	lg, _ := testLogger(t)
	cases := []middleware.CORS{
		{},
		{Origins: []string{"https://example.com/path"}},
		{Origins: []string{"https://*.example.com"}},
		{Origins: []string{"*"}, AllowCredentials: true},
		{Origins: []string{"https://app.example"}, MaxAge: -time.Second},
		{Origins: []string{"not a url"}},
	}
	for _, c := range cases {
		if _, err := New(WithLogger(lg), WithCORS(c)); err == nil {
			t.Fatalf("New(%#v) error = nil", c)
		}
	}
}

func TestOptionErrors(t *testing.T) {
	lg, _ := testLogger(t)
	if _, err := New(); err == nil {
		t.Fatal("missing logger")
	}
	if _, err := New(WithLogger(lg), WithRequestTimeout(-time.Second)); err == nil {
		t.Fatal("negative timeout")
	}
	if _, err := New(WithLogger(lg), WithMaxBodyBytes(-1)); err == nil {
		t.Fatal("negative body limit")
	}
	if _, err := New(WithLogger(lg), WithAccessLogBody(&middleware.AccessLogBody{Enable: true, ResponseMaxBytes: 4})); err == nil {
		t.Fatal("enabled body logging without a request max")
	}
	if _, err := New(WithLogger(lg), WithAccessLogBody(&middleware.AccessLogBody{Enable: true, RequestMaxBytes: 4})); err == nil {
		t.Fatal("enabled body logging without a response max")
	}
	if _, err := New(WithLogger(lg), WithAccessLogBody(&middleware.AccessLogBody{})); err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil); err == nil {
		t.Fatal("nil option")
	}
	if _, err := New(WithLogger(lg), nil); err == nil {
		t.Fatal("nil option after logger")
	}
}

func TestZeroValuesKeepDefaults(t *testing.T) {
	lg, _ := testLogger(t)
	r, err := New(WithLogger(lg), WithRequestTimeout(0), WithMaxBodyBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	r.Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/fast", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if rec := serve(r, http.MethodGet, "/fast", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("fast status = %d", rec.Code)
	}
	okBody := bytes.Repeat([]byte("a"), defaultMaxBodyBytes)
	if rec := serve(r, http.MethodPost, "/upload", bytes.NewReader(okBody), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("default-sized body status = %d", rec.Code)
	}
	big := bytes.Repeat([]byte("b"), defaultMaxBodyBytes+1)
	if rec := serve(r, http.MethodPost, "/upload", bytes.NewReader(big), nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body status = %d", rec.Code)
	}
}

func TestApplicationRouteUsesChain(t *testing.T) {
	lg, read := testLogger(t)
	r, err := New(WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		if middleware.RequestID(r.Context()) == "" {
			http.Error(w, "missing id", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})
	rec := serve(r, http.MethodGet, "/ping", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "pong" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(middleware.HeaderRequestID) == "" {
		t.Fatal("missing request id")
	}
	lines := httpRequests(parseLines(t, read()))
	if len(lines) != 1 || lines[0]["path"] != "/ping" {
		t.Fatalf("logs = %#v", lines)
	}
}

func statusOf(t *testing.T, ev map[string]any) int {
	t.Helper()
	f, ok := ev["status"].(float64)
	if !ok {
		t.Fatalf("status = %#v", ev["status"])
	}
	return int(f)
}
