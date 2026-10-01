package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-tangra/go-tangra/v4/listquery"
)

// parseList reads page, page_size, sort and order for spec. An invalid value
// answers 422 validation_failed naming the parameter (never its value) and
// reports false.
func parseList(w http.ResponseWriter, r *http.Request, spec listquery.Spec) (listquery.Request, bool) {
	req, err := listquery.Parse(r.URL.Query(), spec)
	if err != nil {
		param := "page"
		var le *listquery.Error
		if errors.As(err, &le) {
			param = le.Param
		}
		failParam(w, param)
		return req, false
	}
	return req, true
}

// failParam writes {"reason":"validation_failed","detail":{"param":…}}.
func failParam(w http.ResponseWriter, param string) {
	WriteJSON(w, ErrValidation.Status, map[string]any{"reason": ErrValidation.Reason, "detail": map[string]string{"param": param}})
}

// listParams are the paging query parameters whose refusals name the
// parameter (contracts/http-list.md).
var listParams = map[string]bool{"page": true, "page_size": true, "sort": true, "order": true}

// listPage is the HTTP list response (listquery.Page, clamped to total).
func listPage[T any](items []T, total int, req listquery.Request) listquery.Page[T] {
	return listquery.NewPage(items, total, req.Clamp(total))
}
