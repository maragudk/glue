package http

import (
	"net/http"

	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/http"
	"maragu.dev/httph"

	"maragu.dev/gloo/html"
)

func NotFound(page html.PageFunc) http.HandlerFunc {
	return Adapt(func(w http.ResponseWriter, r *http.Request) (Node, error) {
		return html.NotFoundPage(page), httph.HTTPError{Code: http.StatusNotFound}
	})
}
