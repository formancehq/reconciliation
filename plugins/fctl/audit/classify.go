package audit

import "sort"

// Family is a frozen operation family of the Reconciliation surface.
//
// FamilyPolicies and FamilyReconciliations are the two families the fctl-v2
// programme scopes for this plugin: Task 10B states that Reconciliation
// "covers reconciliations and policies". FamilyRules, FamilyEvaluations and
// FamilyAlerts exist in the current document but have no legacy fctl
// precedent and are outside that stated interface, so they are recorded, not
// frozen into the first tranche. FamilyServerProbe holds the single shared
// probe operation, which the fctl host owns rather than the plugin.
type Family string

const (
	FamilyPolicies        Family = "policies"
	FamilyReconciliations Family = "reconciliations"
	FamilyRules           Family = "rules"
	FamilyEvaluations     Family = "evaluations"
	FamilyAlerts          Family = "alerts"
	FamilyServerProbe     Family = "server-probe"
)

// FrozenFamilies are the families in scope for the first plugin tranche, per
// the Task 10B interface statement.
var FrozenFamilies = []Family{FamilyPolicies, FamilyReconciliations}

// IsFrozen reports whether a family is in the first plugin tranche.
func IsFrozen(f Family) bool {
	for _, frozen := range FrozenFamilies {
		if f == frozen {
			return true
		}
	}
	return false
}

// familyOf assigns every operationId to exactly one family. The table is
// explicit rather than prefix-derived so that a new spec operation fails the
// completeness test instead of being silently absorbed by a prefix rule.
var familyOf = map[string]Family{
	// policies
	"createPolicy": FamilyPolicies,
	"listPolicies": FamilyPolicies,
	"getPolicy":    FamilyPolicies,
	"deletePolicy": FamilyPolicies,

	// reconciliations
	"reconcile":           FamilyReconciliations,
	"listReconciliations": FamilyReconciliations,
	"getReconciliation":   FamilyReconciliations,

	// rules; recorded, not frozen
	"createRule":   FamilyRules,
	"listRules":    FamilyRules,
	"getRule":      FamilyRules,
	"patchRule":    FamilyRules,
	"deleteRule":   FamilyRules,
	"evaluateRule": FamilyRules,

	// evaluations; recorded, not frozen
	"listEvaluations": FamilyEvaluations,
	"getEvaluation":   FamilyEvaluations,

	// alerts; recorded, not frozen
	"listAlerts":      FamilyAlerts,
	"getAlert":        FamilyAlerts,
	"listAlertEvents": FamilyAlerts,
	"ackAlert":        FamilyAlerts,
	"resolveAlert":    FamilyAlerts,
	"acceptAlert":     FamilyAlerts,
	"snoozeAlert":     FamilyAlerts,
	"unsnoozeAlert":   FamilyAlerts,

	// shared server probe: the only operation fctl needs before it knows the
	// product major, and the one the host performs rather than the plugin.
	"getServerInfo": FamilyServerProbe,
}

// FamilyOf returns the frozen family of an operationId, and whether it is
// classified at all.
func FamilyOf(operationID string) (Family, bool) {
	f, ok := familyOf[operationID]
	return f, ok
}

// ClassifiedOperationIDs returns every classified operationId, sorted.
func ClassifiedOperationIDs() []string {
	out := make([]string, 0, len(familyOf))
	for id := range familyOf {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SecretDirection says which side of an operation carries credentials.
type SecretDirection string

const (
	SecretNone     SecretDirection = ""
	SecretInbound  SecretDirection = "request"
	SecretOutbound SecretDirection = "response"
)

// NoSecretBearingOperation records that no operation in the pinned document
// carries credentials in either direction.
//
// Evidence: none of the 37 declared component schemas exposes a property whose
// name matches secret/password/token/apiKey/credential/privateKey/passphrase.
// The Reconciliation domain objects are policies (ledger and payments query
// definitions), reconciliations, rules, evaluations and alerts; the service
// holds no PSP or provider credentials. Asserted by
// TestNoSecretBearingSchemaProperty. This is the opposite of the Payments
// surface, where connector configuration carries cleartext PSP credentials.
const NoSecretBearingOperation = true

// displayOnce is empty: the pinned document declares no success body carrying a
// one-shot value such as a minted link, authorisation URL or generated secret.
// Asserted by TestNoDisplayOnceOperation. It is kept as an explicit table so a
// future one-shot value has an obvious place to be recorded.
var displayOnce = map[string]struct{}{}

// NoIdempotencyKey records that the pinned Reconciliation document declares no
// Idempotency-Key (or equivalent) parameter or header on any operation.
//
// The only idempotency key in this repository is internal/events/alert.go,
// which stamps an outbound event envelope for downstream consumer dedup; it is
// not an inbound API parameter and gives an fctl caller no replay protection.
// Asserted by TestNoIdempotencyKeyIsDeclared.
const NoIdempotencyKey = true

// destructiveNonDelete lists operations that remove or reset server state
// without using the DELETE method. DELETE-method operations are derived.
//
// It is empty for this surface: the two state-removing operations are
// deletePolicy and deleteRule, both DELETE. The alert lifecycle operations
// (ack, resolve, accept, snooze, unsnooze) transition an alert's state rather
// than removing it, and evaluateRule creates an evaluation rather than
// destroying one.
var destructiveNonDelete = map[string]struct{}{}

// Risk is the per-operation risk profile, derived from spec facts plus the
// explicit tables above.
type Risk struct {
	// Destructive is true for operations that remove or reset server state.
	Destructive bool `json:"destructive"`
	// Secret says whether credentials cross the boundary, and in which
	// direction.
	Secret SecretDirection `json:"secret"`
	// DisplayOnce is true when the success body carries a one-shot value.
	DisplayOnce bool `json:"displayOnce"`
	// ReplaySafe is true for HTTP-idempotent methods (GET, PUT, PATCH, DELETE).
	// The document declares no idempotency key on any operation, so POST
	// operations are never replay-safe: see NoIdempotencyKey.
	ReplaySafe bool `json:"replaySafe"`
	// Paginated is true when the operation exposes cursor + pageSize.
	Paginated bool `json:"paginated"`
	// GetWithBody is true when a GET declares a JSON request body, which is the
	// QueryBuilder filter shape and a portability hazard for any host transport
	// that drops GET bodies.
	GetWithBody bool `json:"getWithBody"`
	// StateTransition is true for the alert lifecycle operations, which change
	// an alert's state without removing it. They are not destructive, but they
	// are also not replay-safe, so they are named rather than left implicit.
	StateTransition bool `json:"stateTransition"`
}

// alertStateTransition lists the alert lifecycle operations.
var alertStateTransition = map[string]struct{}{
	"ackAlert":      {},
	"resolveAlert":  {},
	"acceptAlert":   {},
	"snoozeAlert":   {},
	"unsnoozeAlert": {},
}

// RiskOf derives the risk profile of an operation.
func RiskOf(op Operation) Risk {
	_, resetLike := destructiveNonDelete[op.OperationID]
	_, once := displayOnce[op.OperationID]
	_, transition := alertStateTransition[op.OperationID]

	return Risk{
		Destructive:     op.Method == "DELETE" || resetLike,
		Secret:          SecretNone,
		DisplayOnce:     once,
		ReplaySafe:      op.Method == "GET" || op.Method == "PUT" || op.Method == "PATCH" || op.Method == "DELETE",
		Paginated:       op.Paginated(),
		GetWithBody:     op.Method == "GET" && op.HasRequestBody(),
		StateTransition: transition,
	}
}
