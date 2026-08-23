package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
)

func TestTracingMux(t *testing.T) {
	// Every way of reaching a handler should time it exactly once. Registering through a group, a route
	// or With hands out a decorator over the sub-router, so the wrap happens there and not again above.
	tests := []struct {
		name     string
		register func(mux *TracingMux, h http.HandlerFunc) string
		timed    bool
	}{
		{
			name: "registered on the router itself",
			register: func(mux *TracingMux, h http.HandlerFunc) string {
				mux.Get("/thing", h)
				return "/thing"
			},
			timed: true,
		},
		{
			name: "registered with Handle",
			register: func(mux *TracingMux, h http.HandlerFunc) string {
				mux.Handle("/thing", h)
				return "/thing"
			},
			timed: true,
		},
		{
			name: "registered inside a group",
			register: func(mux *TracingMux, h http.HandlerFunc) string {
				mux.Group(func(r chi.Router) {
					r.Get("/thing", h)
				})
				return "/thing"
			},
			timed: true,
		},
		{
			name: "registered inside a route",
			register: func(mux *TracingMux, h http.HandlerFunc) string {
				mux.Route("/sub", func(r chi.Router) {
					r.Get("/thing", h)
				})
				return "/sub/thing"
			},
			timed: true,
		},
		{
			name: "registered behind With",
			register: func(mux *TracingMux, h http.HandlerFunc) string {
				mux.With(func(next http.Handler) http.Handler { return next }).Get("/thing", h)
				return "/thing"
			},
			timed: true,
		},
		{
			name: "mounted, so the sub-router owns its own handlers",
			register: func(mux *TracingMux, h http.HandlerFunc) string {
				sub := chi.NewRouter()
				sub.Get("/thing", h)
				mux.Mount("/sub", sub)
				return "/sub/thing"
			},
			timed: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			mux := &TracingMux{mux: chi.NewRouter()}
			mux.Use(OpenTelemetry)

			target := test.register(mux, func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(5 * time.Millisecond)
			})

			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))

			attrs := onlyEndedSpan(t, sr).Attributes()

			if !test.timed {
				is.True(t, !oteltest.HasAttributeKey(attrs, "handler.duration_ms"), "unexpected handler.duration_ms")
				return
			}

			is.True(t, oteltest.HasAttributeKey(attrs, "handler.duration_ms"), "expected handler.duration_ms")
			for _, attr := range attrs {
				if attr.Key != "handler.duration_ms" {
					continue
				}
				is.Equal(t, attribute.FLOAT64, attr.Value.Type())
				// The handler slept 5ms, so a live measurement cannot be near zero
				is.True(t, attr.Value.AsFloat64() >= 1, "expected the handler duration to reflect the time spent in it")
			}
		})
	}

	t.Run("times a handler which panics, since the measurement is deferred", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := &TracingMux{mux: chi.NewRouter()}
		mux.Use(OpenTelemetry)
		mux.Get("/thing", func(w http.ResponseWriter, r *http.Request) {
			panic("the parrot has ceased to be")
		})

		func() {
			defer func() {
				is.True(t, recover() != nil, "expected the panic to reach the caller")
			}()
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/thing", nil))
		}()

		is.True(t, oteltest.HasAttributeKey(onlyEndedSpan(t, sr).Attributes(), "handler.duration_ms"),
			"expected handler.duration_ms")
	})

	t.Run("does not start a span of its own", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := &TracingMux{mux: chi.NewRouter()}
		mux.Use(OpenTelemetry)
		mux.Get("/thing", func(w http.ResponseWriter, r *http.Request) {})

		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/thing", nil))

		// Only the main span, no http.Handler span underneath it
		is.Equal(t, 1, len(sr.Ended()))
	})
}

// onlyEndedSpan returns the single ended span, failing the test if there is not exactly one.
func onlyEndedSpan(t *testing.T, sr *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()

	spans := sr.Ended()
	if len(spans) != 1 {
		names := make([]string, 0, len(spans))
		for _, span := range spans {
			names = append(names, span.Name())
		}
		t.Fatalf("expected exactly one ended span, got %v: %v", len(spans), names)
	}

	return spans[0]
}
