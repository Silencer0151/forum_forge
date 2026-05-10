package handler

import (
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store"
)

const defaultSearchPerPage = 20

// SearchHandlers handles the /search endpoint.
type SearchHandlers struct {
	store    store.Store
	renderer *render.Renderer
}

// NewSearchHandler constructs a SearchHandlers.
func NewSearchHandler(st store.Store, r *render.Renderer) *SearchHandlers {
	return &SearchHandlers{store: st, renderer: r}
}

// SearchResultView is a display-ready search hit. The Snippet field is
// pre-sanitised HTML (FTS5 highlight marks) so templates can render it
// unescaped via template.HTML.
type SearchResultView struct {
	PostID         int64
	ThreadID       int64
	ThreadTitle    string
	Snippet        template.HTML
	AuthorUsername string
	CreatedAt      interface{} // time.Time — kept as interface{} so formatTime works
}

// SearchPaginationData carries pagination state for search result pages.
// It embeds the query string so the template can build correct URLs of the
// form /search?q=term&page=N without relying on the shared pagination partial
// whose BaseURL scheme assumes no pre-existing query parameters.
type SearchPaginationData struct {
	CurrentPage int
	TotalPages  int
	Pages       []int
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	Query       string
}

// SearchPage serves GET /search.
// An HTMX request (HX-Request: true) returns only the search_results partial;
// a plain GET returns the full page. An empty query skips the database and
// renders an empty-state message.
func (h *SearchHandlers) SearchPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	data := BaseData(r)
	data["Title"] = "Search"
	data["Query"] = query

	if query == "" {
		h.respond(w, r, "search.html", data)
		return
	}

	result, err := h.store.Search(ctx, query, store.PageRequest{
		Page:    page,
		PerPage: defaultSearchPerPage,
	})
	if err != nil {
		slog.Error("search", "query", query, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	views := make([]SearchResultView, len(result.Items))
	for i, item := range result.Items {
		views[i] = SearchResultView{
			PostID:         item.PostID,
			ThreadID:       item.ThreadID,
			ThreadTitle:    item.ThreadTitle,
			Snippet:        template.HTML(item.Snippet), // safe: produced by FTS5 highlight + our sanitiser
			AuthorUsername: item.AuthorUsername,
			CreatedAt:      item.CreatedAt,
		}
	}

	data["Results"] = views
	data["Total"] = result.Total
	data["Pagination"] = buildSearchPagination(page, result.TotalPages, query)

	h.respond(w, r, "search.html", data)
}

// respond sends a partial for HTMX requests or a full page for plain requests.
func (h *SearchHandlers) respond(w http.ResponseWriter, r *http.Request, _ string, data map[string]any) {
	var err error
	if render.IsHTMXRequest(r) {
		err = h.renderer.RenderPartial(w, "search_results.html", data)
	} else {
		err = h.renderer.Render(w, "search.html", data)
	}
	if err != nil {
		slog.Error("render search", "error", err)
	}
}

func buildSearchPagination(page, totalPages int, query string) SearchPaginationData {
	// Show at most 5 page numbers centred around the current page.
	start := page - 2
	if start < 1 {
		start = 1
	}
	end := start + 4
	if end > totalPages {
		end = totalPages
		if end-4 > 0 {
			start = end - 4
		} else {
			start = 1
		}
	}
	pages := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	return SearchPaginationData{
		CurrentPage: page,
		TotalPages:  totalPages,
		Pages:       pages,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
		Query:       query,
	}
}
