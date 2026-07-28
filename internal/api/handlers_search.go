package api

import (
	"net/http"
	"strconv"

	"github.com/gordon2/manualbox/internal/registry"
)

// handleSearch answers the question README puts first: which manual says X, and
// where.
//
// # Why the query is a parameter and not a body
//
// It is a GET with `?q=`, so a search is a URL: linkable, bookmarkable, and back in
// the browser history where a user expects it. A POST with a JSON body would hide
// the query from all three.
//
// # What the response says beyond the hits
//
// `mode` is which path answered -- the FTS5 index, or the substring scan that
// covers the queries a trigram index cannot represent. `truncated` says the limit
// cut the list off. `indexed` appears only when nothing matched, and it is the
// difference between "no manual says that" and "no manual has been converted yet",
// which are the same empty list otherwise. Each hit carries `bm25` and `score`
// because the gap between them is a judgement about headings, and a number in a
// response can be argued with in a way that one buried in an ORDER BY cannot.
//
// `?documentId=` narrows to one manual, which is what a reader already inside a
// document asks. It is not required: search spans documents by default, because a
// household looking for the descaling interval does not know which manual to open.
// An unknown id is not an error here -- it is a search of nothing, which returns no
// hits, and reporting 404 would turn a scoping parameter into an existence oracle
// on a different route's behalf.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := 0
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > registry.MaxSearchLimit {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit",
				"limit must be a number between 1 and "+strconv.Itoa(registry.MaxSearchLimit)+".")
			return
		}
		limit = n
	}

	results, err := s.deps.Registry.Search(r.Context(), registry.SearchQuery{
		Text:       query.Get("q"),
		DocumentID: query.Get("documentId"),
		Limit:      limit,
	})
	if err != nil {
		// An empty q is registry.ErrInvalid and becomes a 400 with the service's own
		// message, rather than an empty result set that reads as "nothing matched".
		s.writeRegistryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}
