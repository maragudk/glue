package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

func OpenTelemetry(next http.Handler) http.Handler {
	return otelhttp.NewHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)

			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			span := trace.SpanFromContext(r.Context())
			span.SetName(r.Method + " " + routePattern)
			span.SetAttributes(semconv.HTTPRoute(routePattern))
		}),
		"", // Setting the name here doesn't matter, it's done on the span above
	)
}
