# Result

Whether the rule passed, failed, or could not be evaluated

## Example Usage

```go
import (
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

value := components.ResultPass

// Open enum: custom values can be created with a direct type cast
custom := components.Result("custom_value")
```


## Values

| Name          | Value         |
| ------------- | ------------- |
| `ResultPass`  | PASS          |
| `ResultFail`  | FAIL          |
| `ResultError` | ERROR         |