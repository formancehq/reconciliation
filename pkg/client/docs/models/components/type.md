# Type

What happened, whether an evaluation outcome or a manual transition

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.TypeFail

// Open enum: custom values can be created with a direct type cast
custom := components.Type("custom_value")
```


## Values

| Name           | Value          |
| -------------- | -------------- |
| `TypeFail`     | fail           |
| `TypePass`     | pass           |
| `TypeAck`      | ack            |
| `TypeResolve`  | resolve        |
| `TypeAccept`   | accept         |
| `TypeSnooze`   | snooze         |
| `TypeUnsnooze` | unsnooze       |