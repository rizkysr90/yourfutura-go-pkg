package logger

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout swaps os.Stdout until read is called. New must run after this,
// because the logger keeps the writer it was given.
func captureStdout(t *testing.T) (read func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = orig
		w.Close()
	})
	return func() string {
		t.Helper()
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
}

func logLine(t *testing.T, env, format, level string) string {
	t.Helper()
	read := captureStdout(t)
	l, err := New(Config{
		AppName: "billing",
		Env:     env,
		Version: "1.2.3",
		Level:   level,
		Format:  format,
		Output:  "stdout",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	l.Info().Msg("hello")
	return read()
}

func TestJSONFormat(t *testing.T) {
	out := logLine(t, "local", "json", "info")

	var event map[string]any
	if err := json.Unmarshal([]byte(out), &event); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	for _, key := range []string{"app", "env", "version", "level", "message", "time"} {
		if _, ok := event[key]; !ok {
			t.Errorf("JSON missing %q in %s", key, out)
		}
	}
	if event["app"] != "billing" || event["env"] != "local" || event["version"] != "1.2.3" {
		t.Fatalf("fields = %#v", event)
	}
	if event["level"] != "info" || event["message"] != "hello" {
		t.Fatalf("level/message = %#v", event)
	}
}

func TestConsoleFormatIsNotJSON(t *testing.T) {
	out := logLine(t, "production", "console", "info")
	if json.Valid([]byte(out)) {
		t.Fatalf("console output is JSON: %s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("console output = %q, want message", out)
	}
}

func TestAutoFormat(t *testing.T) {
	tests := []struct {
		env    string
		isJSON bool
	}{
		{env: "local", isJSON: false},
		{env: "staging", isJSON: true},
		{env: "production", isJSON: true},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			out := logLine(t, tt.env, "auto", "info")
			if got := json.Valid([]byte(out)); got != tt.isJSON {
				t.Fatalf("json.Valid = %v, want %v; output = %s", got, tt.isJSON, out)
			}
		})
	}
}

func TestInvalidConfig(t *testing.T) {
	base := Config{Level: "info", Format: "json", Output: "stdout"}
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{name: "level", edit: func(c *Config) { c.Level = "verbose" }},
		{name: "format", edit: func(c *Config) { c.Format = "xml" }},
		{name: "output", edit: func(c *Config) { c.Output = "file" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.edit(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("New() error = nil, want error")
			}
		})
	}
}

func TestLevelFiltering(t *testing.T) {
	read := captureStdout(t)
	l, err := New(Config{
		AppName: "billing",
		Env:     "staging",
		Version: "1.2.3",
		Level:   "info",
		Format:  "json",
		Output:  "stdout",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	l.Debug().Msg("hidden-debug")
	l.Info().Msg("visible-info")
	out := read()
	if strings.Contains(out, "hidden-debug") {
		t.Fatalf("debug log was written: %s", out)
	}
	if !strings.Contains(out, "visible-info") {
		t.Fatalf("info log missing from %q", out)
	}
}
