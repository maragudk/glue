package html

import (
	"context"
	"net/http"
	"slices"

	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/components"
	. "maragu.dev/gomponents/html"

	"maragu.dev/glue/model"
)

type PageProps struct {
	Title       string
	Description string
	Ctx         context.Context
	R           *http.Request
	W           http.ResponseWriter
	HideAuth    bool
	UserID      *model.UserID
	Permissions []model.Permission
}

func (p PageProps) HasPermission(perm model.Permission) bool {
	return slices.Contains(p.Permissions, perm)
}

type PageFunc = func(props PageProps, children ...Node) Node

func FavIcons(name string) Node {
	return Group{
		// <link rel="icon" type="image/png" href="/favicon-96x96.png" sizes="96x96" />
		Link(Rel("icon"), Type("image/png"), Href("/favicon-96x96.png"), Attr("sizes", "96x96")),

		// <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
		Link(Rel("icon"), Type("image/svg+xml"), Href("/favicon.svg")),

		// <link rel="shortcut icon" href="/favicon.ico" />
		Link(Rel("shortcut icon"), Href("/favicon.ico")),

		// <link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png" />
		Link(Rel("apple-touch-icon"), Attr("sizes", "180x180"), Href("/apple-touch-icon.png")),

		// <meta name="apple-mobile-web-app-title" content="name" />
		Meta(Name("apple-mobile-web-app-title"), Content(name)),

		// <link rel="manifest" href="/manifest.json" />
		Link(Rel("manifest"), Href("/manifest.json")),
	}
}

func Container(padX, padY bool, children ...Node) Node {
	return Div(
		Classes{
			"max-w-7xl mx-auto":     true,
			"px-4 md:px-8 lg:px-16": padX,
			"py-4 md:py-8":          padY,
		},
		Group(children),
	)
}
