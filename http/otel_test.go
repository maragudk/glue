package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
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

	t.Run("sets main attribute", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)))
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

	t.Run("adds query parameters as attributes with lowercased keys", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/search", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/search?q=hello&PageSize=10", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		attrs := span.Attributes()
		is.True(t, oteltest.HasAttribute(attrs, attribute.StringSlice("url.query.q", []string{"hello"})))
		is.True(t, oteltest.HasAttribute(attrs, attribute.StringSlice("url.query.pagesize", []string{"10"})))
	})

	t.Run("does not add query attributes when no query parameters", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := lastEndedSpan(t, sr)
		for _, attr := range span.Attributes() {
			is.True(t, !strings.HasPrefix(string(attr.Key), "url.query."), "unexpected query attribute")
		}
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
