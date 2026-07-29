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
	Sequence      int64             `json:"sequence"`
	At            time.Time         `json:"at"`
	Kind          string            `json:"kind"`
	RuleID        *string           `json:"ruleID,omitempty"`
	RuleRevision  *int64            `json:"ruleRevision,omitempty"`
	AlertID       *string           `json:"alertID,omitempty"`
	EvaluationID  *string           `json:"evaluationID,omitempty"`
	PeriodID      string            `json:"periodID,omitempty"`
	Subject       subjectResponse   `json:"subject"`
	MementoDigest string            `json:"mementoDigest"`
	PrevHash      string            `json:"prevHash,omitempty"`
	Hash          string            `json:"hash"`
	HashVersion   int               `json:"hashVersion"`
	Memento       string            `json:"memento,omitempty"`
	MementoJSON   json.RawMessage   `json:"mementoJSON,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
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
	Head      int64  `json:"head"`
	HeadHash  string `json:"headHash,omitempty"`
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
			seal, err := b.GetService().GetPeriodSeal(r.Context(), req.PeriodID)
			if err != nil {
				handleServiceErrors(w, r, err)
				return
			}
			from, to = seal.FirstSequence, seal.LastSequence
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
		f       storage.AuditEntryFilters
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

// --- period seals ---

type periodSealResponse struct {
	PeriodID      string `json:"periodID"`
	Status        string `json:"status"`
	FirstSequence int64  `json:"firstSequence"`
	LastSequence  int64  `json:"lastSequence"`
	EntryCount    int64  `json:"entryCount"`
	LastAuditHash string `json:"lastAuditHash,omitempty"`
	StateHash     string `json:"stateHash"`
	// SealingHash is the single value that stands for the whole period. It is
	// unkeyed, so an auditor can recompute it from the fields above.
	SealingHash string `json:"sealingHash"`
	// Signature is what makes the seal checkable without trusting this service.
	Signature       string          `json:"signature,omitempty"`
	SigningKeyID    string          `json:"signingKeyID,omitempty"`
	SealedBy        subjectResponse `json:"sealedBy"`
	AlertCount      int64           `json:"alertCount"`
	UnresolvedCount int64           `json:"unresolvedCount"`
	SealedAt        time.Time       `json:"sealedAt"`
	AuditSequence   int64           `json:"auditSequence"`
}

func renderPeriodSeal(seal *models.PeriodSeal) *periodSealResponse {
	return &periodSealResponse{
		PeriodID:        seal.PeriodID,
		Status:          string(seal.Status()),
		FirstSequence:   seal.FirstSequence,
		LastSequence:    seal.LastSequence,
		EntryCount:      seal.EntryCount,
		LastAuditHash:   hex.EncodeToString(seal.LastAuditHash),
		StateHash:       hex.EncodeToString(seal.StateHash),
		SealingHash:     hex.EncodeToString(seal.SealingHash),
		Signature:       base64.StdEncoding.EncodeToString(seal.Signature),
		SigningKeyID:    seal.SigningKeyID,
		SealedBy:        renderSubject(seal.SealedBy),
		AlertCount:      seal.AlertCount,
		UnresolvedCount: seal.UnresolvedCount,
		SealedAt:        seal.SealedAt,
		AuditSequence:   seal.AuditSequence,
	}
}

func listPeriodSealsHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seals, err := b.GetService().ListPeriodSeals(r.Context())
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		out := make([]*periodSealResponse, 0, len(seals))
		for i := range seals {
			out = append(out, renderPeriodSeal(&seals[i]))
		}
		api.Ok(w, out)
	}
}

func getPeriodSealHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID := chi.URLParam(r, "periodID")
		seal, err := b.GetService().GetPeriodSeal(r.Context(), periodID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				// An open period is a legitimate answer, not a missing resource:
				// absence of a seal IS the open state, and returning 404 would make
				// a client guess whether the period is open or the id is wrong.
				api.Ok(w, &periodSealResponse{PeriodID: periodID, Status: string(models.PeriodOpen)})
				return
			}
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderPeriodSeal(seal))
	}
}

func sealPeriodHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID := chi.URLParam(r, "periodID")
		seal, err := b.GetService().SealPeriod(r.Context(), periodID)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Created(w, renderPeriodSeal(seal))
	}
}

type verifySealResponse struct {
	PeriodID string              `json:"periodID"`
	OK       bool                `json:"ok"`
	Reason   string              `json:"reason,omitempty"`
	Seal     *periodSealResponse `json:"seal"`
}

func verifyPeriodSealHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		periodID := chi.URLParam(r, "periodID")
		seal, ok, reason, err := b.GetService().VerifySealSignature(r.Context(), periodID)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, &verifySealResponse{
			PeriodID: periodID,
			OK:       ok,
			Reason:   reason,
			Seal:     renderPeriodSeal(seal),
		})
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
