package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	"maragu.dev/httph"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	gluehttp "maragu.dev/glue/http"
)

func TestRouter(t *testing.T) {
	t.Run("responds 499 when the handler returns a canceled context error", func(t *testing.T) {
		router := gluehttp.NewRouter(gluehttp.NewRouterOpts{})
		router.Get("/", func(props html.PageProps) (g.Node, error) {
			return nil, context.Canceled
		})

		code := serve(t, router, http.MethodGet, "/")
		is.Equal(t, 499, code)
	})

	t.Run("responds 499 when the canceled context error is wrapped", func(t *testing.T) {
		router := gluehttp.NewRouter(gluehttp.NewRouterOpts{})
		router.Get("/", func(props html.PageProps) (g.Node, error) {
			return nil, fmt.Errorf("querying the database: %w", context.Canceled)
		})

		code := serve(t, router, http.MethodGet, "/")
		is.Equal(t, 499, code)
	})

	t.Run("responds 500 for a genuine error that is not a canceled context", func(t *testing.T) {
		router := gluehttp.NewRouter(gluehttp.NewRouterOpts{})
		router.Get("/", func(props html.PageProps) (g.Node, error) {
			return nil, errors.New("the gremlins are back")
		})

		code := serve(t, router, http.MethodGet, "/")
		is.Equal(t, http.StatusInternalServerError, code)
	})

	t.Run("responds 500 for a deadline exceeded, which is a real server timeout", func(t *testing.T) {
		router := gluehttp.NewRouter(gluehttp.NewRouterOpts{})
		router.Get("/", func(props html.PageProps) (g.Node, error) {
			return nil, context.DeadlineExceeded
		})

		code := serve(t, router, http.MethodGet, "/")
		is.Equal(t, http.StatusInternalServerError, code)
	})
}

func serve(t *testing.T, router *gluehttp.Router, method, target string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	router.Mux.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Code
}

func TestGetProps(t *testing.T) {
	t.Run("populates the nonce from a request which passed a nonce-generating CSP middleware", func(t *testing.T) {
		props, rec := getProps(t, func(opts *httph.ContentSecurityPolicyOptions) {
			opts.ScriptNonce = true
		})

		is.True(t, props.Nonce != "", "the nonce should not be empty")
		is.True(t, strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self' 'nonce-"+props.Nonce+"'"))
	})

	t.Run("leaves the nonce empty for a CSP middleware which generates none", func(t *testing.T) {
		props, _ := getProps(t, func(opts *httph.ContentSecurityPolicyOptions) {})

		is.Equal(t, "", props.Nonce)
	})

	t.Run("leaves the nonce empty with no CSP middleware at all", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		is.Equal(t, "", gluehttp.GetProps(rec, req).Nonce)
	})
}

// getProps as the given CSP middleware options leave them, from inside a handler below that middleware.
func getProps(t *testing.T, optsFunc func(opts *httph.ContentSecurityPolicyOptions)) (html.PageProps, *httptest.ResponseRecorder) {
	t.Helper()

	var props html.PageProps
	h := httph.ContentSecurityPolicy(optsFunc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		props = gluehttp.GetProps(w, r)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	return props, rec
}
