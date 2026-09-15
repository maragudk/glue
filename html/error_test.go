package html_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	"maragu.dev/glue/model"
)

func TestErrorPage(t *testing.T) {
	t.Run("passes the given props through to the page function, but sets the title and clears the description", func(t *testing.T) {
		testPagePassesPropsThrough(t, html.ErrorPage, "Something went wrong")
	})
}

func TestNotFoundPage(t *testing.T) {
	t.Run("passes the given props through to the page function, but sets the title and clears the description", func(t *testing.T) {
		testPagePassesPropsThrough(t, html.NotFoundPage, "Not found")
	})
}

// testPagePassesPropsThrough renders the given error page with every field of [html.PageProps] set, and
// checks that the page function sees the expected title, an empty description, and all the other fields
// unchanged.
func testPagePassesPropsThrough(t *testing.T, errorPage func(page html.PageFunc, props html.PageProps) g.Node, expectedTitle string) {
	t.Helper()

	var got html.PageProps
	page := func(props html.PageProps, children ...g.Node) g.Node {
		got = props
		return g.Group(children)
	}

	userID := model.UserID("u_123")
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	props := html.PageProps{
		Title:       "This gets overridden",
		Description: "A description",
		Ctx:         t.Context(),
		R:           req,
		W:           rec,
		HideAuth:    true,
		UserID:      &userID,
		Permissions: []model.Permission{"read"},
	}

	var b strings.Builder
	is.NotError(t, errorPage(page, props).Render(&b))

	is.Equal(t, expectedTitle, got.Title)
	is.Equal(t, "", got.Description)
	is.Equal(t, t.Context(), got.Ctx)
	is.Equal(t, req, got.R)
	is.True(t, got.W == rec, "the response writer should be passed through")
	is.True(t, got.HideAuth, "HideAuth should be passed through")
	is.Equal(t, &userID, got.UserID)
	is.EqualSlice(t, []model.Permission{"read"}, got.Permissions)
	is.True(t, strings.Contains(b.String(), "<h1>"+expectedTitle+"</h1>"))
}
