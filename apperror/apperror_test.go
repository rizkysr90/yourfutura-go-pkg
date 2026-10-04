package apperror

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rizkysr90/yourfutura-go-pkg/router/middleware"
	"github.com/rs/zerolog"
)

func bodyMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return got
}

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Write(rec, http.StatusCreated, map[string]string{"id": "u1"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	got := bodyMap(t, rec)
	if got["id"] != "u1" {
		t.Fatalf("body = %#v", got)
	}
}

func TestWriteNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Write(rec, http.StatusNoContent, map[string]string{"id": "u1"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestWriteErrorUsesAppError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	req.Header.Set(middleware.HeaderRequestID, "req-12345678")
	var rec *httptest.ResponseRecorder
	middleware.RequestIDMiddleware(zerolog.Nop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec = httptest.NewRecorder()
		err := Wrap(errors.New("sql: no rows"), http.StatusNotFound, CodeNotFound, "user not found")
		err.Fields = []Field{{Name: "id", Message: "unknown"}}
		if writeErr := WriteError(rec, r, fmt.Errorf("load: %w", err)); writeErr != nil {
			t.Fatal(writeErr)
		}
	})).ServeHTTP(httptest.NewRecorder(), req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	got := bodyMap(t, rec)
	if got["code"] != CodeNotFound || got["message"] != "user not found" || got["request_id"] != "req-12345678" {
		t.Fatalf("body = %#v", got)
	}
	if strings.Contains(rec.Body.String(), "sql") {
		t.Fatalf("cause leaked: %s", rec.Body.String())
	}
	fields, ok := got["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("fields = %#v", got["fields"])
	}
}

func TestWriteErrorHidesPlainError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := WriteError(rec, req, errors.New("password reset token row missing")); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	got := bodyMap(t, rec)
	if got["code"] != CodeInternal || got["message"] != "internal error" || got["request_id"] != "" {
		t.Fatalf("body = %#v", got)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("cause leaked: %s", rec.Body.String())
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	mux := chi.NewRouter()
	mux.Use(middleware.RequestIDMiddleware(zerolog.Nop()))
	mux.Get("/items", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.NotFound(NotFound)
	mux.MethodNotAllowed(MethodNotAllowed)

	missing := httptest.NewRecorder()
	missReq := httptest.NewRequest(http.MethodGet, "/missing", nil)
	missReq.Header.Set(middleware.HeaderRequestID, "req-404path")
	mux.ServeHTTP(missing, missReq)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("404 status = %d", missing.Code)
	}
	if ct := missing.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("404 content-type = %q", ct)
	}
	got := bodyMap(t, missing)
	if got["code"] != CodeNotFound || got["message"] != "not found" || got["request_id"] != "req-404path" {
		t.Fatalf("404 body = %#v", got)
	}

	wrong := httptest.NewRecorder()
	wrongReq := httptest.NewRequest(http.MethodPost, "/items", nil)
	wrongReq.Header.Set(middleware.HeaderRequestID, "req-405path")
	mux.ServeHTTP(wrong, wrongReq)
	if wrong.Code != http.StatusMethodNotAllowed {
		t.Fatalf("405 status = %d", wrong.Code)
	}
	got = bodyMap(t, wrong)
	if got["code"] != CodeMethodNotAllowed || got["message"] != "method not allowed" || got["request_id"] != "req-405path" {
		t.Fatalf("405 body = %#v", got)
	}
	if allow := wrong.Header().Get("Allow"); allow != "" {
		t.Fatalf("Allow = %q", allow)
	}
}

func TestDecode(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ada","age":1}`))
	if err := Decode(req, 64, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "ada" || dst.Age != 1 {
		t.Fatalf("dst = %#v", dst)
	}
}

func TestDecodeFailures(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		max     int64
		status  int
		code    string
		message string
	}{
		{name: "empty", body: "", max: 64, status: 400, code: CodeInvalidBody, message: "request body is empty"},
		{name: "broken", body: `{"name"`, max: 64, status: 400, code: CodeInvalidBody, message: "invalid json: unexpected end of input"},
		{name: "syntax", body: `{"name":`, max: 64, status: 400, code: CodeInvalidBody, message: "invalid json: unexpected end of input"},
		{name: "unknown", body: `{"name":"ada","extra":1}`, max: 64, status: 400, code: CodeInvalidBody, message: `unknown field "extra"`},
		{name: "type", body: `{"name":"ada","age":"old"}`, max: 64, status: 400, code: CodeInvalidBody, message: `field "age" has the wrong type`},
		{name: "two values", body: `{"name":"a"}{"name":"b"}`, max: 64, status: 400, code: CodeInvalidBody, message: "request body must contain a single JSON value"},
		{name: "too large", body: `{"name":"abcdefghij"}`, max: 8, status: 413, code: CodeBodyTooLarge, message: "request body is too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var dst struct {
				Name string `json:"name"`
				Age  int    `json:"age"`
			}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			err := Decode(req, tc.max, &dst)
			var ae *Error
			if !errors.As(err, &ae) {
				t.Fatalf("error = %v", err)
			}
			if ae.Status != tc.status || ae.Code != tc.code || ae.Message != tc.message {
				t.Fatalf("error = status %d code %q message %q", ae.Status, ae.Code, ae.Message)
			}
		})
	}
}

func TestDecodeProgrammerErrorsStayInternal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	err := Decode(req, 0, &struct{}{})
	var ae *Error
	if errors.As(err, &ae) {
		t.Fatalf("programmer error is client-safe: %#v", ae)
	}
	rec := httptest.NewRecorder()
	if writeErr := WriteError(rec, req, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "body limit") {
		t.Fatalf("programmer message leaked: %s", rec.Body.String())
	}
}

func TestDecodeReadsTheLimitedBodyAgain(t *testing.T) {
	raw := []byte(`{"name":"ada"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	var dst struct {
		Name string `json:"name"`
	}
	if err := Decode(req, int64(len(raw)), &dst); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("unread = %q", rest)
	}
}
