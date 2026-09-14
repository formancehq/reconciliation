package audit

import (
	"fmt"
	"path/filepath"
	"sort"
)

// Record is one operation with everything this audit can prove about it.
type Record struct {
	Operation
	Family Family `json:"family"`
	// Frozen reports whether the record's family is in the first plugin
	// tranche (policies and reconciliations).
	Frozen bool `json:"frozen"`
	Risk   Risk `json:"risk"`
	// BaselineCommands are the legacy fctl command paths that map onto this
	// operation, sorted. Empty means the operation has no legacy precedent.
	BaselineCommands []string `json:"baselineCommands"`
	// Blockers are the blocker IDs recorded against this operation, sorted.
	Blockers []string `json:"blockers"`
}

// Totals are the counts the inventory document quotes. Every one of them is
// derived, never transcribed.
type Totals struct {
	// SpecOperations is every operation in the document.
	SpecOperations int `json:"specOperations"`
	// UniqueOperationIDs must equal SpecOperations.
	UniqueOperationIDs int `json:"uniqueOperationIds"`
	// DeprecatedOperations is the count marked deprecated.
	DeprecatedOperations int `json:"deprecatedOperations"`
	// OperationsWithDeclaredScopes is the count declaring a security block.
	OperationsWithDeclaredScopes int `json:"operationsWithDeclaredScopes"`

	// BaselineCommands is the executable legacy fctl `reconciliation` command
	// count at BaselineRevision.
	BaselineCommands int `json:"baselineCommands"`
	// BaselineMapped and BaselineExcluded partition BaselineCommands.
	BaselineMapped   int `json:"baselineMapped"`
	BaselineExcluded int `json:"baselineExcluded"`

	// FrozenOperations is the size of the first tranche (policies +
	// reconciliations). RecordedOperations is the rest of the surface, which is
	// inventoried but outside the Task 10B interface statement.
	FrozenOperations   int `json:"frozenOperations"`
	RecordedOperations int `json:"recordedOperations"`

	// WithBaseline is the number of operations reached by the legacy baseline.
	WithBaseline int `json:"withBaseline"`
	// WithoutBaseline is the surface that has no legacy precedent.
	WithoutBaseline int `json:"withoutBaseline"`
	// Blocked is the number of operations carrying a blocker.
	Blocked int `json:"blocked"`
	// FrozenBlocked is the number of first-tranche operations carrying a
	// blocker.
	FrozenBlocked int `json:"frozenBlocked"`
	// Admissible is SpecOperations minus Blocked: proven facts, no recorded
	// blocker. It is not an acceptance claim; the runtime gates are separate.
	Admissible int `json:"admissible"`
	// FrozenAdmissible is FrozenOperations minus FrozenBlocked.
	FrozenAdmissible int `json:"frozenAdmissible"`

	// GenerationBlockers is the number of recorded reasons the Task 10A SDK
	// was not generated in this tranche.
	GenerationBlockers int `json:"generationBlockers"`
	// Divergences is the number of recorded spec-versus-server mismatches and
	// document defects.
	Divergences int `json:"divergences"`
}

// Report is the whole deterministic inventory.
type Report struct {
	// SpecDocument is the base name of the document the report was built from.
	// Only the base name is recorded so the report is identical whether it is
	// produced from the module root or from a package directory.
	SpecDocument string `json:"specDocument"`
	// ProductRevision pins the Reconciliation base commit used for the audit;
	// the generated-client contract bytes are pinned separately.
	ProductRevision string `json:"productRevision"`
	// ClientSpecSHA256 pins the exact amended OpenAPI bytes used for generation.
	ClientSpecSHA256 string `json:"clientSpecSHA256"`
	// BaselineRevision pins the legacy fctl tree the baseline came from.
	BaselineRevision string `json:"baselineRevision"`
	// FctlV2Revision pins the programme tree this preparation answers to.
	FctlV2Revision string `json:"fctlV2Revision"`
	// Document holds the document-level facts, including the undefined
	// security scheme.
	Document Document `json:"document"`
	// UndefinedSecuritySchemes is surfaced at the top level because it is the
	// single most load-bearing document defect for Task 10A.
	UndefinedSecuritySchemes []string `json:"undefinedSecuritySchemes"`
	Totals                   Totals   `json:"totals"`
	// Operations is every operation, sorted by operationId.
	Operations []Record `json:"operations"`
}

// Build reads the document at specPath and produces the full report.
func Build(specPath string) (*Report, error) {
	meta, ops, err := Load(specPath)
	if err != nil {
		return nil, err
	}

	byID := Index(ops)
	if len(byID) != len(ops) {
		return nil, fmt.Errorf("document declares %d operations but only %d unique operationIds", len(ops), len(byID))
	}

	commandsByOp := map[string][]string{}
	for _, c := range Baseline {
		for _, op := range c.Ops {
			commandsByOp[op] = append(commandsByOp[op], c.Path)
		}
	}
	blockersByOp := map[string][]string{}
	for _, b := range Blockers {
		for _, op := range b.OperationIDs {
			blockersByOp[op] = append(blockersByOp[op], b.ID)
		}
	}

	report := &Report{
		SpecDocument:             filepath.Base(specPath),
		ProductRevision:          ProductRevision,
		ClientSpecSHA256:         ClientSpecSHA256,
		BaselineRevision:         BaselineRevision,
		FctlV2Revision:           FctlV2Revision,
		Document:                 meta,
		UndefinedSecuritySchemes: meta.UndefinedSecuritySchemes(),
	}

	for _, op := range ops {
		if op.Tag != TagV1 {
			return nil, fmt.Errorf("operation %s carries unknown tag %q", op.OperationID, op.Tag)
		}
		family, ok := FamilyOf(op.OperationID)
		if !ok {
			return nil, fmt.Errorf("operation %s is not classified into a family", op.OperationID)
		}
		commands := append([]string(nil), commandsByOp[op.OperationID]...)
		sort.Strings(commands)
		blocks := append([]string(nil), blockersByOp[op.OperationID]...)
		sort.Strings(blocks)
		report.Operations = append(report.Operations, Record{
			Operation:        op,
			Family:           family,
			Frozen:           IsFrozen(family),
			Risk:             RiskOf(op),
			BaselineCommands: commands,
			Blockers:         blocks,
		})
	}

	t := Totals{
		SpecOperations:     len(ops),
		UniqueOperationIDs: len(byID),
		BaselineCommands:   len(Baseline),
		BaselineMapped:     len(MappedBaseline()),
		BaselineExcluded:   len(ExcludedBaseline()),
		GenerationBlockers: len(GenerationBlockers),
		Divergences:        len(Divergences),
	}
	for _, r := range report.Operations {
		if r.Deprecated {
			t.DeprecatedOperations++
		}
		if r.HasSecurity {
			t.OperationsWithDeclaredScopes++
		}
		if r.Frozen {
			t.FrozenOperations++
		} else {
			t.RecordedOperations++
		}
		if len(r.BaselineCommands) > 0 {
			t.WithBaseline++
		} else {
			t.WithoutBaseline++
		}
		if len(r.Blockers) > 0 {
			t.Blocked++
			if r.Frozen {
				t.FrozenBlocked++
			}
		}
	}
	t.Admissible = t.SpecOperations - t.Blocked
	t.FrozenAdmissible = t.FrozenOperations - t.FrozenBlocked
	report.Totals = t

	return report, nil
}

// UnknownBaselineTargets returns baseline Ops entries that do not exist in the
// document. A non-empty result means the mapping references an operation the
// current source does not have.
func (r *Report) UnknownBaselineTargets() []string {
	present := map[string]struct{}{}
	for _, rec := range r.Operations {
		present[rec.OperationID] = struct{}{}
	}
	var missing []string
	for _, op := range BaselineTargets() {
		if _, ok := present[op]; !ok {
			missing = append(missing, op)
		}
	}
	sort.Strings(missing)
	return missing
}

// ByFamily groups the records by family, preserving operationId order.
func (r *Report) ByFamily() map[Family][]Record {
	out := map[Family][]Record{}
	for _, rec := range r.Operations {
		out[rec.Family] = append(out[rec.Family], rec)
	}
	return out
}
