# PrevStatus

Status before this event. Null only for the alert's inaugural event

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.PrevStatusOpen

// Open enum: custom values can be created with a direct type cast
custom := components.PrevStatus("custom_value")
```


## Values

| Name                     | Value                    |
| ------------------------ | ------------------------ |
| `PrevStatusOpen`         | OPEN                     |
| `PrevStatusAcknowledged` | ACKNOWLEDGED             |
| `PrevStatusResolved`     | RESOLVED                 |