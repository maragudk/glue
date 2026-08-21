package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
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

		func() {
			defer func() {
				is.True(t, recover() != nil, "expected the panic to reach the caller")
			}()
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		}()

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)), "expected main attribute")
		is.True(t, oteltest.HasAttributeKey(span.Attributes(), "uptime_sec"), "expected uptime_sec attribute")
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

	t.Run("stores root span in context", func(t *testing.T) {
		oteltest.NewSpanRecorder(t)

		var rootSpan bool

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {
			rootSpan = gluehttp.GetRootSpanFromContext(r.Context()) != nil
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		is.True(t, rootSpan, "expected root span in context")
	})
}

func TestGetRootSpanFromContext(t *testing.T) {
	t.Run("returns nil when no root span in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		is.True(t, gluehttp.GetRootSpanFromContext(req.Context()) == nil)
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
