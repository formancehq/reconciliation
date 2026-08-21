package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/go-libs/api"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
)

// auditEntryResponse is one journal entry as published.
//
// The memento is included on the single-entry read but not on the list: a list
// is for navigating, and shipping every payload would make the common case
// expensive. On the single read it is base64 rather than parsed JSON, because
// what a verifier needs is the exact bytes that were hashed — re-serialising
// through a JSON view would defeat the purpose.
type auditEntryResponse struct {
	Sequence      int64           `json:"sequence"`
	At            time.Time       `json:"at"`
	Kind          string          `json:"kind"`
	RuleID        *string         `json:"ruleID,omitempty"`
	RuleRevision  *int64          `json:"ruleRevision,omitempty"`
	AlertID       *string         `json:"alertID,omitempty"`
	EvaluationID  *string         `json:"evaluationID,omitempty"`
	PeriodID      string          `json:"periodID,omitempty"`
	Subject       subjectResponse `json:"subject"`
	MementoDigest string          `json:"mementoDigest"`
	PrevHash      string          `json:"prevHash,omitempty"`
	Hash          string          `json:"hash"`
	HashVersion   int             `json:"hashVersion"`
	Memento       string          `json:"memento,omitempty"`
	MementoJSON   json.RawMessage `json:"mementoJSON,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// subjectResponse renders attribution. Actor is the human-readable form; the
// structured fields are what actually entered the hash, and `system` is called
// out explicitly because "was this a machine or a person" is the first question
// asked of any control record.
type subjectResponse struct {
	Actor       string   `json:"actor"`
	Subject     string   `json:"subject,omitempty"`
	Source      string   `json:"source,omitempty"`
	SourceValue string   `json:"sourceValue,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	System      bool     `json:"system"`
}

func renderSubject(s models.Subject) subjectResponse {
	return subjectResponse{
		Actor:       s.Display(),
		Subject:     s.Subject,
		Source:      string(s.Source),
		SourceValue: s.SourceValue,
		Scopes:      s.Scopes,
		System:      s.IsSystem(),
	}
}

func renderAuditEntry(e *models.AuditEntry, withMemento bool) *auditEntryResponse {
	out := &auditEntryResponse{
		Sequence:      e.Sequence,
		At:            e.At,
		Kind:          string(e.Kind),
		RuleRevision:  e.RuleRevision,
		PeriodID:      e.PeriodID,
		Subject:       renderSubject(e.Subject),
		MementoDigest: hex.EncodeToString(e.MementoDigest),
		PrevHash:      hex.EncodeToString(e.PrevHash),
		Hash:          hex.EncodeToString(e.Hash),
		HashVersion:   e.HashVersion,
		CreatedAt:     e.CreatedAt,
	}
	if e.RuleID != nil {
		out.RuleID = stringPtr(e.RuleID.String())
	}
	if e.AlertID != nil {
		out.AlertID = stringPtr(e.AlertID.String())
	}
	if e.EvaluationID != nil {
		out.EvaluationID = stringPtr(e.EvaluationID.String())
	}
	if withMemento {
		out.Memento = base64.StdEncoding.EncodeToString(e.Memento)
		// The parsed form is a convenience for reading; the base64 above is the
		// authoritative one for verifying.
		out.MementoJSON = json.RawMessage(e.Memento)
	}
	return out
}

func stringPtr(s string) *string { return &s }

type auditEntriesResponse struct {
	Data []*auditEntryResponse `json:"data"`
	// Next is the sequence to resume after. Absent when the page is the last one.
	Next *int64 `json:"next,omitempty"`
	// Head is where the journal ends right now, so a client can tell how far it
	// still has to walk without a second call.
	Head     int64  `json:"head"`
	HeadHash string `json:"headHash,omitempty"`
}

func listAuditEntriesHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filters, afterSeq, limit, err := parseAuditFilters(r)
		if err != nil {
			api.BadRequest(w, ErrValidation, err)
			return
		}

		entries, next, err := b.GetService().ListAuditEntries(r.Context(), filters, afterSeq, limit)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		head, headHash, err := b.GetService().ChainHead(r.Context())
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}

		out := &auditEntriesResponse{
			Data:     make([]*auditEntryResponse, 0, len(entries)),
			Head:     head,
			HeadHash: hex.EncodeToString(headHash),
		}
		for i := range entries {
			out.Data = append(out.Data, renderAuditEntry(&entries[i], false))
		}
		if next > 0 {
			out.Next = &next
		}
		api.Ok(w, out)
	}
}

func getAuditEntryHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sequence, err := strconv.ParseInt(chi.URLParam(r, "sequence"), 10, 64)
		if err != nil || sequence <= 0 {
			api.BadRequest(w, ErrValidation, fmt.Errorf("sequence must be a positive integer"))
			return
		}
		entry, err := b.GetService().GetAuditEntry(r.Context(), sequence)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAuditEntry(entry, true))
	}
}

// verifyChainRequest bounds a verification walk. An empty body verifies
// everything, which is the request an auditor actually makes.
type verifyChainRequest struct {
	FromSequence int64 `json:"fromSequence,omitempty"`
	ToSequence   int64 `json:"toSequence,omitempty"`
	// PeriodID verifies exactly the range a period seal covers — the shortcut for
	// "prove May was not touched after we closed it".
	PeriodID string `json:"periodID,omitempty"`
}

type verifyChainResponse struct {
	OK            bool     `json:"ok"`
	FirstSequence int64    `json:"firstSequence"`
	LastSequence  int64    `json:"lastSequence"`
	EntriesWalked int64    `json:"entriesWalked"`
	Violation     string   `json:"violation,omitempty"`
	AtSequence    *int64   `json:"atSequence,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	SealsCrossed  []string `json:"sealsCrossed,omitempty"`
}

func verifyChainHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := verifyChainRequest{}
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
		}

		from, to := req.FromSequence, req.ToSequence
		if req.PeriodID != "" {
			if from != 0 || to != 0 {
				api.BadRequest(w, ErrValidation,
					fmt.Errorf("give either periodID or an explicit sequence range, not both"))
				return
			}
			// A business period can span more than one closing, so the range to
			// verify runs from the first attesting closure's start to the last
			// one's boundary. Narrowing to a single closure would answer a
			// question about part of the period while appearing to answer about
			// all of it.
			out, err := b.GetService().AttestationsForPeriod(r.Context(), req.PeriodID)
			if err != nil {
				handleServiceErrors(w, r, err)
				return
			}
			from = out[0].Closure.FirstSequence
			for i := range out {
				if last := out[i].Closure.LastSequence; last != nil && *last > to {
					to = *last
				}
			}

			// A closure that covered nothing stores lastSequence = firstSequence - 1,
			// which for the very first one is 0. Forwarding that would read as "no
			// upper bound given" and verify the whole journal instead — answering a
			// different question than the one asked.
			//
			// An empty range is NOT automatically intact: there are no entries to
			// recompute, but the closure itself can still have been edited. The walk
			// re-derives each closure it crosses, and with nothing walked it crosses
			// none, so they are checked directly here.
			if to < from {
				resp := &verifyChainResponse{OK: true, FirstSequence: from, LastSequence: to}
				for i := range out {
					_, ok, reason, err := b.GetService().VerifyClosure(r.Context(), out[i].Closure.ID)
					if err != nil {
						handleServiceErrors(w, r, err)
						return
					}
					resp.SealsCrossed = append(resp.SealsCrossed, req.PeriodID)
					if !ok {
						resp.OK = false
						resp.Violation = string(models.ChainViolationHashMismatch)
						resp.Detail = reason
						break
					}
				}
				api.Ok(w, resp)
				return
			}
		}

		result, err := b.GetService().VerifyChain(r.Context(), from, to)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, &verifyChainResponse{
			OK:            result.OK,
			FirstSequence: result.FirstSequence,
			LastSequence:  result.LastSequence,
			EntriesWalked: result.EntriesWalked,
			Violation:     string(result.Violation),
			AtSequence:    result.AtSequence,
			Detail:        result.Detail,
			SealsCrossed:  result.SealsCrossed,
		})
	}
}

func parseAuditFilters(r *http.Request) (storage.AuditEntryFilters, int64, int, error) {
	var (
		f        storage.AuditEntryFilters
		afterSeq int64
		limit    int
	)
	q := r.URL.Query()

	if raw := q.Get("kind"); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			kind := models.AuditEntryKind(strings.TrimSpace(k))
			switch kind {
			case models.AuditRuleCreated, models.AuditRuleRevised, models.AuditRuleDeleted,
				models.AuditEvaluationCommitted, models.AuditAlertTransition, models.AuditPeriodSealed:
				f.Kinds = append(f.Kinds, kind)
			default:
				return f, 0, 0, fmt.Errorf("unknown kind %q", k)
			}
		}
	}

	for param, dst := range map[string]**uuid.UUID{
		"ruleID":       &f.RuleID,
		"alertID":      &f.AlertID,
		"evaluationID": &f.EvaluationID,
	} {
		if raw := q.Get(param); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				return f, 0, 0, fmt.Errorf("invalid %s: %w", param, err)
			}
			*dst = &id
		}
	}

	f.PeriodID = q.Get("periodID")
	f.Subject = q.Get("subject")

	switch actor := q.Get("actor"); actor {
	case "":
	case "system":
		f.SystemOnly = true
	case "human":
		f.HumanOnly = true
	default:
		return f, 0, 0, fmt.Errorf("actor must be 'system' or 'human'")
	}

	for param, dst := range map[string]*int64{
		"fromSequence": &f.FromSeq,
		"toSequence":   &f.ToSeq,
		"after":        &afterSeq,
	} {
		if raw := q.Get(param); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v < 0 {
				return f, 0, 0, fmt.Errorf("%s must be a non-negative integer", param)
			}
			*dst = v
		}
	}

	for param, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if raw := q.Get(param); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return f, 0, 0, fmt.Errorf("%s must be an RFC3339 timestamp", param)
			}
			*dst = &t
		}
	}

	if raw := q.Get("pageSize"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 1000 {
			return f, 0, 0, fmt.Errorf("pageSize must be between 1 and 1000")
		}
		limit = v
	}
	return f, afterSeq, limit, nil
}

// --- closures ---

type closurePeriodResponse struct {
	PeriodID        string `json:"periodID"`
	EntryCount      int64  `json:"entryCount"`
	AlertCount      int64  `json:"alertCount"`
	UnresolvedCount int64  `json:"unresolvedCount"`
	StateHash       string `json:"stateHash"`
	// Ended says whether the period's calendar span was over at closing time, and
	// Frozen whether its books stopped accepting writes as a result. A closure
	// running mid-period attests what it saw without freezing it.
	Ended  bool `json:"ended"`
	Frozen bool `json:"frozen"`
}

type closureResponse struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	OpenedAt string `json:"openedAt"`
	ClosedAt string `json:"closedAt,omitempty"`

	FirstSequence int64  `json:"firstSequence"`
	LastSequence  *int64 `json:"lastSequence,omitempty"`
	EntryCount    int64  `json:"entryCount"`
	LastAuditHash string `json:"lastAuditHash,omitempty"`

	// Periods is the per-business-period breakdown, and is what an auditor
	// actually reads. StateHash covers it, and the signature covers StateHash.
	Periods   []closurePeriodResponse `json:"periods"`
	StateHash string                  `json:"stateHash,omitempty"`
	// SealingHash is the single value that stands for the whole closure. It is
	// unkeyed, so an auditor can recompute it from the fields above.
	SealingHash string `json:"sealingHash,omitempty"`
	// Signature is what makes the closure checkable without trusting this service.
	Signature     string          `json:"signature,omitempty"`
	SigningKeyID  string          `json:"signingKeyID,omitempty"`
	ClosedBy      subjectResponse `json:"closedBy"`
	AuditSequence *int64          `json:"auditSequence,omitempty"`
}

func renderClosure(closure *models.Closure) *closureResponse {
	periods := make([]closurePeriodResponse, 0, len(closure.Periods))
	for _, p := range closure.Periods {
		periods = append(periods, closurePeriodResponse{
			PeriodID:        p.PeriodID,
			EntryCount:      p.EntryCount,
			AlertCount:      p.AlertCount,
			UnresolvedCount: p.UnresolvedCount,
			StateHash:       p.StateHash,
			Ended:           p.Ended,
			Frozen:          p.Frozen,
		})
	}
	out := &closureResponse{
		ID:            closure.ID,
		Status:        string(closure.Status),
		OpenedAt:      closure.OpenedAt.Format(time.RFC3339Nano),
		FirstSequence: closure.FirstSequence,
		LastSequence:  closure.LastSequence,
		EntryCount:    closure.EntryCount,
		LastAuditHash: hex.EncodeToString(closure.LastAuditHash),
		Periods:       periods,
		StateHash:     hex.EncodeToString(closure.StateHash),
		SealingHash:   hex.EncodeToString(closure.SealingHash),
		Signature:     base64.StdEncoding.EncodeToString(closure.Signature),
		SigningKeyID:  closure.SigningKeyID,
		ClosedBy:      renderSubject(closure.ClosedBy),
		AuditSequence: closure.AuditSequence,
	}
	if closure.ClosedAt != nil {
		out.ClosedAt = closure.ClosedAt.Format(time.RFC3339Nano)
	}
	return out
}

func listClosuresHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		closures, err := b.GetService().ListClosures(r.Context())
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		out := make([]*closureResponse, 0, len(closures))
		for i := range closures {
			out = append(out, renderClosure(&closures[i]))
		}
		api.Ok(w, out)
	}
}

func closeJournalHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		closure, err := b.GetService().CloseJournal(r.Context())
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Created(w, renderClosure(closure))
	}
}

type verifyClosureResponse struct {
	ClosureID int64            `json:"closureID"`
	OK        bool             `json:"ok"`
	Reason    string           `json:"reason,omitempty"`
	Closure   *closureResponse `json:"closure"`
}

func verifyClosureHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "closureID"), 10, 64)
		if err != nil {
			api.BadRequest(w, ErrValidation, fmt.Errorf("closure id must be a number"))
			return
		}
		closure, ok, reason, err := b.GetService().VerifyClosure(r.Context(), id)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, &verifyClosureResponse{
			ClosureID: id,
			OK:        ok,
			Reason:    reason,
			Closure:   renderClosure(closure),
		})
	}
}

// --- business periods ---
//
// The read path still speaks in business periods, even though closing no longer
// does. That asymmetry is deliberate: an auditor asks "what do you attest for
// May", and a period id carries a meaning no operational boundary can — it comes
// from the evaluation's point-in-time, so a backfill of May run in August
// belongs to May.

type periodAttestationResponse struct {
	PeriodID string `json:"periodID"`
	Status   string `json:"status"`
	// Attestations is usually one entry. More than one means the period's evidence
	// was recorded across several closings — a period spanning a boundary, or a
	// backfill landing long after the fact — and an auditor needs all of them
	// rather than the most convenient one.
	Attestations []periodAttestationEntry `json:"attestations"`
}

type periodAttestationEntry struct {
	Period  closurePeriodResponse `json:"period"`
	Closure *closureResponse      `json:"closure"`
}

func getPeriodHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID := chi.URLParam(r, "periodID")
		out, err := b.GetService().AttestationsForPeriod(r.Context(), periodID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				// A period nothing has attested yet is a legitimate answer, not a
				// missing resource: absence IS the open state, and a 404 would make a
				// client guess whether the period is open or the id is wrong.
				api.Ok(w, &periodAttestationResponse{
					PeriodID:     periodID,
					Status:       string(models.PeriodOpen),
					Attestations: []periodAttestationEntry{},
				})
				return
			}
			handleServiceErrors(w, r, err)
			return
		}

		entries := make([]periodAttestationEntry, 0, len(out))
		frozen := false
		for i := range out {
			p := out[i].Period
			if p.Frozen {
				frozen = true
			}
			entries = append(entries, periodAttestationEntry{
				Period: closurePeriodResponse{
					PeriodID:        p.PeriodID,
					EntryCount:      p.EntryCount,
					AlertCount:      p.AlertCount,
					UnresolvedCount: p.UnresolvedCount,
					StateHash:       p.StateHash,
					Ended:           p.Ended,
					Frozen:          p.Frozen,
				},
				Closure: renderClosure(out[i].Closure),
			})
		}
		status := models.PeriodOpen
		if frozen {
			status = models.PeriodSealed
		}
		api.Ok(w, &periodAttestationResponse{
			PeriodID:     periodID,
			Status:       string(status),
			Attestations: entries,
		})
	}
}

// --- closing schedule ---

type closingScheduleResponse struct {
	// Cron is empty when rotation is manual. Kept as a plain string rather than a
	// structured cadence so the operator sees exactly what will fire.
	Cron string `json:"cron"`
}

func getClosingScheduleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spec, err := b.GetService().GetClosingSchedule(r.Context())
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, &closingScheduleResponse{Cron: spec})
	}
}

func setClosingScheduleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req closingScheduleResponse
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		if err := b.GetService().SetClosingSchedule(r.Context(), req.Cron); err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, &closingScheduleResponse{Cron: req.Cron})
	}
}

// --- verification keys ---

func listVerificationKeysHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keys, err := b.GetService().ListVerificationKeys(r.Context())
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, keys)
	}
}

// --- rule revisions ---

func listRuleRevisionsHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		revs, err := b.GetService().ListRuleRevisions(r.Context(), ruleID)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, revs)
	}
}

func getRuleRevisionHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		revision, err := strconv.ParseInt(chi.URLParam(r, "revision"), 10, 64)
		if err != nil || revision <= 0 {
			api.BadRequest(w, ErrValidation, fmt.Errorf("revision must be a positive integer"))
			return
		}
		rev, err := b.GetService().GetRuleRevision(r.Context(), ruleID, revision)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, rev)
	}
}
