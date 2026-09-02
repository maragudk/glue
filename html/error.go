package html

import (
	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/html"
)

// ErrorPage rendered with the given page function, from props describing the request that failed.
// The title is set here, and the rest of the props reach the page function unchanged, so an error page
// is rendered from the same nonce, user and permissions as any other page.
func ErrorPage(page PageFunc, props PageProps) Node {
	props.Title = "Something went wrong"

	return page(props,
		H1(Text("Something went wrong")),
	)
}

// NotFoundPage rendered with the given page function, from props describing the request that matched
// nothing. See [ErrorPage] for how the props are treated.
func NotFoundPage(page PageFunc, props PageProps) Node {
	props.Title = "Not found"

	return page(props,
		H1(Text("Not found")),
	)
}
