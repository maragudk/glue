package html

import (
	"fmt"
	"net/url"

	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/html"
)

func Pagination(href string, total, limit, offset int) Node {
	totalPages := (total + limit - 1) / limit
	currentPage := offset/limit + 1

	return Nav(Class("flex items-center justify-center gap-1"),
		Map(generatePageNumbers(currentPage, totalPages), func(page int) Node {
			if page == -1 {
				return PaginationButtonRange()
			}

			if page == currentPage {
				return PaginationButtonCurrent(page)
			}

			offset := (page - 1) * limit
			vs := url.Values{}
			vs.Set("offset", fmt.Sprint(offset))
			vs.Set("limit", fmt.Sprint(limit))
			return PaginationButtonNavigate(href+vs.Encode(), page)
		}),
	)
}

func PaginationButtonCurrent(page int) Node {
	return Span(Class("px-3 py-2 text-sm font-medium text-white bg-primary-600 rounded-md min-w-12 text-center"), Textf("%d", page))
}

func PaginationButtonNavigate(href string, page int) Node {
	return A(Href(href), Class("px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-100 rounded-md w-12 text-center"), Textf("%d", page))
}

func PaginationButtonRange() Node {
	return Span(Class("px-3 py-2 text-sm font-medium text-gray-400 w-12 text-center"), Text("…"))
}

func generatePageNumbers(currentPage, totalPages int) []int {
	const maxButtons = 7

	if totalPages <= maxButtons {
		pages := make([]int, totalPages)
		for i := range totalPages {
			pages[i] = i + 1
		}
		return pages
	}

	var pages []int

	// For pages 1-4, show: 1 2 3 4 5 ... last
	if currentPage <= 4 {
		for i := 1; i <= 5; i++ {
			pages = append(pages, i)
		}
		pages = append(pages, -1) // ellipsis
		pages = append(pages, totalPages)
		return pages
	}

	// For last 4 pages, show: 1 ... (last-4) (last-3) (last-2) (last-1) last
	if currentPage >= totalPages-3 {
		pages = append(pages, 1)
		pages = append(pages, -1) // ellipsis
		for i := totalPages - 4; i <= totalPages; i++ {
			pages = append(pages, i)
		}
		return pages
	}

	// For middle pages, show: 1 ... (current-1) current (current+1) ... last
	pages = append(pages, 1)
	pages = append(pages, -1) // ellipsis
	pages = append(pages, currentPage-1)
	pages = append(pages, currentPage)
	pages = append(pages, currentPage+1)
	pages = append(pages, -1) // ellipsis
	pages = append(pages, totalPages)

	return pages
}
