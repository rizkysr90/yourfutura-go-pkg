package logger

// Config is the one-time setup for a logger. Callers pass plain values;
// this package does not read the environment.
type Config struct {
	AppName string
	Env     string
	Version string
	Level   string // debug, info, warn, error
	Format  string // auto, json, console
	Output  string // stdout, stderr
}
