# ScheduleKind

Whether the rule runs only when triggered or on a recurring cron schedule

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.ScheduleKindOnDemand

// Open enum: custom values can be created with a direct type cast
custom := components.ScheduleKind("custom_value")
```


## Values

| Name                   | Value                  |
| ---------------------- | ---------------------- |
| `ScheduleKindOnDemand` | on_demand              |
| `ScheduleKindCron`     | cron                   |