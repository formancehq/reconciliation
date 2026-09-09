package audit

import "sort"

// BaselineRevision is the legacy fctl commit this baseline was transcribed
// from. Every SourceFile below is a path in that tree.
const BaselineRevision = "693c58e27865f83332e6c3199d61fed81b742f41"

// BaselineCommand is one executable legacy fctl `reconciliation` command.
//
// Grouping-only cobra commands are not commands and are not listed: the
// baseline counts executable leaves only. At BaselineRevision the two
// grouping-only nodes are `reconciliation` (cmd/reconciliation/root.go, built
// with fctl.NewStackCommand and carrying no controller) and `reconciliation
// policies` (cmd/reconciliation/policies/root.go, alias `p`).
type BaselineCommand struct {
	// Path is the canonical invocation, without the `fctl` prefix.
	Path string
	// Aliases are the command's declared aliases at BaselineRevision, in
	// declaration order. The fctl-v2 programme requires every alias to be
	// retained, so they are pinned here rather than rediscovered later.
	Aliases []string
	// SourceFile is the declaring file at BaselineRevision.
	SourceFile string
	// Ops are the operationIds the legacy command reaches, in the order the
	// legacy code issues them. Empty means the behaviour has no equivalent in
	// the current document and Exclusion states why.
	Ops []string
	// Exclusion is the evidence-backed reason the command is not carried over.
	// Non-empty exactly when Ops is empty.
	Exclusion string
}

// Baseline is the complete set of executable legacy fctl `reconciliation`
// commands at BaselineRevision, with each one's mapping onto the current
// document.
//
// The legacy commands call a single API generation through the Formance Go SDK
// surface `stackClient.Reconciliation.V1.*`; unlike Payments there is no
// version-probing branch and therefore no pair of legacy/current operationIds
// per command.
var Baseline = []BaselineCommand{
	// reconciliations
	{
		Path:       "reconciliation list",
		Aliases:    []string{"ls", "l"},
		SourceFile: "cmd/reconciliation/list.go",
		Ops:        []string{"listReconciliations"},
	},
	{
		Path:       "reconciliation get <reconciliationID>",
		Aliases:    []string{"sh", "s"},
		SourceFile: "cmd/reconciliation/show.go",
		Ops:        []string{"getReconciliation"},
	},

	// policies
	{
		Path:       "reconciliation policies list",
		Aliases:    []string{"ls", "l"},
		SourceFile: "cmd/reconciliation/policies/list.go",
		Ops:        []string{"listPolicies"},
	},
	{
		Path:       "reconciliation policies get <policyID>",
		Aliases:    []string{"sh", "s"},
		SourceFile: "cmd/reconciliation/policies/show.go",
		Ops:        []string{"getPolicy"},
	},
	{
		Path:       "reconciliation policies create <file>|-",
		Aliases:    []string{"cr", "c"},
		SourceFile: "cmd/reconciliation/policies/create.go",
		Ops:        []string{"createPolicy"},
	},
	{
		Path:       "reconciliation policies delete <policyID>",
		Aliases:    []string{"d"},
		SourceFile: "cmd/reconciliation/policies/delete.go",
		Ops:        []string{"deletePolicy"},
	},
	{
		Path:       "reconciliation policies reconcile <policyID> <atLedger> <atPayments>",
		Aliases:    []string{"r"},
		SourceFile: "cmd/reconciliation/policies/reconciliation.go",
		Ops:        []string{"reconcile"},
	},
}

// MappedBaseline returns the baseline commands that map onto a current
// operation.
func MappedBaseline() []BaselineCommand {
	var out []BaselineCommand
	for _, c := range Baseline {
		if len(c.Ops) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// ExcludedBaseline returns the baseline commands recorded as not carried over.
func ExcludedBaseline() []BaselineCommand {
	var out []BaselineCommand
	for _, c := range Baseline {
		if len(c.Ops) == 0 {
			out = append(out, c)
		}
	}
	return out
}

// BaselineTargets returns the sorted, de-duplicated operationIds the baseline
// maps onto.
func BaselineTargets() []string {
	seen := map[string]struct{}{}
	for _, c := range Baseline {
		for _, op := range c.Ops {
			seen[op] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for op := range seen {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}
