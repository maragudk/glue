package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
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

	t.Run("names a panicking handler's span when there is no chi route context at all", func(t *testing.T) {
		// The naming and the recording share a defer, and the naming reads a route context which is
		// nil outside a chi router, so this is the panic path with the most to go wrong on it
		sr := oteltest.NewSpanRecorder(t)

		h := gluehttp.OpenTelemetry(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("the parrot has ceased to be")
		}))

		serveExpectingPanic(t, h, httptest.NewRequest(http.MethodPost, "/anything", nil))

		span := lastEndedSpan(t, sr)
		is.Equal(t, "POST", span.Name())
		is.Equal(t, codes.Error, span.Status().Code)
	})

	t.Run("records a panicking handler as an error on the span", func(t *testing.T) {
		tests := []struct {
			name            string
			value           any
			expectedType    string
			expectedMessage string
		}{
			// The panic value is not wrapped when it is already an error, so the exception keeps the
			// type and message the handler panicked with. Wrapping would make every panic look like
			// the wrapper's type instead.
			{name: "error", value: errors.New("the parrot has ceased to be"), expectedType: "*errors.errorString", expectedMessage: "the parrot has ceased to be"},
			{name: "wrapped abort", value: fmt.Errorf("giving up: %w", http.ErrAbortHandler), expectedType: "*fmt.wrapError", expectedMessage: "giving up: net/http: abort Handler"},
			{name: "string", value: "the parrot has ceased to be", expectedType: "*errors.errorString", expectedMessage: "panic: the parrot has ceased to be"},
			{name: "anything else", value: 42, expectedType: "*errors.errorString", expectedMessage: "panic: 42"},
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
				is.True(t, v == test.value, "expected the original panic value to reach the caller")

				span := lastEndedSpan(t, sr)
				is.Equal(t, codes.Error, span.Status().Code)
				is.Equal(t, "panic", span.Status().Description)

				events := oteltest.ExceptionEventsWithStackTrace(span)
				is.Equal(t, 1, len(events))
				is.True(t, oteltest.HasAttribute(events[0].Attributes, semconv.ExceptionType(test.expectedType)),
					"expected exception type "+test.expectedType)
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
		is.True(t, v == error(http.ErrAbortHandler), "expected http.ErrAbortHandler to reach the caller")

		span := lastEndedSpan(t, sr)
		is.Equal(t, "GET /things/{id}", span.Name())
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.HTTPRoute("/things/{id}")), "expected http.route attribute")
		is.Equal(t, codes.Unset, span.Status().Code)
		is.Equal(t, 0, len(oteltest.ExceptionEventsWithStackTrace(span)))

		// An abort is not invisible: the SDK adds an exception event of its own on the way through
		// [trace.Span.End], which is what this one event is. What the middleware leaves out is the
		// error status and an exception of its own.
		is.Equal(t, 1, len(span.Events()))
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

	// Trace context on the request comes from a client outside this service's control, so it decides
	// neither the trace grouping nor the sampling. It is kept as a link, which correlates the two traces
	// without one owning the other.
	t.Run("starts a new trace root and links the remote span context the request carried", func(t *testing.T) {
		tests := []struct {
			name       string
			traceFlags string
		}{
			{name: "sampled trace", traceFlags: "01"},
			// Parented to this, the span would inherit the sampling decision and never be recorded at all
			// under the default sampler, so reaching the assertions below is itself part of the test.
			{name: "unsampled trace", traceFlags: "00"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				sr := oteltest.NewSpanRecorder(t)
				usePropagators(t)

				traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
				is.NotError(t, err)
				spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
				is.NotError(t, err)

				mux := chi.NewMux()
				mux.Use(gluehttp.OpenTelemetry)
				mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {})

				req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
				req.Header.Set("traceparent", "00-"+traceID.String()+"-"+spanID.String()+"-"+test.traceFlags)
				req.Header.Set("tracestate", "vendor=abc123")
				// Discarding the baggage must not take the trace context with it
				req.Header.Set("baggage", "user.id=evil")
				mux.ServeHTTP(httptest.NewRecorder(), req)

				span := lastEndedSpan(t, sr)
				is.True(t, !span.Parent().IsValid(), "expected the server span to have no parent")
				is.True(t, span.SpanContext().TraceID() != traceID, "expected a trace ID of this service's own")
				is.True(t, span.SpanContext().IsSampled(), "expected a sampling decision of this service's own")
				is.Equal(t, 0, span.SpanContext().TraceState().Len())

				links := span.Links()
				is.Equal(t, 1, len(links))
				is.Equal(t, traceID, links[0].SpanContext.TraceID())
				is.Equal(t, spanID, links[0].SpanContext.SpanID())
				is.True(t, links[0].SpanContext.IsRemote(), "expected the linked span context to be remote")
				// The vendor state is the client's, so it belongs on the link rather than on this trace
				is.Equal(t, "vendor=abc123", links[0].SpanContext.TraceState().String())
			})
		}
	})

	t.Run("starts a root span with no links when the request carries no trace context", func(t *testing.T) {
		tests := []struct {
			name        string
			traceparent string
		}{
			{name: "no traceparent header"},
			// Nothing valid to link, and nothing to fail the request over either
			{name: "malformed traceparent header", traceparent: "not-a-traceparent"},
			{name: "traceparent header with a zero trace ID", traceparent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				sr := oteltest.NewSpanRecorder(t)
				usePropagators(t)

				mux := chi.NewMux()
				mux.Use(gluehttp.OpenTelemetry)
				mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {})

				req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
				if test.traceparent != "" {
					req.Header.Set("traceparent", test.traceparent)
				}
				mux.ServeHTTP(httptest.NewRecorder(), req)

				span := lastEndedSpan(t, sr)
				is.True(t, !span.Parent().IsValid(), "expected the server span to have no parent")
				is.Equal(t, 0, len(span.Links()))
			})
		}
	})

	// The middleware is exported and can be reached with a span already in the request context: under a
	// router which traces, under another copy of this middleware, or from an in-process request. That
	// parent is this process's own, so severing it would lose the connection for nothing, and it would be
	// lost silently, since only a remote span context is linked.
	t.Run("keeps a parent span from the same process", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)
		usePropagators(t)

		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {})

		ctx, parent := otel.Tracer("test").Start(t.Context(), "parent")
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/things/42", nil).WithContext(ctx))
		parent.End()

		span := endedSpanNamed(t, sr, "GET /things/{id}")
		is.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID())
		is.Equal(t, parent.SpanContext().TraceID(), span.SpanContext().TraceID())
		is.Equal(t, 0, len(span.Links()))
	})

	// Baggage rides along on the context into everything below and out again through anything the
	// propagator is injected into, so what a client puts there would reach places it has no business
	// reaching.
	t.Run("discards the baggage the request carried", func(t *testing.T) {
		oteltest.NewSpanRecorder(t)
		usePropagators(t)

		var served bool
		var handlerBaggage baggage.Baggage
		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			served = true
			handlerBaggage = baggage.FromContext(r.Context())
		})

		req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
		req.Header.Set("baggage", "user.id=evil,tenant=acme")
		mux.ServeHTTP(httptest.NewRecorder(), req)

		is.True(t, served, "expected the handler to have run")
		is.Equal(t, 0, handlerBaggage.Len())
	})

	// Baggage in the context above the middleware goes the same way as the request's own, so that what
	// reaches a handler does not depend on which of the two a client managed to get its baggage into.
	t.Run("discards baggage already in the context", func(t *testing.T) {
		tests := []struct {
			name    string
			baggage string
		}{
			{name: "request carrying no baggage"},
			{name: "request carrying baggage of its own", baggage: "user.id=evil"},
			{name: "request carrying a malformed baggage header", baggage: "not a baggage header"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				oteltest.NewSpanRecorder(t)
				usePropagators(t)

				var served bool
				var handlerBaggage baggage.Baggage
				mux := chi.NewMux()
				mux.Use(gluehttp.OpenTelemetry)
				mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
					served = true
					handlerBaggage = baggage.FromContext(r.Context())
				})

				member, err := baggage.NewMember("tenant", "acme")
				is.NotError(t, err)
				contextBaggage, err := baggage.New(member)
				is.NotError(t, err)

				req := httptest.NewRequest(http.MethodGet, "/things/42", nil).
					WithContext(baggage.ContextWithBaggage(t.Context(), contextBaggage))
				if test.baggage != "" {
					req.Header.Set("baggage", test.baggage)
				}
				mux.ServeHTTP(httptest.NewRecorder(), req)

				is.True(t, served, "expected the handler to have run")
				is.Equal(t, 0, handlerBaggage.Len())
			})
		}
	})

	// The middleware is applied twice where a traced router is mounted in another, and the request's
	// baggage is discarded again by the inner one rather than mistaken for this process's own.
	t.Run("discards the baggage the request carried under another copy of the middleware", func(t *testing.T) {
		oteltest.NewSpanRecorder(t)
		usePropagators(t)

		var served bool
		var handlerBaggage baggage.Baggage
		inner := chi.NewMux()
		inner.Use(gluehttp.OpenTelemetry)
		inner.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			served = true
			handlerBaggage = baggage.FromContext(r.Context())
		})

		outer := chi.NewMux()
		outer.Use(gluehttp.OpenTelemetry)
		outer.Mount("/api", inner)

		req := httptest.NewRequest(http.MethodGet, "/api/things/42", nil)
		req.Header.Set("baggage", "user.id=evil")
		outer.ServeHTTP(httptest.NewRecorder(), req)

		is.True(t, served, "expected the handler to have run")
		is.Equal(t, 0, handlerBaggage.Len())
	})

	// The context below the middleware is what anything the request reaches propagates from, which is how
	// baggage would travel on out of this service if it survived
	t.Run("does not propagate onwards the baggage the request carried", func(t *testing.T) {
		oteltest.NewSpanRecorder(t)
		usePropagators(t)

		carrier := propagation.MapCarrier{}
		mux := chi.NewMux()
		mux.Use(gluehttp.OpenTelemetry)
		mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
			// What an outbound request or a job enqueued while handling this one carries with it
			otel.GetTextMapPropagator().Inject(r.Context(), carrier)
		})

		req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
		req.Header.Set("baggage", "user.id=evil")
		mux.ServeHTTP(httptest.NewRecorder(), req)

		is.Equal(t, "", carrier.Get("baggage"))
		is.True(t, carrier.Get("traceparent") != "", "expected the trace context to propagate")
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

// usePropagators globally for the duration of the test, via [oteltest.UsePropagators]. The middleware's
// behaviour depends on both trace context and baggage, and it reads them off the request through the global
// [propagation.TextMapPropagator], which extracts nothing by default, so traceparent and baggage headers go
// unnoticed without this. Like [oteltest.NewSpanRecorder] this mutates global state, so it is not safe for
// parallel tests.
func usePropagators(t *testing.T) {
	t.Helper()

	p := oteltest.UsePropagators(t, propagation.TraceContext{}, propagation.Baggage{})

	// Every assertion about baggage below the middleware is that there is none, which a propagator not
	// carrying baggage in the first place would satisfy for the wrong reason
	is.True(t, slices.Contains(p.Fields(), "baggage"), "expected the propagator under test to carry baggage")
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

// serveExpectingPanic with the given handler, returning the value it panicked with and failing the test
// if it did not panic.
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
