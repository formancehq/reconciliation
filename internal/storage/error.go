package storage

import (
	"database/sql"
	"errors"
	"fmt"

	pgconnv4 "github.com/jackc/pgconn"
	pgconnv5 "github.com/jackc/pgx/v5/pgconn"
)

var ErrNotFound = errors.New("not found")
var ErrDuplicateKeyValue = errors.New("duplicate key value")
var ErrInvalidQuery = errors.New("invalid query")
var ErrStaleClaim = errors.New("stale evaluation job claim")
var ErrObsoleteJob = errors.New("evaluation job no longer matches the rule")
var ErrRuleRevisionConflict = errors.New("rule revision changed")

// pgUniqueViolation is SQLSTATE 23505. Both pgx v4's pgconn and pgx v5's
// pgconn use it but expose disjoint error types (different import paths), so
// the check must cover both — bun's stdlib driver returns the v5 type, while
// older paths in the same binary may still surface the v4 one.
const pgUniqueViolation = "23505"

func e(msg string, err error) error {
	if err == nil {
		return nil
	}

	var pgErrV5 *pgconnv5.PgError
	if errors.As(err, &pgErrV5) && pgErrV5.Code == pgUniqueViolation {
		return fmt.Errorf("%s: %w", msg, ErrDuplicateKeyValue)
	}
	var pgErrV4 *pgconnv4.PgError
	if errors.As(err, &pgErrV4) && pgErrV4.Code == pgUniqueViolation {
		return fmt.Errorf("%s: %w", msg, ErrDuplicateKeyValue)
	}

	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", msg, ErrNotFound)
	}

	return fmt.Errorf("%s: %w", msg, err)
}
