package ledgerstate

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
)

// Layout maps the port's keys to ledger accounts: where an entity lives,
// which gauge it posts from, which transaction reference takes a version,
// and what the ledger must declare and index.
type Layout interface {
	Address(k controlstate.Key) (string, error)
	Gauge(k controlstate.Key) (string, error)
	// Reference is the transaction reference that takes version v of k.
	Reference(k controlstate.Key, v controlstate.Version) string
	// Guards returns the two TRANSIENT accounts of the guard without a write.
	Guards() (string, string)
	// Projected lists the fields of a kind also written as metadata keys of
	// their own, so that list filters reach them.
	Projected(kind string) []string
	// TxMetadata is the extra transaction metadata of every write about k.
	TxMetadata(k controlstate.Key) map[string]string
	AccountTypes() map[string]*commonpb.AccountType
	AccountIndexes() []string
	TxIndexes() []string
}

var plainSegment = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// segment keeps a plain value as it is and hex-encodes anything else behind
// an underscore, which plain values never contain.
func segment(s string) string {
	if plainSegment.MatchString(s) {
		return s
	}
	return "_" + hex.EncodeToString([]byte(s))
}

func transient(name, pattern string) *commonpb.AccountType {
	return &commonpb.AccountType{Name: name, Pattern: pattern, Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_TRANSIENT}
}

// Generic is a layout for any key, used by the contract suite: the address
// is <ns>:<kind>:<id segments>, each ":"-separated segment of the ID kept
// readable, and one gauge serves the whole namespace.
type Generic struct{ NS string }

func (g Generic) Address(k controlstate.Key) (string, error) {
	parts := []string{g.NS, segment(k.Kind)}
	for _, p := range strings.Split(k.ID, ":") {
		parts = append(parts, segment(p))
	}
	return strings.Join(parts, ":"), nil
}
func (g Generic) Gauge(controlstate.Key) (string, error) { return g.NS + ":pool", nil }
func (g Generic) Reference(k controlstate.Key, v controlstate.Version) string {
	addr, _ := g.Address(k)
	return fmt.Sprintf("%s:v%d", addr, v)
}
func (g Generic) Guards() (string, string)                    { return g.NS + ":guard:a", g.NS + ":guard:b" }
func (Generic) Projected(string) []string                     { return nil }
func (Generic) TxMetadata(controlstate.Key) map[string]string { return nil }
func (g Generic) AccountTypes() map[string]*commonpb.AccountType {
	a, b := g.Guards()
	return map[string]*commonpb.AccountType{
		g.NS + "-guard-a": transient(g.NS+"-guard-a", a),
		g.NS + "-guard-b": transient(g.NS+"-guard-b", b),
	}
}
func (Generic) AccountIndexes() []string { return nil }
func (Generic) TxIndexes() []string      { return nil }

// Recon is the reconciliation chart of RFC 0022 (9959818):
//
//	pool:rule:{ruleId}                            gauge, NORMAL
//	rule:{ruleId}                                 key {rule, ruleId}
//	eval:rule:{ruleId}                            key {eval, ruleId}
//	alert:rule:{ruleId}:per:{period}:fp:{fpHash}  key {alert, ruleId:period:fpHash}
//	guard:a, guard:b                              TRANSIENT
//
// Every transaction carries the indexed transaction metadata rule.
type Recon struct{}

const (
	KindRule  = "rule"
	KindEval  = "eval"
	KindAlert = "alert"
)

// AlertID builds the key ID of an alert.
func AlertID(ruleID, period, fpHash string) string { return ruleID + ":" + period + ":" + fpHash }

func (Recon) rule(k controlstate.Key) (string, error) {
	switch k.Kind {
	case KindRule, KindEval:
		return k.ID, nil
	case KindAlert:
		r, _, ok := strings.Cut(k.ID, ":")
		if !ok {
			return "", fmt.Errorf("%w: alert id %q is not rule:period:fp", controlstate.ErrInvalid, k.ID)
		}
		return r, nil
	default:
		return "", fmt.Errorf("%w: kind %q is not in the reconciliation chart", controlstate.ErrInvalid, k.Kind)
	}
}

func (r Recon) Address(k controlstate.Key) (string, error) {
	switch k.Kind {
	case KindRule:
		return "rule:" + k.ID, nil
	case KindEval:
		return "eval:rule:" + k.ID, nil
	case KindAlert:
		p := strings.Split(k.ID, ":")
		if len(p) != 3 {
			return "", fmt.Errorf("%w: alert id %q is not rule:period:fp", controlstate.ErrInvalid, k.ID)
		}
		return fmt.Sprintf("alert:rule:%s:per:%s:fp:%s", p[0], p[1], p[2]), nil
	}
	_, err := r.rule(k)
	return "", err
}

func (r Recon) Gauge(k controlstate.Key) (string, error) {
	rule, err := r.rule(k)
	if err != nil {
		return "", err
	}
	return "pool:rule:" + rule, nil
}

func (Recon) Reference(k controlstate.Key, v controlstate.Version) string {
	return fmt.Sprintf("%s:%s:v%d", k.Kind, k.ID, v)
}

func (Recon) Guards() (string, string) { return "guard:a", "guard:b" }

func (Recon) Projected(kind string) []string {
	switch kind {
	case KindRule:
		return []string{"template", "enabled"}
	case KindAlert:
		return []string{"status", "rule", "period", "fingerprint"}
	}
	return nil
}

func (r Recon) TxMetadata(k controlstate.Key) map[string]string {
	rule, err := r.rule(k)
	if err != nil {
		return nil
	}
	return map[string]string{"rule": rule}
}

func (Recon) AccountTypes() map[string]*commonpb.AccountType {
	uuid := map[string]*commonpb.SegmentType{"rule": {Constraint: &commonpb.SegmentType_Uuid{Uuid: &commonpb.UUIDConstraint{}}}}
	normal := func(name, pattern string, segs map[string]*commonpb.SegmentType) *commonpb.AccountType {
		return &commonpb.AccountType{Name: name, Pattern: pattern, Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL, SegmentTypes: segs}
	}
	alertSegs := map[string]*commonpb.SegmentType{
		"rule":   uuid["rule"],
		"period": {Constraint: &commonpb.SegmentType_Regex{Regex: `^[0-9A-Za-z-]+$`}},
		"fp":     {Constraint: &commonpb.SegmentType_Bytes{Bytes: &commonpb.BytesConstraint{}}},
	}
	return map[string]*commonpb.AccountType{
		"gauge":   normal("gauge", "pool:rule:{rule}", uuid),
		"rule":    normal("rule", "rule:{rule}", uuid),
		"eval":    normal("eval", "eval:rule:{rule}", uuid),
		"alert":   normal("alert", "alert:rule:{rule}:per:{period}:fp:{fp}", alertSegs),
		"guard-a": transient("guard-a", "guard:a"),
		"guard-b": transient("guard-b", "guard:b"),
	}
}

func (Recon) AccountIndexes() []string {
	return []string{"template", "enabled", "status", "rule", "period", "fingerprint"}
}
func (Recon) TxIndexes() []string { return []string{"rule", "cs_record_kind"} }
