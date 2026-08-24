package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/is"

	gluehttp "maragu.dev/glue/http"
	"maragu.dev/glue/oteltest"
)

func TestOpenTelemetry(t *testing.T) {
	t.Run("sets span name to method and route pattern", func(t *testing.T) {
		tests := []struct {
			name         string
			method       string
			pattern      string
			target       string
			expectedName string
		}{
			{name: "GET with path parameter", method: http.MethodGet, pattern: "/things/{id}", target: "/things/42", expectedName: "GET /things/{id}"},
			{name: "POST", method: http.MethodPost, pattern: "/things", target: "/things", expectedName: "POST /things"},
			{name: "PUT with path parameter", method: http.MethodPut, pattern: "/things/{id}", target: "/things/42", expectedName: "PUT /things/{id}"},
			{name: "DELETE with path parameter", method: http.MethodDelete, pattern: "/things/{id}", target: "/things/42", expectedName: "DELETE /things/{id}"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				sr := oteltest.NewSpanRecorder(t)

				mux := chi.NewMux()
				mux.Use(gluehttp.OpenTelemetry)
				mux.Method(test.method, test.pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

				req := httptest.NewRequest(test.method, test.target, nil)
				mux.ServeHTTP(httptest.NewRecorder(), req)

				span := lastEndedSpan(t, sr)
				is.Equal(t, test.expectedName, span.Name())
			})
		}
	})

	// A router which has already matched before the middleware runs sets [http.Request.Pattern] on the
	// request the middleware is handed, so these cover the naming against a router above the middleware
	// as well as below it.
	t.Run("sets span name to method and route pattern below another router", func(t *testing.T) {
		t.Run("mounted under an outer mux", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			inner := chi.NewMux()
			inner.Use(gluehttp.OpenTelemetry)
			inner.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {})

			outer := chi.NewMux()
			outer.Mount("/api", inner)

			outer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things/42", nil))

			span := lastEndedSpan(t, sr)
			is.Equal(t, "GET /api/things/{id}", span.Name())
			is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/api/things/{id}")))
		})

		t.Run("inside a subrouter", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			mux := chi.NewMux()
			mux.Route("/api", func(r chi.Router) {
				r.Use(gluehttp.OpenTelemetry)
				r.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {})
			})

			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things/42", nil))

			span := lastEndedSpan(t, sr)
			is.Equal(t, "GET /api/things/{id}", span.Name())
			is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/api/things/{id}")))
		})
	})

	t.Run("sets main span attributes", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)))
		is.True(t, oteltest.HasAttributeKey(span.Attributes(), "uptime_sec"), "expected uptime_sec attribute")
		is.True(t, oteltest.HasAttributeKey(span.Attributes(), "uptime_sec_log_10"), "expected uptime_sec_log_10 attribute")
	})

	t.Run("sets main span attributes even when the handler panics", func(t *testing.T) {
		// There is no recovery middleware in the chain, so a panicking handler unwinds past the
		// middleware. The span is still ended and exported, and a panicking request is still a unit
		// of work, so it has to stay countable.
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {
			panic("the parrot has ceased to be")
		})

		serveExpectingPanic(t, mux, httptest.NewRequest(http.MethodGet, "/", nil))

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)), "expected main attribute")
		is.True(t, oteltest.HasAttributeKey(span.Attributes(), "uptime_sec"), "expected uptime_sec attribute")
	})

	t.Run("names a panicking handler's span and sets its route", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			panic("the parrot has ceased to be")
		})

		serveExpectingPanic(t, mux, httptest.NewRequest(http.MethodGet, "/things/42", nil))

		span := lastEndedSpan(t, sr)
		is.Equal(t, "GET /things/{id}", span.Name())
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/things/{id}")), "expected http.route attribute")
	})

	t.Run("records a panicking handler as an error on the span", func(t *testing.T) {
		tests := []struct {
			name            string
			value           any
			expectedMessage string
		}{
			// The panic value is not wrapped when it is already an error, so the exception keeps the
			// type and message the handler panicked with
			{name: "error", value: errors.New("the parrot has ceased to be"), expectedMessage: "the parrot has ceased to be"},
			{name: "string", value: "the parrot has ceased to be", expectedMessage: "panic: the parrot has ceased to be"},
			{name: "anything else", value: 42, expectedMessage: "panic: 42"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				sr := oteltest.NewSpanRecorder(t)

				mux := chi.NewMux()
				mux.Use(gluehttp.OpenTelemetry)
				mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
					panic(test.value)
				})

				v := serveExpectingPanic(t, mux, httptest.NewRequest(http.MethodGet, "/things/42", nil))
				is.Equal(t, fmt.Sprint(test.value), fmt.Sprint(v), "expected the original panic value to reach the caller")

				span := lastEndedSpan(t, sr)
				is.Equal(t, codes.Error, span.Status().Code)

				events := oteltest.ExceptionEventsWithStackTrace(span)
				is.Equal(t, 1, len(events))
				is.True(t, oteltest.HasAttribute(events[0].Attributes, semconv.ExceptionMessage(test.expectedMessage)),
					"expected exception message "+test.expectedMessage)

				// The stack has to be the one which panicked, not the one which recorded it, so the
				// panicking handler in this file must appear in it
				stacktrace, ok := attributeValue(events[0].Attributes, semconv.ExceptionStacktraceKey)
				is.True(t, ok, "expected exception stacktrace attribute")
				is.True(t, strings.Contains(stacktrace.AsString(), "otel_test.go"), "expected the panic site in the stacktrace")
			})
		}
	})

	t.Run("does not record an aborted handler as an error, but still names the span", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		})

		v := serveExpectingPanic(t, mux, httptest.NewRequest(http.MethodGet, "/things/42", nil))
		err, ok := v.(error)
		is.True(t, ok, "expected the panic value to be an error")
		is.True(t, errors.Is(err, http.ErrAbortHandler), "expected http.ErrAbortHandler to reach the caller")

		span := lastEndedSpan(t, sr)
		is.Equal(t, "GET /things/{id}", span.Name())
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/things/{id}")), "expected http.route attribute")
		is.Equal(t, codes.Unset, span.Status().Code)
		is.Equal(t, 0, len(oteltest.ExceptionEventsWithStackTrace(span)))
	})

	t.Run("keeps the main span attributes when the request carries a flood of query parameters", func(t *testing.T) {
		// The SDK caps a span at 128 attributes by default and drops everything past it, so a request
		// which mints one attribute per query parameter could push main and http.route off the span.
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {})

		params := make([]string, 0, 200)
		for i := range 200 {
			params = append(params, fmt.Sprintf("p%v=v%v", i, i))
		}

		req := httptest.NewRequest(http.MethodGet, "/things/42?"+strings.Join(params, "&"), nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)), "expected main attribute")
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/things/{id}")), "expected http.route attribute")
		is.True(t, oteltest.HasAttributeKey(span.Attributes(), "url.query"), "expected url.query attribute")
	})

	t.Run("sets http.route attribute", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/users/{id}")))
	})

	t.Run("names the span with just the method when no route matches", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/nope", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		// Exactly the method, so a trailing space fails here
		is.Equal(t, "GET", span.Name())
		is.True(t, !oteltest.HasAttributeKey(span.Attributes(), "http.route"), "unexpected http.route attribute")
	})

	t.Run("names the span with just the method when there is no chi route context at all", func(t *testing.T) {
		// The middleware is exported, so it can be applied to a handler outside a chi router
		sr := oteltest.NewSpanRecorder(t)

		h := gluehttp.OpenTelemetry(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/anything", nil))

		span := lastEndedSpan(t, sr)
		is.Equal(t, "POST", span.Name())
		is.True(t, !oteltest.HasAttributeKey(span.Attributes(), "http.route"), "unexpected http.route attribute")
	})

	t.Run("parses user agent attributes", func(t *testing.T) {
		tests := []struct {
			name          string
			userAgent     string
			expectedAttrs []attribute.KeyValue
		}{
			{
				name:      "desktop browser",
				userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				expectedAttrs: []attribute.KeyValue{
					semconv.UserAgentName("Chrome"),
					attribute.String("device.type", "desktop"),
				},
			},
			{
				name:      "mobile browser",
				userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
				expectedAttrs: []attribute.KeyValue{
					semconv.UserAgentName("Safari"),
					attribute.String("device.type", "mobile"),
					semconv.BrowserMobile(true),
				},
			},
			{
				name:      "bot with URL",
				userAgent: "Googlebot/2.1 (+http://www.google.com/bot.html)",
				expectedAttrs: []attribute.KeyValue{
					attribute.Bool("user_agent.bot", true),
					attribute.String("device.type", "bot"),
					attribute.String("user_agent.url", "http://www.google.com/bot.html"),
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				sr := oteltest.NewSpanRecorder(t)

				mux := chi.NewMux()
				mux.Use(gluehttp.OpenTelemetry)
				mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("User-Agent", test.userAgent)
				mux.ServeHTTP(httptest.NewRecorder(), req)

				span := lastEndedSpan(t, sr)
				attrs := span.Attributes()
				for _, expected := range test.expectedAttrs {
					is.True(t, oteltest.HasAttribute(attrs, expected))
				}
			})
		}
	})

	t.Run("sets the query string as a single attribute", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/search", func(w http.ResponseWriter, r *http.Request) {})

		// Q and q are different parameters, which one attribute per key could not represent
		req := httptest.NewRequest(http.MethodGet, "/search?q=hello&Q=goodbye&PageSize=10", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.URLQuery("q=hello&Q=goodbye&PageSize=10")))
	})

	t.Run("sets no query attribute when there is no query string", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/search", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/search", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, !oteltest.HasAttributeKey(span.Attributes(), "url.query"), "unexpected url.query attribute")
	})

	t.Run("sets client disconnected attribute when the request context is canceled", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		// Simulate a client that has gone away by canceling the request context.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("http.client_disconnected", true)))
	})

	t.Run("does not set client disconnected attribute when the client is still connected", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, !oteltest.HasAttributeKey(span.Attributes(), "http.client_disconnected"), "unexpected client disconnected attribute")
	})

	// This is what lets everything below write telemetry without being handed a span: whatever the context
	// carries below the middleware is the main span itself.
	t.Run("makes the main span the current span for handlers below it", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			trace.SpanFromContext(r.Context()).SetAttributes(attribute.Bool("from.handler", true))
		})

		req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		// The write has to have landed on the one span marked main, not merely on some recording span
		span := lastEndedSpan(t, sr)
		is.Equal(t, "GET /things/{id}", span.Name())
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)))
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("from.handler", true)),
			"expected the handler to have written on the main span")
	})
}

// lastEndedSpan returns the last ended span from the recorder, failing the test if none exist.
func lastEndedSpan(t *testing.T, sr interface {
	Ended() []sdktrace.ReadOnlySpan
}) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := sr.Ended()
	if len(spans) == 0 {
		t.Fatal("expected at least one ended span")
	}
	return spans[len(spans)-1]
}

// serveExpectingPanic serves the request with the given handler and returns the value it panicked with,
// failing the test if it did not panic.
func serveExpectingPanic(t *testing.T, h http.Handler, req *http.Request) (v any) {
	t.Helper()

	defer func() {
		if v = recover(); v == nil {
			t.Error("expected the panic to reach the caller")
		}
	}()

	h.ServeHTTP(httptest.NewRecorder(), req)

	return nil
}

// attributeValue for the given key in the slice, also reporting whether it was there at all.
func attributeValue(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

// endedSpanNamed returns the ended span with the given name, failing the test if there is no such span.
func endedSpanNamed(t *testing.T, sr interface {
	Ended() []sdktrace.ReadOnlySpan
}, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	names := make([]string, 0, len(sr.Ended()))
	for _, span := range sr.Ended() {
		if span.Name() == name {
			return span
		}
		names = append(names, span.Name())
	}

	t.Fatalf("no ended span named %v, recorded %v", name, names)
	return nil
}
