package api

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/formancehq/go-libs/bun/bunpaginate"
)

const (
	MaxPageSize     = 100
	DefaultPageSize = bunpaginate.QueryDefaultPageSize

	QueryKeyCursor   = "cursor"
	QueryKeyPageSize = "pageSize"
	QueryKeyQuery    = "query"
	QueryKeyPeriod   = "period"
	QueryKeyScope    = "scope"
	QueryKeyLimit    = "limit"
	QueryKeyPrefix   = "prefix"
	QueryKeyFilter   = "filter"
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

// validateQueryParams rejects a malformed query string and every query
// parameter outside allowed. A list endpoint that ignored one returned the whole
// list, so a typo or an unsupported filter such as ?status=OPEN looked like a
// filtered answer. It parses rawQuery itself because url.URL.Query drops a
// malformed pair, such as status=%ZZ, without an error.
func validateQueryParams(rawQuery string, allowed ...string) error {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return fmt.Errorf("invalid query string: %w", err)
	}

	var unknown []string
	for _, key := range slices.Sorted(maps.Keys(q)) {
		if !slices.Contains(allowed, key) {
			unknown = append(unknown, strconv.Quote(key))
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	noun := "parameter"
	if len(unknown) > 1 {
		noun = "parameters"
	}

	return fmt.Errorf("unknown query %s %s: this endpoint %s", noun, strings.Join(unknown, ", "), describeAllowedParams(allowed))
}

// describeAllowedParams names the accepted parameters in sorted order, with a
// serial comma.
func describeAllowedParams(allowed []string) string {
	names := slices.Sorted(slices.Values(allowed))
	switch len(names) {
	case 0:
		return "reads no query parameters"
	case 1:
		return "reads only " + names[0]
	case 2:
		return "reads only " + names[0] + " and " + names[1]
	default:
		return "reads only " + strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}
