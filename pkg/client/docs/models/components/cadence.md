# Cadence

Reconciliation rhythm. Scopes each failing fingerprint into a period so a
March break and an April break are distinct, independently-closable cases.
`continuous` (default) is a single unbounded period (live monitoring).


## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.CadenceContinuous

// Open enum: custom values can be created with a direct type cast
custom := components.Cadence("custom_value")
```


## Values

| Name                | Value               |
| ------------------- | ------------------- |
| `CadenceContinuous` | continuous          |
| `CadenceDaily`      | daily               |
| `CadenceWeekly`     | weekly              |
| `CadenceMonthly`    | monthly             |