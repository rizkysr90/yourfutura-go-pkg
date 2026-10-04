# apperror

HTTP error type, JSON responses, and a JSON body reader. This package does not read environment variables and does not log. The handler logs `err` and then calls `WriteError`.

## Error JSON

`WriteError` writes one object:

```json
{"code":"not_found","message":"not found","request_id":"abc"}
```

`fields` is added only when the `*Error` has fields:

```json
{"code":"validation_error","message":"name is required","request_id":"abc","fields":[{"field":"name","message":"is required"}]}
```

`request_id` is `middleware.RequestID` from the router. It is an empty string when that middleware did not run. The key names `code`, `message`, `request_id`, and `fields` are a public contract.

An `*Error` is written as given when its status is 4xx or 5xx and both `Code` and `Message` are set. Any other error is status 500, code `internal`, message `internal error`. The wrapped cause stays out of the JSON.

## Reading a body

`Decode` reads one JSON value. `maxBytes` caps the read. A larger body is status 413, code `body_too_large`. Unknown fields are rejected. Broken JSON returns status 400, code `invalid_body`, with a message that names the problem. `Decode` does not write the response. Pass the error to `WriteError`.

A nil request, a nil destination, or a limit of 0 or less is a programmer error. `WriteError` turns that into the generic 500.

## 404 and 405

Register these on the mux returned by `router.New`:

```go
mux.NotFound(apperror.NotFound)
mux.MethodNotAllowed(apperror.MethodNotAllowed)
```

Both write the error JSON above. Chi does not pass the allowed methods into a custom 405 handler, so `Allow` is not set.

## Usage

```go
mux.NotFound(apperror.NotFound)
mux.MethodNotAllowed(apperror.MethodNotAllowed)

mux.Post("/users", func(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Name string `json:"name"`
    }
    if err := apperror.Decode(r, 1<<20, &req); err != nil {
        log.Error().Err(err).Msg("decode user")
        _ = apperror.WriteError(w, r, err)
        return
    }
    if req.Name == "" {
        err := &apperror.Error{
            Status:  http.StatusUnprocessableEntity,
            Code:    "validation_error",
            Message: "name is required",
            Fields:  []apperror.Field{{Name: "name", Message: "is required"}},
        }
        _ = apperror.WriteError(w, r, err)
        return
    }
    _ = apperror.Write(w, http.StatusCreated, map[string]string{"name": req.Name})
})
```
