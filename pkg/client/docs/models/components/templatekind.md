# TemplateKind

Which built-in check a rule applies, determining the shape of its templateSpec

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.TemplateKindLedgerVsPoolDrift

// Open enum: custom values can be created with a direct type cast
custom := components.TemplateKind("custom_value")
```


## Values

| Name                            | Value                           |
| ------------------------------- | ------------------------------- |
| `TemplateKindLedgerVsPoolDrift` | ledger_vs_pool_drift            |
| `TemplateKindLedgerInvariant`   | ledger_invariant                |
| `TemplateKindAccountThreshold`  | account_threshold               |
| `TemplateKindSourceParity`      | source_parity                   |