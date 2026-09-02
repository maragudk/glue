package html_test

import (
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	"maragu.dev/glue/model"
)

// recordingPage returns a page function which renders its children and records the props it was called
// with in the returned pointer.
func recordingPage() (html.PageFunc, *html.PageProps) {
	var got html.PageProps
	return func(props html.PageProps, children ...g.Node) g.Node {
		got = props
		return g.Group(children)
	}, &got
}

func TestErrorPage(t *testing.T) {
	t.Run("passes the given props through to the page function and sets the title", func(t *testing.T) {
		page, got := recordingPage()

		userID := model.UserID("u_123")
		props := html.PageProps{
			Title:       "This gets overridden",
			Description: "A description",
			Nonce:       "abc123",
			UserID:      &userID,
			Permissions: []model.Permission{"read"},
		}

		var b strings.Builder
		is.NotError(t, html.ErrorPage(page, props).Render(&b))

		is.Equal(t, "Something went wrong", got.Title)
		is.Equal(t, "A description", got.Description)
		is.Equal(t, "abc123", got.Nonce)
		is.Equal(t, &userID, got.UserID)
		is.EqualSlice(t, []model.Permission{"read"}, got.Permissions)
		is.True(t, strings.Contains(b.String(), "<h1>Something went wrong</h1>"))
	})
}

func TestNotFoundPage(t *testing.T) {
	t.Run("passes the given props through to the page function and sets the title", func(t *testing.T) {
		page, got := recordingPage()

		userID := model.UserID("u_123")
		props := html.PageProps{
			Title:       "This gets overridden",
			Description: "A description",
			Nonce:       "abc123",
			UserID:      &userID,
			Permissions: []model.Permission{"read"},
		}

		var b strings.Builder
		is.NotError(t, html.NotFoundPage(page, props).Render(&b))

		is.Equal(t, "Not found", got.Title)
		is.Equal(t, "A description", got.Description)
		is.Equal(t, "abc123", got.Nonce)
		is.Equal(t, &userID, got.UserID)
		is.EqualSlice(t, []model.Permission{"read"}, got.Permissions)
		is.True(t, strings.Contains(b.String(), "<h1>Not found</h1>"))
	})
}
