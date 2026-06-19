package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	pkgErrors "github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// OpenIncidentInput is the payload OpenOrUpdateIncident consumes. It separates
// "new incident" fields from "occurrence update" fields so the upsert can do
// the right thing without re-deriving anything from a partially-built model.
type OpenIncidentInput struct {
	RuleID        uuid.UUID
	Fingerprint   string
	Severity      models.Severity
	EvaluationID  uuid.UUID
	Evidence      json.RawMessage // jsonb
	Labels        map[string]string
	OccurredAt    time.Time
}

// OpenIncidentResult describes what OpenOrUpdateIncident actually did so the
// caller can fire the right event (`opened` vs `updated`).
type OpenIncidentResult struct {
	Incident *models.Incident
	Created  bool // true if a new row was inserted; false if an active row was updated
}

// OpenOrUpdateIncident is the dedup-aware write that the EvaluationService
// calls for every FAILING outcome. Semantics:
//
//   - If an active (OPEN / ACKNOWLEDGED) incident exists for (rule_id, fingerprint),
//     it gets UPDATEd in place: last_seen_at, last_evaluation_id, occurrence_count++.
//   - Otherwise a new incident row is INSERTed. If a RESOLVED incident exists
//     for the same fingerprint, its id is recorded as parent_incident_id so the
//     re-open chain stays traceable.
//
// The function uses a transaction with SELECT FOR UPDATE to serialise concurrent
// writers and the partial unique index `incident_active_per_fingerprint` as the
// backstop invariant.
//
// Concurrent first-open: two transactions can both pass the SELECT FOR UPDATE
// when no active row exists yet, then both attempt the INSERT — the unique
// index rejects one with ErrDuplicateKeyValue. We retry the whole transaction:
// the racing writer has committed by then, so the retry's SELECT finds the
// active row and the UPDATE branch handles it. One retry is sufficient because
// the unique index guarantees at most one active row per fingerprint at any
// time; if the retry's SELECT still misses, something is structurally wrong
// and we surface the error.
func (s *Storage) OpenOrUpdateIncident(ctx context.Context, in OpenIncidentInput) (*OpenIncidentResult, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := s.openOrUpdateIncidentOnce(ctx, in)
		if err == nil {
			return result, nil
		}
		if attempt < maxAttempts && errors.Is(err, ErrDuplicateKeyValue) {
			continue
		}
		return nil, err
	}
	// Unreachable — the loop returns on every path.
	return nil, errors.New("OpenOrUpdateIncident: retry budget exhausted")
}

func (s *Storage) openOrUpdateIncidentOnce(ctx context.Context, in OpenIncidentInput) (*OpenIncidentResult, error) {
	var result *OpenIncidentResult
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. Lock + look up the active incident, if any.
		var active models.Incident
		err := tx.NewSelect().
			Model(&active).
			Where("rule_id = ? AND fingerprint = ?", in.RuleID, in.Fingerprint).
			Where("status IN (?, ?)", string(models.IncidentOpen), string(models.IncidentAcknowledged)).
			For("UPDATE").
			Scan(ctx)

		switch {
		case err == nil:
			// 2a. Active exists — update in place.
			_, uerr := tx.NewUpdate().
				Model(&active).
				Set("last_seen_at = ?", in.OccurredAt).
				Set("last_evaluation_id = ?", in.EvaluationID).
				Set("occurrence_count = occurrence_count + 1").
				Set("evidence = ?", in.Evidence).
				Where("id = ?", active.ID).
				Returning("*").
				Exec(ctx)
			if uerr != nil {
				return e("update incident", uerr)
			}
			result = &OpenIncidentResult{Incident: &active, Created: false}
			return nil

		case errors.Is(err, sql.ErrNoRows):
			// 2b. No active — INSERT new. Set parent_incident_id from the most
			//     recent RESOLVED incident for the same fingerprint, if any.
			// (bun's Scan returns sql.ErrNoRows directly — not the package's
			// ErrNotFound sentinel, which only appears after the e() wrapper.)
			var parentID *uuid.UUID
			var prior models.Incident
			perr := tx.NewSelect().
				Model(&prior).
				Where("rule_id = ? AND fingerprint = ? AND status = ?", in.RuleID, in.Fingerprint, string(models.IncidentResolved)).
				Order("opened_at DESC").
				Limit(1).
				Scan(ctx)
			if perr == nil {
				p := prior.ID
				parentID = &p
			} else if !errors.Is(perr, sql.ErrNoRows) {
				return e("lookup parent incident", perr)
			}

			fresh := &models.Incident{
				ID:                uuid.New(),
				RuleID:            in.RuleID,
				Fingerprint:       in.Fingerprint,
				Status:            models.IncidentOpen,
				Severity:          in.Severity,
				OpenedAt:          in.OccurredAt,
				LastSeenAt:        in.OccurredAt,
				OccurrenceCount:   1,
				FirstEvaluationID: in.EvaluationID,
				LastEvaluationID:  in.EvaluationID,
				Evidence:          in.Evidence,
				ParentIncidentID:  parentID,
				Labels:            in.Labels,
			}
			if _, ierr := tx.NewInsert().Model(fresh).Exec(ctx); ierr != nil {
				return e("insert incident", ierr)
			}
			result = &OpenIncidentResult{Incident: fresh, Created: true}
			return nil

		default:
			return e("lookup active incident", err)
		}
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListActiveIncidentFingerprints returns the fingerprints of every OPEN /
// ACKNOWLEDGED incident for the rule. The evaluation service uses this to
// auto-resolve incidents whose fingerprint disappears from the next round of
// outcomes — e.g. when both sides of a `ledger_vs_pool_drift` invariant clear
// to zero and the asset is no longer in either source's balance map. Without
// this sweep the active incident would stay OPEN forever even though the
// underlying condition cleared.
func (s *Storage) ListActiveIncidentFingerprints(ctx context.Context, ruleID uuid.UUID) ([]string, error) {
	var fps []string
	err := s.db.NewSelect().
		Model((*models.Incident)(nil)).
		Column("fingerprint").
		Where("rule_id = ?", ruleID).
		Where("status IN (?, ?)", string(models.IncidentOpen), string(models.IncidentAcknowledged)).
		Scan(ctx, &fps)
	if err != nil {
		return nil, e("list active incident fingerprints", err)
	}
	return fps, nil
}

// AutoResolveIncident closes the active incident (if any) for (rule_id, fingerprint)
// with resolution.kind = "auto". No-op if no active incident exists.
// Returns the resolved incident or nil when nothing to do.
func (s *Storage) AutoResolveIncident(ctx context.Context, ruleID uuid.UUID, fingerprint string, evaluationID uuid.UUID, at time.Time) (*models.Incident, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	resolution := &models.Resolution{
		Kind: models.ResolutionAuto,
		By:   "system",
		At:   at,
	}
	var inc models.Incident
	res, err := s.db.NewUpdate().
		Model(&inc).
		Set("status = ?", string(models.IncidentResolved)).
		Set("resolution = ?", resolution).
		Set("last_evaluation_id = ?", evaluationID).
		Where("rule_id = ? AND fingerprint = ?", ruleID, fingerprint).
		Where("status IN (?, ?)", string(models.IncidentOpen), string(models.IncidentAcknowledged)).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, e("auto-resolve incident", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	return &inc, nil
}

// AckIncident transitions OPEN → ACKNOWLEDGED with the supplied Ack metadata.
// Idempotent: re-ACK of an already-ACKNOWLEDGED incident returns the existing
// row UNCHANGED — the original ack metadata (who/when/why) is the audit trail
// and must not be overwritten by a second ack call. Rejects on RESOLVED.
func (s *Storage) AckIncident(ctx context.Context, id uuid.UUID, ack *models.Ack) (*models.Incident, error) {
	var inc models.Incident
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock the row first so a concurrent ack can't sneak in between
		// the read and the conditional update.
		if err := tx.NewSelect().
			Model(&inc).
			Where("id = ?", id).
			For("UPDATE").
			Scan(ctx); err != nil {
			return e("ack incident", err)
		}
		if inc.Status == models.IncidentResolved {
			return e("ack incident", ErrNotFound)
		}
		if inc.Status == models.IncidentAcknowledged {
			// No-op: preserve the original ack metadata.
			return nil
		}
		// inc.Status == OPEN → transition.
		if _, err := tx.NewUpdate().
			Model(&inc).
			Set("status = ?", string(models.IncidentAcknowledged)).
			Set("ack = ?", ack).
			Where("id = ?", id).
			Returning("*").
			Exec(ctx); err != nil {
			return e("ack incident", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &inc, nil
}

// ResolveIncidentManual closes the incident with kind = "fixed_by_booking" (or
// "auto" if called by the system path — see AutoResolveIncident for that variant).
// Rejects on already-resolved incidents.
func (s *Storage) ResolveIncidentManual(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Incident, error) {
	if resolution.Kind != models.ResolutionFixedByBooking && resolution.Kind != models.ResolutionAuto {
		return nil, fmt.Errorf("ResolveIncidentManual: unsupported resolution kind %q", resolution.Kind)
	}
	return s.applyResolution(ctx, id, resolution)
}

// AcceptIncident closes the incident with kind = "accepted_by_business". Note is
// required; the service layer enforces evidence-snapshot capture.
func (s *Storage) AcceptIncident(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Incident, error) {
	if resolution.Kind != models.ResolutionAcceptedByBusiness {
		return nil, fmt.Errorf("AcceptIncident: expected kind %q, got %q", models.ResolutionAcceptedByBusiness, resolution.Kind)
	}
	if resolution.Note == "" {
		return nil, fmt.Errorf("AcceptIncident: note is required")
	}
	return s.applyResolution(ctx, id, resolution)
}

func (s *Storage) applyResolution(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Incident, error) {
	var inc models.Incident
	res, err := s.db.NewUpdate().
		Model(&inc).
		Set("status = ?", string(models.IncidentResolved)).
		Set("resolution = ?", resolution).
		Where("id = ?", id).
		Where("status IN (?, ?)", string(models.IncidentOpen), string(models.IncidentAcknowledged)).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, e("resolve incident", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, e("resolve incident", ErrNotFound)
	}
	return &inc, nil
}

// GetIncident returns the incident by id or ErrNotFound.
func (s *Storage) GetIncident(ctx context.Context, id uuid.UUID) (*models.Incident, error) {
	var inc models.Incident
	err := s.db.NewSelect().Model(&inc).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, e("get incident", err)
	}
	return &inc, nil
}

func (s *Storage) buildIncidentListQuery(selectQuery *bun.SelectQuery, where string, args []any) *bun.SelectQuery {
	selectQuery = selectQuery.Order("opened_at DESC")
	if where != "" {
		return selectQuery.Where(where, args...)
	}
	return selectQuery
}

// ListIncidents returns a cursor-paginated set. Filters: id, ruleID, status,
// severity, fingerprint, openedAt, plus label-based filtering (V1 GA wiring).
func (s *Storage) ListIncidents(ctx context.Context, q GetIncidentsQuery) (*bunpaginate.Cursor[models.Incident], error) {
	var (
		where string
		args  []any
		err   error
	)
	if q.Options.QueryBuilder != nil {
		where, args, err = s.incidentQueryContext(q.Options.QueryBuilder)
		if err != nil {
			return nil, err
		}
	}
	return paginateWithOffset[PaginatedQueryOptions[IncidentsFilters], models.Incident](s, ctx,
		(*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[IncidentsFilters]])(&q),
		func(query *bun.SelectQuery) *bun.SelectQuery {
			return s.buildIncidentListQuery(query, where, args)
		},
	)
}

func (s *Storage) incidentQueryContext(qb query.Builder) (string, []any, error) {
	return qb.Build(query.ContextFn(func(key, operator string, value any) (string, []any, error) {
		switch key {
		case "id", "status", "severity", "fingerprint":
			if operator != "$match" {
				return "", nil, pkgErrors.Wrap(ErrInvalidQuery, key+" can only be used with $match")
			}
			return fmt.Sprintf("%s = ?", key), []any{value}, nil
		case "ruleID":
			if operator != "$match" {
				return "", nil, pkgErrors.Wrap(ErrInvalidQuery, "'ruleID' can only be used with $match")
			}
			return "rule_id = ?", []any{value}, nil
		case "openedAt", "lastSeenAt":
			col := map[string]string{"openedAt": "opened_at", "lastSeenAt": "last_seen_at"}[key]
			return fmt.Sprintf("%s %s ?", col, query.DefaultComparisonOperatorsMapping[operator]), []any{value}, nil
		default:
			return "", nil, pkgErrors.Wrapf(ErrInvalidQuery, "unknown key '%s' when building incident query", key)
		}
	}))
}

type IncidentsFilters struct{}

type GetIncidentsQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[IncidentsFilters]]

func NewGetIncidentsQuery(opts PaginatedQueryOptions[IncidentsFilters]) GetIncidentsQuery {
	return GetIncidentsQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}
