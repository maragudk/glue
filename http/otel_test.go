package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"maragu.dev/is"

	gluehttp "maragu.dev/glue/http"
	"maragu.dev/glue/oteltest"
)

func TestOpenTelemetry(t *testing.T) {
	t.Run("sets span name to method and route pattern", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		is.Equal(t, http.StatusOK, rec.Code)

		spans := sr.Ended()
		is.True(t, len(spans) > 0, "expected at least one span")

		span := spans[len(spans)-1]
		is.Equal(t, "GET /things/{id}", span.Name())
	})

	t.Run("sets main attribute", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := sr.Ended()[len(sr.Ended())-1]
		is.True(t, hasAttribute(span.Attributes(), attribute.Bool("main", true)))
	})

	t.Run("sets http.route attribute", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := sr.Ended()[len(sr.Ended())-1]
		is.True(t, hasAttribute(span.Attributes(), semconv.HTTPRoute("/users/{id}")))
	})

	t.Run("parses user agent attributes", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := sr.Ended()[len(sr.Ended())-1]
		attrs := span.Attributes()
		is.True(t, hasAttribute(attrs, semconv.UserAgentName("Chrome")))
		is.True(t, hasAttribute(attrs, attribute.String("device.type", "desktop")))
	})

	t.Run("detects bot user agent", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("User-Agent", "Googlebot/2.1 (+http://www.google.com/bot.html)")
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := sr.Ended()[len(sr.Ended())-1]
		attrs := span.Attributes()
		is.True(t, hasAttribute(attrs, attribute.Bool("user_agent.bot", true)))
		is.True(t, hasAttribute(attrs, attribute.String("device.type", "bot")))
	})

	t.Run("adds query parameters as attributes", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/search", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/search?q=hello&page=1", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := sr.Ended()[len(sr.Ended())-1]
		attrs := span.Attributes()
		is.True(t, hasAttribute(attrs, attribute.StringSlice("url.query.q", []string{"hello"})))
		is.True(t, hasAttribute(attrs, attribute.StringSlice("url.query.page", []string{"1"})))
	})

	t.Run("does not add query attributes when no query parameters", func(t *testing.T) {
		sr := oteltest.Setup(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)

		span := sr.Ended()[len(sr.Ended())-1]
		for _, attr := range span.Attributes() {
			key := string(attr.Key)
			is.True(t, len(key) < 10 || key[:10] != "url.query.", "unexpected query attribute")
		}
	})

	t.Run("stores root span in context", func(t *testing.T) {
		oteltest.Setup(t)

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

func hasAttribute(attrs []attribute.KeyValue, want attribute.KeyValue) bool {
	for _, attr := range attrs {
		if attr.Key == want.Key && attr.Value == want.Value {
			return true
		}
	}
	return false
}
