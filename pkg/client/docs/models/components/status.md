# Status

Where the alert stands in its open, acknowledged, resolved lifecycle

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.StatusOpen

// Open enum: custom values can be created with a direct type cast
custom := components.Status("custom_value")
```


## Values

| Name                 | Value                |
| -------------------- | -------------------- |
| `StatusOpen`         | OPEN                 |
| `StatusAcknowledged` | ACKNOWLEDGED         |
| `StatusResolved`     | RESOLVED             |