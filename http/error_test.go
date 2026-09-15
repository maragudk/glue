package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	gluehttp "maragu.dev/glue/http"
	"maragu.dev/glue/model"
)

func TestNotFound(t *testing.T) {
	t.Run("renders the not-found page with the request's props", func(t *testing.T) {
		var props html.PageProps
		h := gluehttp.NotFound(func(p html.PageProps, children ...g.Node) g.Node {
			props = p
			return g.Group(children)
		})

		userID := model.UserID("u_123")
		req := httptest.NewRequest(http.MethodGet, "/nope", nil)
		ctx := context.WithValue(req.Context(), gluehttp.ContextKey("userID"), &userID)
		ctx = context.WithValue(ctx, gluehttp.ContextKey("permissions"), []model.Permission{"read"})
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		h(rec, req)

		is.Equal(t, http.StatusNotFound, rec.Code)
		is.Equal(t, "Not found", props.Title)
		is.Equal(t, &userID, props.UserID)
		is.EqualSlice(t, []model.Permission{"read"}, props.Permissions)
		is.Equal(t, req, props.R)
		is.Equal(t, req.Context(), props.Ctx)
		is.True(t, props.W == rec, "the response writer should reach the page function")
		is.True(t, strings.Contains(rec.Body.String(), "<h1>Not found</h1>"))
	})
}
