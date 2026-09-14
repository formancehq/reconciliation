# Reconciliation.V1

## Overview

### Available Operations

* [GetServerInfo](#getserverinfo) - Get server info
* [CreatePolicy](#createpolicy) - Create a policy
* [ListPolicies](#listpolicies) - List policies
* [DeletePolicy](#deletepolicy) - Delete a policy
* [GetPolicy](#getpolicy) - Get a policy
* [Reconcile](#reconcile) - Reconcile using a policy
* [ListReconciliations](#listreconciliations) - List reconciliations
* [GetReconciliation](#getreconciliation) - Get a reconciliation
* [CreateRule](#createrule) - Create a rule
* [ListRules](#listrules) - List rules
* [GetRule](#getrule) - Get a rule
* [PatchRule](#patchrule) - Patch a rule (partial update)
* [DeleteRule](#deleterule) - Delete a rule (cascades to evaluations + alerts + alert events)
* [EvaluateRule](#evaluaterule) - Evaluate a rule now
* [ListEvaluations](#listevaluations) - List evaluations
* [GetEvaluation](#getevaluation) - Get an evaluation
* [ListAlerts](#listalerts) - List alerts
* [GetAlert](#getalert) - Get an alert
* [ListAlertEvents](#listalertevents) - List alert events (append-only timeline)
* [AckAlert](#ackalert) - Acknowledge an alert
* [ResolveAlert](#resolvealert) - Resolve an alert (fixed_by_booking)
* [AcceptAlert](#acceptalert) - Accept an alert (accepted_by_business)
* [SnoozeAlert](#snoozealert) - Snooze an alert's notifications until a future instant
* [UnsnoozeAlert](#unsnoozealert) - Lift a snooze early

## GetServerInfo

Get server info

### Example Usage

<!-- UsageSnippet language="go" operationID="getServerInfo" method="get" path="/_info" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.GetServerInfo(ctx)
    if err != nil {
        log.Fatal(err)
    }
    if res.ServerInfo != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |

### Response

**[*operations.GetServerInfoResponse](../../models/operations/getserverinforesponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## CreatePolicy

Create a policy

### Example Usage

<!-- UsageSnippet language="go" operationID="createPolicy" method="post" path="/policies" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.CreatePolicy(ctx, components.PolicyRequest{
        Name: "XXX",
        LedgerName: "default",
        LedgerQuery: map[string]any{
            "key": "<value>",
        },
        PaymentsPoolID: "XXX",
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.PolicyResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                            | Type                                                                 | Required                                                             | Description                                                          |
| -------------------------------------------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `ctx`                                                                | [context.Context](https://pkg.go.dev/context#Context)                | :heavy_check_mark:                                                   | The context to use for the request.                                  |
| `request`                                                            | [components.PolicyRequest](../../models/components/policyrequest.md) | :heavy_check_mark:                                                   | The request object to use for the request.                           |
| `opts`                                                               | [][operations.Option](../../models/operations/option.md)             | :heavy_minus_sign:                                                   | The options for this request.                                        |

### Response

**[*operations.CreatePolicyResponse](../../models/operations/createpolicyresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## ListPolicies

List policies

### Example Usage

<!-- UsageSnippet language="go" operationID="listPolicies" method="get" path="/policies" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ListPolicies(ctx, client.Pointer[int64](100), client.Pointer("aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ=="), nil)
    if err != nil {
        log.Fatal(err)
    }
    if res.PoliciesCursorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                                                                                                                                                                                | Type                                                                                                                                                                                                                                                     | Required                                                                                                                                                                                                                                                 | Description                                                                                                                                                                                                                                              | Example                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx`                                                                                                                                                                                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                                                                                                                                                                                    | :heavy_check_mark:                                                                                                                                                                                                                                       | The context to use for the request.                                                                                                                                                                                                                      |                                                                                                                                                                                                                                                          |
| `pageSize`                                                                                                                                                                                                                                               | `*int64`                                                                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The maximum number of results to return per page.<br/>                                                                                                                                                                                                   | 100                                                                                                                                                                                                                                                      |
| `cursor`                                                                                                                                                                                                                                                 | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | Parameter used in pagination requests. Maximum page size is set to 15.<br/>Set to the value of next for the next page of results.<br/>Set to the value of previous for the previous page of results.<br/>No other parameters can be set when this parameter is set.<br/> | aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ==                                                                                                                                                                                                             |
| `query`                                                                                                                                                                                                                                                  | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | JSON-encoded QueryBuilder filter. Omit it on cursor continuation requests.                                                                                                                                                                               |                                                                                                                                                                                                                                                          |
| `opts`                                                                                                                                                                                                                                                   | [][operations.Option](../../models/operations/option.md)                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The options for this request.                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |

### Response

**[*operations.ListPoliciesResponse](../../models/operations/listpoliciesresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## DeletePolicy

Delete a policy by its id.

### Example Usage

<!-- UsageSnippet language="go" operationID="deletePolicy" method="delete" path="/policies/{policyID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.DeletePolicy(ctx, "XXX")
    if err != nil {
        log.Fatal(err)
    }
    if res.ErrorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              | Example                                                  |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |                                                          |
| `policyID`                                               | `string`                                                 | :heavy_check_mark:                                       | The policy ID.                                           | XXX                                                      |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |                                                          |

### Response

**[*operations.DeletePolicyResponse](../../models/operations/deletepolicyresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## GetPolicy

Get a policy

### Example Usage

<!-- UsageSnippet language="go" operationID="getPolicy" method="get" path="/policies/{policyID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.GetPolicy(ctx, "XXX")
    if err != nil {
        log.Fatal(err)
    }
    if res.PolicyResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              | Example                                                  |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |                                                          |
| `policyID`                                               | `string`                                                 | :heavy_check_mark:                                       | The policy ID.                                           | XXX                                                      |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |                                                          |

### Response

**[*operations.GetPolicyResponse](../../models/operations/getpolicyresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## Reconcile

Reconcile using a policy

### Example Usage

<!-- UsageSnippet language="go" operationID="reconcile" method="post" path="/policies/{policyID}/reconciliation" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/types"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.Reconcile(ctx, "XXX", components.ReconciliationRequest{
        ReconciledAtLedger: types.MustTimeFromString("2021-01-01T00:00:00.000Z"),
        ReconciledAtPayments: types.MustTimeFromString("2021-01-01T00:00:00.000Z"),
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.ReconciliationResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                            | Type                                                                                 | Required                                                                             | Description                                                                          | Example                                                                              |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| `ctx`                                                                                | [context.Context](https://pkg.go.dev/context#Context)                                | :heavy_check_mark:                                                                   | The context to use for the request.                                                  |                                                                                      |
| `policyID`                                                                           | `string`                                                                             | :heavy_check_mark:                                                                   | The policy ID.                                                                       | XXX                                                                                  |
| `body`                                                                               | [components.ReconciliationRequest](../../models/components/reconciliationrequest.md) | :heavy_check_mark:                                                                   | N/A                                                                                  |                                                                                      |
| `opts`                                                                               | [][operations.Option](../../models/operations/option.md)                             | :heavy_minus_sign:                                                                   | The options for this request.                                                        |                                                                                      |

### Response

**[*operations.ReconcileResponse](../../models/operations/reconcileresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## ListReconciliations

List reconciliations

### Example Usage

<!-- UsageSnippet language="go" operationID="listReconciliations" method="get" path="/reconciliations" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ListReconciliations(ctx, client.Pointer[int64](100), client.Pointer("aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ=="), nil)
    if err != nil {
        log.Fatal(err)
    }
    if res.ReconciliationsCursorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                                                                                                                                                                                | Type                                                                                                                                                                                                                                                     | Required                                                                                                                                                                                                                                                 | Description                                                                                                                                                                                                                                              | Example                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx`                                                                                                                                                                                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                                                                                                                                                                                    | :heavy_check_mark:                                                                                                                                                                                                                                       | The context to use for the request.                                                                                                                                                                                                                      |                                                                                                                                                                                                                                                          |
| `pageSize`                                                                                                                                                                                                                                               | `*int64`                                                                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The maximum number of results to return per page.<br/>                                                                                                                                                                                                   | 100                                                                                                                                                                                                                                                      |
| `cursor`                                                                                                                                                                                                                                                 | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | Parameter used in pagination requests. Maximum page size is set to 15.<br/>Set to the value of next for the next page of results.<br/>Set to the value of previous for the previous page of results.<br/>No other parameters can be set when this parameter is set.<br/> | aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ==                                                                                                                                                                                                             |
| `query`                                                                                                                                                                                                                                                  | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | JSON-encoded QueryBuilder filter. Omit it on cursor continuation requests.                                                                                                                                                                               |                                                                                                                                                                                                                                                          |
| `opts`                                                                                                                                                                                                                                                   | [][operations.Option](../../models/operations/option.md)                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The options for this request.                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |

### Response

**[*operations.ListReconciliationsResponse](../../models/operations/listreconciliationsresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## GetReconciliation

Get a reconciliation

### Example Usage

<!-- UsageSnippet language="go" operationID="getReconciliation" method="get" path="/reconciliations/{reconciliationID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.GetReconciliation(ctx, "XXX")
    if err != nil {
        log.Fatal(err)
    }
    if res.ReconciliationResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              | Example                                                  |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |                                                          |
| `reconciliationID`                                       | `string`                                                 | :heavy_check_mark:                                       | The reconciliation ID.                                   | XXX                                                      |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |                                                          |

### Response

**[*operations.GetReconciliationResponse](../../models/operations/getreconciliationresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## CreateRule

Create a rule

### Example Usage

<!-- UsageSnippet language="go" operationID="createRule" method="post" path="/rules" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.CreateRule(ctx, components.RuleRequest{
        Name: "<value>",
        TemplateKind: components.TemplateKindLedgerVsPoolDrift,
        TemplateSpec: map[string]any{

        },
        Schedule: &components.Schedule{
            Kind: components.ScheduleKindCron,
            Expr: client.Pointer("*/15 * * * *"),
            Tz: client.Pointer("UTC"),
            SafetyMargin: client.Pointer("30s"),
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.RuleResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                        | Type                                                             | Required                                                         | Description                                                      |
| ---------------------------------------------------------------- | ---------------------------------------------------------------- | ---------------------------------------------------------------- | ---------------------------------------------------------------- |
| `ctx`                                                            | [context.Context](https://pkg.go.dev/context#Context)            | :heavy_check_mark:                                               | The context to use for the request.                              |
| `request`                                                        | [components.RuleRequest](../../models/components/rulerequest.md) | :heavy_check_mark:                                               | The request object to use for the request.                       |
| `opts`                                                           | [][operations.Option](../../models/operations/option.md)         | :heavy_minus_sign:                                               | The options for this request.                                    |

### Response

**[*operations.CreateRuleResponse](../../models/operations/createruleresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## ListRules

List rules

### Example Usage

<!-- UsageSnippet language="go" operationID="listRules" method="get" path="/rules" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ListRules(ctx, client.Pointer[int64](100), client.Pointer("aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ=="), nil)
    if err != nil {
        log.Fatal(err)
    }
    if res.RulesCursorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                                                                                                                                                                                | Type                                                                                                                                                                                                                                                     | Required                                                                                                                                                                                                                                                 | Description                                                                                                                                                                                                                                              | Example                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx`                                                                                                                                                                                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                                                                                                                                                                                    | :heavy_check_mark:                                                                                                                                                                                                                                       | The context to use for the request.                                                                                                                                                                                                                      |                                                                                                                                                                                                                                                          |
| `pageSize`                                                                                                                                                                                                                                               | `*int64`                                                                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The maximum number of results to return per page.<br/>                                                                                                                                                                                                   | 100                                                                                                                                                                                                                                                      |
| `cursor`                                                                                                                                                                                                                                                 | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | Parameter used in pagination requests. Maximum page size is set to 15.<br/>Set to the value of next for the next page of results.<br/>Set to the value of previous for the previous page of results.<br/>No other parameters can be set when this parameter is set.<br/> | aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ==                                                                                                                                                                                                             |
| `query`                                                                                                                                                                                                                                                  | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | JSON-encoded QueryBuilder filter. Omit it on cursor continuation requests.                                                                                                                                                                               |                                                                                                                                                                                                                                                          |
| `opts`                                                                                                                                                                                                                                                   | [][operations.Option](../../models/operations/option.md)                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The options for this request.                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |

### Response

**[*operations.ListRulesResponse](../../models/operations/listrulesresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## GetRule

Get a rule

### Example Usage

<!-- UsageSnippet language="go" operationID="getRule" method="get" path="/rules/{ruleID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.GetRule(ctx, "fd71d712-041d-4271-b7c5-c9adac177f52")
    if err != nil {
        log.Fatal(err)
    }
    if res.RuleResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |
| `ruleID`                                                 | `string`                                                 | :heavy_check_mark:                                       | The rule ID.                                             |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |

### Response

**[*operations.GetRuleResponse](../../models/operations/getruleresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## PatchRule

Patch a rule (partial update)

### Example Usage

<!-- UsageSnippet language="go" operationID="patchRule" method="patch" path="/rules/{ruleID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.PatchRule(ctx, "0b4aa7b1-cc5d-4700-91ec-4983510fef86", components.RulePatchRequest{
        Schedule: &components.Schedule{
            Kind: components.ScheduleKindCron,
            Expr: client.Pointer("*/15 * * * *"),
            Tz: client.Pointer("UTC"),
            SafetyMargin: client.Pointer("30s"),
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.RuleResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                  | Type                                                                       | Required                                                                   | Description                                                                |
| -------------------------------------------------------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `ctx`                                                                      | [context.Context](https://pkg.go.dev/context#Context)                      | :heavy_check_mark:                                                         | The context to use for the request.                                        |
| `ruleID`                                                                   | `string`                                                                   | :heavy_check_mark:                                                         | The rule ID.                                                               |
| `body`                                                                     | [components.RulePatchRequest](../../models/components/rulepatchrequest.md) | :heavy_check_mark:                                                         | N/A                                                                        |
| `opts`                                                                     | [][operations.Option](../../models/operations/option.md)                   | :heavy_minus_sign:                                                         | The options for this request.                                              |

### Response

**[*operations.PatchRuleResponse](../../models/operations/patchruleresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## DeleteRule

Delete a rule (cascades to evaluations + alerts + alert events)

### Example Usage

<!-- UsageSnippet language="go" operationID="deleteRule" method="delete" path="/rules/{ruleID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.DeleteRule(ctx, "3254b217-2184-4bf4-bbc8-b529fa29bd7c")
    if err != nil {
        log.Fatal(err)
    }
    if res.ErrorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |
| `ruleID`                                                 | `string`                                                 | :heavy_check_mark:                                       | The rule ID.                                             |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |

### Response

**[*operations.DeleteRuleResponse](../../models/operations/deleteruleresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## EvaluateRule

Evaluate a rule now

### Example Usage

<!-- UsageSnippet language="go" operationID="evaluateRule" method="post" path="/rules/{ruleID}/evaluate" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/types"
	"time"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.EvaluateRule(ctx, "e9d27cb2-b7fc-4383-b319-936c01a66703", &components.EvaluateRuleRequest{
        SafetyMargin: client.Pointer("30s"),
        SourcePITs: map[string]time.Time{
            "ledger:main#0": types.MustTimeFromString("2026-06-30T23:59:59Z"),
            "pool:acct#0": types.MustTimeFromString("2026-06-30T23:00:00Z"),
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.EvaluationResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                         | Type                                                                              | Required                                                                          | Description                                                                       |
| --------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `ctx`                                                                             | [context.Context](https://pkg.go.dev/context#Context)                             | :heavy_check_mark:                                                                | The context to use for the request.                                               |
| `ruleID`                                                                          | `string`                                                                          | :heavy_check_mark:                                                                | The rule ID.                                                                      |
| `body`                                                                            | [*components.EvaluateRuleRequest](../../models/components/evaluaterulerequest.md) | :heavy_minus_sign:                                                                | N/A                                                                               |
| `opts`                                                                            | [][operations.Option](../../models/operations/option.md)                          | :heavy_minus_sign:                                                                | The options for this request.                                                     |

### Response

**[*operations.EvaluateRuleResponse](../../models/operations/evaluateruleresponse.md), error**

### Errors

| Error Type              | Status Code             | Content Type            |
| ----------------------- | ----------------------- | ----------------------- |
| apierrors.ErrorResponse | 409                     | application/json        |
| apierrors.APIError      | 4XX, 5XX                | \*/\*                   |

## ListEvaluations

List evaluations

### Example Usage

<!-- UsageSnippet language="go" operationID="listEvaluations" method="get" path="/evaluations" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ListEvaluations(ctx, client.Pointer[int64](100), client.Pointer("aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ=="), nil)
    if err != nil {
        log.Fatal(err)
    }
    if res.EvaluationsCursorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                                                                                                                                                                                | Type                                                                                                                                                                                                                                                     | Required                                                                                                                                                                                                                                                 | Description                                                                                                                                                                                                                                              | Example                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx`                                                                                                                                                                                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                                                                                                                                                                                    | :heavy_check_mark:                                                                                                                                                                                                                                       | The context to use for the request.                                                                                                                                                                                                                      |                                                                                                                                                                                                                                                          |
| `pageSize`                                                                                                                                                                                                                                               | `*int64`                                                                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The maximum number of results to return per page.<br/>                                                                                                                                                                                                   | 100                                                                                                                                                                                                                                                      |
| `cursor`                                                                                                                                                                                                                                                 | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | Parameter used in pagination requests. Maximum page size is set to 15.<br/>Set to the value of next for the next page of results.<br/>Set to the value of previous for the previous page of results.<br/>No other parameters can be set when this parameter is set.<br/> | aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ==                                                                                                                                                                                                             |
| `query`                                                                                                                                                                                                                                                  | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | JSON-encoded QueryBuilder filter. Omit it on cursor continuation requests.                                                                                                                                                                               |                                                                                                                                                                                                                                                          |
| `opts`                                                                                                                                                                                                                                                   | [][operations.Option](../../models/operations/option.md)                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The options for this request.                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |

### Response

**[*operations.ListEvaluationsResponse](../../models/operations/listevaluationsresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## GetEvaluation

Get an evaluation

### Example Usage

<!-- UsageSnippet language="go" operationID="getEvaluation" method="get" path="/evaluations/{evaluationID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.GetEvaluation(ctx, "121717d3-a7d1-444d-9d11-6ea2dc0d3db5")
    if err != nil {
        log.Fatal(err)
    }
    if res.EvaluationResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |
| `evaluationID`                                           | `string`                                                 | :heavy_check_mark:                                       | The evaluation ID.                                       |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |

### Response

**[*operations.GetEvaluationResponse](../../models/operations/getevaluationresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## ListAlerts

List alerts

### Example Usage

<!-- UsageSnippet language="go" operationID="listAlerts" method="get" path="/alerts" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ListAlerts(ctx, client.Pointer[int64](100), client.Pointer("aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ=="), nil)
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertsCursorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                                                                                                                                                                                | Type                                                                                                                                                                                                                                                     | Required                                                                                                                                                                                                                                                 | Description                                                                                                                                                                                                                                              | Example                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx`                                                                                                                                                                                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                                                                                                                                                                                    | :heavy_check_mark:                                                                                                                                                                                                                                       | The context to use for the request.                                                                                                                                                                                                                      |                                                                                                                                                                                                                                                          |
| `pageSize`                                                                                                                                                                                                                                               | `*int64`                                                                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The maximum number of results to return per page.<br/>                                                                                                                                                                                                   | 100                                                                                                                                                                                                                                                      |
| `cursor`                                                                                                                                                                                                                                                 | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | Parameter used in pagination requests. Maximum page size is set to 15.<br/>Set to the value of next for the next page of results.<br/>Set to the value of previous for the previous page of results.<br/>No other parameters can be set when this parameter is set.<br/> | aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ==                                                                                                                                                                                                             |
| `query`                                                                                                                                                                                                                                                  | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | JSON-encoded QueryBuilder filter. Omit it on cursor continuation requests.                                                                                                                                                                               |                                                                                                                                                                                                                                                          |
| `opts`                                                                                                                                                                                                                                                   | [][operations.Option](../../models/operations/option.md)                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The options for this request.                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |

### Response

**[*operations.ListAlertsResponse](../../models/operations/listalertsresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## GetAlert

Get an alert

### Example Usage

<!-- UsageSnippet language="go" operationID="getAlert" method="get" path="/alerts/{alertID}" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.GetAlert(ctx, "c7c54af9-81a4-4208-844b-4f25f89cf8a1")
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                | Type                                                     | Required                                                 | Description                                              |
| -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------- |
| `ctx`                                                    | [context.Context](https://pkg.go.dev/context#Context)    | :heavy_check_mark:                                       | The context to use for the request.                      |
| `alertID`                                                | `string`                                                 | :heavy_check_mark:                                       | The alert ID.                                            |
| `opts`                                                   | [][operations.Option](../../models/operations/option.md) | :heavy_minus_sign:                                       | The options for this request.                            |

### Response

**[*operations.GetAlertResponse](../../models/operations/getalertresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## ListAlertEvents

Returns a page of the events recorded for this alert — every evaluation
that touched it plus every manual transition. The list is append-only;
events are never modified or deleted. Ordered most-recent-first and
cursor-paginated: a long-lived alert's timeline is unbounded (one row per
failing evaluation), so callers must page through it.


### Example Usage

<!-- UsageSnippet language="go" operationID="listAlertEvents" method="get" path="/alerts/{alertID}/events" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ListAlertEvents(ctx, "259536e6-acd5-4e38-9154-10e46ea2bc63", client.Pointer[int64](100), client.Pointer("aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ=="))
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertEventsCursorResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                                                                                                                                                                                                | Type                                                                                                                                                                                                                                                     | Required                                                                                                                                                                                                                                                 | Description                                                                                                                                                                                                                                              | Example                                                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx`                                                                                                                                                                                                                                                    | [context.Context](https://pkg.go.dev/context#Context)                                                                                                                                                                                                    | :heavy_check_mark:                                                                                                                                                                                                                                       | The context to use for the request.                                                                                                                                                                                                                      |                                                                                                                                                                                                                                                          |
| `alertID`                                                                                                                                                                                                                                                | `string`                                                                                                                                                                                                                                                 | :heavy_check_mark:                                                                                                                                                                                                                                       | The alert ID.                                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |
| `pageSize`                                                                                                                                                                                                                                               | `*int64`                                                                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The maximum number of results to return per page.<br/>                                                                                                                                                                                                   | 100                                                                                                                                                                                                                                                      |
| `cursor`                                                                                                                                                                                                                                                 | `*string`                                                                                                                                                                                                                                                | :heavy_minus_sign:                                                                                                                                                                                                                                       | Parameter used in pagination requests. Maximum page size is set to 15.<br/>Set to the value of next for the next page of results.<br/>Set to the value of previous for the previous page of results.<br/>No other parameters can be set when this parameter is set.<br/> | aHR0cHM6Ly9nLnBhZ2UvTmVrby1SYW1lbj9zaGFyZQ==                                                                                                                                                                                                             |
| `opts`                                                                                                                                                                                                                                                   | [][operations.Option](../../models/operations/option.md)                                                                                                                                                                                                 | :heavy_minus_sign:                                                                                                                                                                                                                                       | The options for this request.                                                                                                                                                                                                                            |                                                                                                                                                                                                                                                          |

### Response

**[*operations.ListAlertEventsResponse](../../models/operations/listalerteventsresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## AckAlert

Acknowledge an alert

### Example Usage

<!-- UsageSnippet language="go" operationID="ackAlert" method="post" path="/alerts/{alertID}/ack" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.AckAlert(ctx, "5439ab64-6482-49fb-993f-3411bfe19fef", components.AckAlertRequest{
        By: "ops@buildr.com",
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                | Type                                                                     | Required                                                                 | Description                                                              |
| ------------------------------------------------------------------------ | ------------------------------------------------------------------------ | ------------------------------------------------------------------------ | ------------------------------------------------------------------------ |
| `ctx`                                                                    | [context.Context](https://pkg.go.dev/context#Context)                    | :heavy_check_mark:                                                       | The context to use for the request.                                      |
| `alertID`                                                                | `string`                                                                 | :heavy_check_mark:                                                       | The alert ID.                                                            |
| `body`                                                                   | [components.AckAlertRequest](../../models/components/ackalertrequest.md) | :heavy_check_mark:                                                       | N/A                                                                      |
| `opts`                                                                   | [][operations.Option](../../models/operations/option.md)                 | :heavy_minus_sign:                                                       | The options for this request.                                            |

### Response

**[*operations.AckAlertResponse](../../models/operations/ackalertresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## ResolveAlert

Resolve an alert (fixed_by_booking)

### Example Usage

<!-- UsageSnippet language="go" operationID="resolveAlert" method="post" path="/alerts/{alertID}/resolve" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.ResolveAlert(ctx, "53527ec3-b39f-4eee-ac1d-6e2bad87f240", components.ResolveAlertRequest{
        By: "<value>",
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                        | Type                                                                             | Required                                                                         | Description                                                                      |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `ctx`                                                                            | [context.Context](https://pkg.go.dev/context#Context)                            | :heavy_check_mark:                                                               | The context to use for the request.                                              |
| `alertID`                                                                        | `string`                                                                         | :heavy_check_mark:                                                               | The alert ID.                                                                    |
| `body`                                                                           | [components.ResolveAlertRequest](../../models/components/resolvealertrequest.md) | :heavy_check_mark:                                                               | N/A                                                                              |
| `opts`                                                                           | [][operations.Option](../../models/operations/option.md)                         | :heavy_minus_sign:                                                               | The options for this request.                                                    |

### Response

**[*operations.ResolveAlertResponse](../../models/operations/resolvealertresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## AcceptAlert

Accept an alert (accepted_by_business)

### Example Usage

<!-- UsageSnippet language="go" operationID="acceptAlert" method="post" path="/alerts/{alertID}/accept" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.AcceptAlert(ctx, "5550ef95-072d-4bbb-9d3b-6a9dd307b2bd", components.AcceptAlertRequest{
        By: "<value>",
        Note: "<value>",
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                      | Type                                                                           | Required                                                                       | Description                                                                    |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ |
| `ctx`                                                                          | [context.Context](https://pkg.go.dev/context#Context)                          | :heavy_check_mark:                                                             | The context to use for the request.                                            |
| `alertID`                                                                      | `string`                                                                       | :heavy_check_mark:                                                             | The alert ID.                                                                  |
| `body`                                                                         | [components.AcceptAlertRequest](../../models/components/acceptalertrequest.md) | :heavy_check_mark:                                                             | N/A                                                                            |
| `opts`                                                                         | [][operations.Option](../../models/operations/option.md)                       | :heavy_minus_sign:                                                             | The options for this request.                                                  |

### Response

**[*operations.AcceptAlertResponse](../../models/operations/acceptalertresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## SnoozeAlert

Mutes the alert's webhook notifications until `until`. The alert keeps
failing, keeps its status, and keeps counting against period-green —
only its notifications are suppressed, even if the discrepancy changes.
The first failing evaluation at or after `until` clears the snooze and
notifies once. Re-snoozing overwrites the window. Rejects RESOLVED
alerts and a non-future `until`.


### Example Usage

<!-- UsageSnippet language="go" operationID="snoozeAlert" method="post" path="/alerts/{alertID}/snooze" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/types"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.SnoozeAlert(ctx, "96529a25-9005-499e-a0ec-daa0ae32f4cb", components.SnoozeAlertRequest{
        By: "ops@buildr.com",
        Until: types.MustTimeFromString("2026-07-17T12:27:27.142Z"),
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                      | Type                                                                           | Required                                                                       | Description                                                                    |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ |
| `ctx`                                                                          | [context.Context](https://pkg.go.dev/context#Context)                          | :heavy_check_mark:                                                             | The context to use for the request.                                            |
| `alertID`                                                                      | `string`                                                                       | :heavy_check_mark:                                                             | The alert ID.                                                                  |
| `body`                                                                         | [components.SnoozeAlertRequest](../../models/components/snoozealertrequest.md) | :heavy_check_mark:                                                             | N/A                                                                            |
| `opts`                                                                         | [][operations.Option](../../models/operations/option.md)                       | :heavy_minus_sign:                                                             | The options for this request.                                                  |

### Response

**[*operations.SnoozeAlertResponse](../../models/operations/snoozealertresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |

## UnsnoozeAlert

Clears an active snooze before its window elapses. Idempotent —
unsnoozing an alert that is not snoozed returns it unchanged.


### Example Usage

<!-- UsageSnippet language="go" operationID="unsnoozeAlert" method="post" path="/alerts/{alertID}/unsnooze" -->
```go
package main

import(
	"context"
	"github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	"log"
)

func main() {
    ctx := context.Background()

    s := client.New(
        "https://api.example.com",
    )

    res, err := s.Reconciliation.V1.UnsnoozeAlert(ctx, "a1f12fdd-d9de-483a-b3c6-41ec79a76231", components.UnsnoozeAlertRequest{
        By: "ops@buildr.com",
    })
    if err != nil {
        log.Fatal(err)
    }
    if res.AlertResponse != nil {
        // handle response
    }
}
```

### Parameters

| Parameter                                                                          | Type                                                                               | Required                                                                           | Description                                                                        |
| ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| `ctx`                                                                              | [context.Context](https://pkg.go.dev/context#Context)                              | :heavy_check_mark:                                                                 | The context to use for the request.                                                |
| `alertID`                                                                          | `string`                                                                           | :heavy_check_mark:                                                                 | The alert ID.                                                                      |
| `body`                                                                             | [components.UnsnoozeAlertRequest](../../models/components/unsnoozealertrequest.md) | :heavy_check_mark:                                                                 | N/A                                                                                |
| `opts`                                                                             | [][operations.Option](../../models/operations/option.md)                           | :heavy_minus_sign:                                                                 | The options for this request.                                                      |

### Response

**[*operations.UnsnoozeAlertResponse](../../models/operations/unsnoozealertresponse.md), error**

### Errors

| Error Type         | Status Code        | Content Type       |
| ------------------ | ------------------ | ------------------ |
| apierrors.APIError | 4XX, 5XX           | \*/\*              |