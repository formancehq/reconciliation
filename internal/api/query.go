package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/formancehq/go-libs/bun/bunpaginate"
)

const (
	MaxPageSize     = 100
	DefaultPageSize = bunpaginate.QueryDefaultPageSize

	QueryKeyCursor   = "cursor"
	QueryKeyPageSize = "pageSize"
)

var (
	ErrInvalidPageSize = errors.New("invalid 'pageSize' query param")
)

func getPageSize(r *http.Request) (uint64, error) {
	pageSizeParam := r.URL.Query().Get(QueryKeyPageSize)
	if pageSizeParam == "" {
		return DefaultPageSize, nil
	}

	pageSize, err := strconv.ParseUint(pageSizeParam, 10, 32)
	if err != nil || pageSize == 0 {
		// pageSize=0 would disable the SQL LIMIT entirely
		return 0, ErrInvalidPageSize
	}

	if pageSize > MaxPageSize {
		return MaxPageSize, nil
	}

	return pageSize, nil
}

// validateCursorPageSize guards against forged cursors: a page size of 0
// disables the SQL LIMIT and values above MaxPageSize bypass the cap
// enforced by getPageSize.
func validateCursorPageSize(pageSize uint64) error {
	if pageSize == 0 || pageSize > MaxPageSize {
		return ErrInvalidPageSize
	}
	return nil
}
