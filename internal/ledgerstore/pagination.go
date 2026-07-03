package ledgerstore

import "github.com/formancehq/go-libs/bun/bunpaginate"

// The ledger lists accounts by address with an opaque cursor; recon's Store
// interface is offset-paginated (bunpaginate.OffsetPaginatedQuery) and orders by
// a time column. We bridge the two by fetching the full matching set (bounded by
// the query filter), ordering it client-side, then slicing the requested offset
// window — mirroring bunpaginate.usingOffset over an in-memory slice so the
// emitted cursors round-trip through recon's existing HTTP layer unchanged.
//
// Cost: O(matches) per call, so filtered lists stay cheap but an unfiltered list
// loads its whole result set. A cursor-based Store interface + server-side
// ordering would remove the fetch-all (tracked as F23).

// paginateSlice returns the [offset, offset+pageSize) window of items and whether
// a further page exists.
func paginateSlice[T any](items []T, offset, pageSize uint64) (data []T, hasMore bool) {
	total := uint64(len(items))
	if offset >= total {
		return nil, false
	}

	end := total
	if pageSize > 0 && offset+pageSize < total {
		end = offset + pageSize
		hasMore = true
	}

	return items[offset:end], hasMore
}

// offsetCursor builds the bunpaginate.Cursor for a page, encoding the
// previous/next offset queries exactly as bunpaginate.usingOffset does so recon's
// HTTP layer decodes them with no ledger-specific handling.
func offsetCursor[O, T any](q bunpaginate.OffsetPaginatedQuery[O], data []T, hasMore bool) *bunpaginate.Cursor[T] {
	var previous, next *bunpaginate.OffsetPaginatedQuery[O]

	if q.Offset > 0 {
		cp := q
		if cp.Offset < cp.PageSize {
			cp.Offset = 0
		} else {
			cp.Offset -= cp.PageSize
		}

		previous = &cp
	}

	if hasMore {
		cp := q
		cp.Offset += cp.PageSize
		next = &cp
	}

	return &bunpaginate.Cursor[T]{
		PageSize: int(q.PageSize),
		HasMore:  hasMore,
		Previous: previous.EncodeAsCursor(),
		Next:     next.EncodeAsCursor(),
		Data:     data,
	}
}
