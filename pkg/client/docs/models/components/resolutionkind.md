# ResolutionKind

How the alert was resolved, whether automatically, by booking a correcting transaction, or by business acceptance

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.ResolutionKindAuto

// Open enum: custom values can be created with a direct type cast
custom := components.ResolutionKind("custom_value")
```


## Values

| Name                               | Value                              |
| ---------------------------------- | ---------------------------------- |
| `ResolutionKindAuto`               | auto                               |
| `ResolutionKindFixedByBooking`     | fixed_by_booking                   |
| `ResolutionKindAcceptedByBusiness` | accepted_by_business               |