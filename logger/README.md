# logger

Zerolog logger. This package does not read environment variables. Callers fill `Config` once and pass it to `New`.

## Config

- `AppName`, `Env`, `Version` are attached to every line.
- `Level` is `debug`, `info`, `warn`, or `error`.
- `Format` is `auto`, `json`, or `console`.
- `Output` is `stdout` or `stderr`.

Invalid `Level`, `Format`, or `Output` returns an error. There is no silent fallback.

## Format `auto`

`auto` writes a console line when `Env` is `local`, and JSON for every other environment. `json` and `console` force that format regardless of `Env`.

## Stable field names

Every line includes `app`, `env`, and `version`. Those key names are a public contract.

## Usage

```go
log, err := logger.New(logger.Config{
    AppName: appName,
    Env:     env,
    Version: version,
    Level:   level,
    Format:  format,
    Output:  output,
})
if err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
}

log.Info().Int("port", port).Msg("starting")
```
