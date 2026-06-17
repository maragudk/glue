package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	g "maragu.dev/gomponents"
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
