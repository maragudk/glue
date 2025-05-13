package html

import (
	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/html"
)

func ErrorPage(page PageFunc) Node {
	return page(PageProps{Title: "Something went wrong"},
		H1(Text("Something went wrong")),
	)
}

func NotFoundPage(page PageFunc) Node {
	return page(PageProps{Title: "Not found"},
		H1(Text("Not found")),
	)
}
