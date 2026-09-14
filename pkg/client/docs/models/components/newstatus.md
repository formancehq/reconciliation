# NewStatus

Status after this event

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.NewStatusOpen

// Open enum: custom values can be created with a direct type cast
custom := components.NewStatus("custom_value")
```


## Values

| Name                    | Value                   |
| ----------------------- | ----------------------- |
| `NewStatusOpen`         | OPEN                    |
| `NewStatusAcknowledged` | ACKNOWLEDGED            |
| `NewStatusResolved`     | RESOLVED                |