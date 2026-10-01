package logger

import (
	"fmt"
	"io"
	"os"

	"github.com/rs/zerolog"
)

var levels = map[string]zerolog.Level{
	"debug": zerolog.DebugLevel,
	"info":  zerolog.InfoLevel,
	"warn":  zerolog.WarnLevel,
	"error": zerolog.ErrorLevel,
}

// New returns a logger that stamps timestamp, app, env, and version on every line.
// It does not change zerolog's global logger or global level.
func New(cfg Config) (zerolog.Logger, error) {
	level, ok := levels[cfg.Level]
	if !ok {
		return zerolog.Logger{}, fmt.Errorf("invalid log level %q: must be one of debug, info, warn, error", cfg.Level)
	}
	format, err := resolveFormat(cfg.Format, cfg.Env)
	if err != nil {
		return zerolog.Logger{}, err
	}
	dest, err := destination(cfg)
	if err != nil {
		return zerolog.Logger{}, err
	}
	w := dest
	if format == "console" {
		w = zerolog.ConsoleWriter{Out: dest}
	}
	return zerolog.New(w).Level(level).With().
		Timestamp().
		Str("app", cfg.AppName).
		Str("env", cfg.Env).
		Str("version", cfg.Version).
		Logger(), nil
}

func resolveFormat(format, env string) (string, error) {
	switch format {
	case "json", "console":
		return format, nil
	case "auto":
		if env == "local" {
			return "console", nil
		}
		return "json", nil
	default:
		return "", fmt.Errorf("invalid log format %q: must be one of auto, json, console", format)
	}
}

func destination(cfg Config) (io.Writer, error) {
	switch cfg.Output {
	case "stdout":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	default:
		return nil, fmt.Errorf("invalid log output %q: must be one of stdout, stderr", cfg.Output)
	}
}
