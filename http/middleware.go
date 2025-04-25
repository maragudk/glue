package http

import "net/http"

type Middleware = func(next http.Handler) http.Handler

// ContextKey is a custom type to be used for storing keys in a [context.Context].
type ContextKey string
