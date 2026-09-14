package core

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
)

func TestEveryCommandExecutesItsDeclaredOperation(t *testing.T) {
	for _, command := range Catalogue() {
		command := command
		t.Run(command.ID, func(t *testing.T) {
			responseBody := []byte(`{"data":{"id":"ok"}}`)
			status := int32(200)
			if command.Operations[0].ID == "createPolicy" || command.Operations[0].ID == "createRule" {
				status = 201
			}
			if command.Pagination.Supported {
				responseBody = []byte(`{"cursor":{"pageSize":1,"hasMore":false,"data":[]}}`)
			}
			if command.Operations[0].HTTP.Method == "DELETE" {
				responseBody, status = nil, 204
			}
			host := sdk.NewMemoryHost(func(_ context.Context, got sdk.Request) (sdk.Responses, error) {
				if got.Operation != command.Operations[0].ID || got.Service != sdk.ServiceReconciliation || got.Capability != "auth.stack" {
					t.Fatalf("request identity = %#v", got)
				}
				return sdk.NewResponseStream(sdk.Response{Status: status, ContentType: "application/json", Body: responseBody}), nil
			})
			arguments := make([]string, len(command.Arguments))
			for index := range arguments {
				arguments[index] = "id-value"
			}
			flags := []sdk.FlagOccurrence{}
			for _, flag := range command.Flags {
				if flag.Required {
					flags = append(flags, sdk.FlagOccurrence{Name: flag.Name, Value: `{}`})
				}
			}
			err := (Plugin{}).Execute(context.Background(), sdk.ExecuteRequest{CommandID: command.ID, Arguments: arguments, Flags: flags, Target: sdk.TargetSelection{OrganizationID: "org", StackID: "stack"}, ServiceVersions: []sdk.ServiceVersion{{Service: sdk.ServiceReconciliation, Version: "1.0.0", Major: 1}}}, host)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if len(host.Requests()) != 1 || len(host.Events()) != 1 {
				t.Fatalf("requests/events = %d/%d", len(host.Requests()), len(host.Events()))
			}
			if strings.Contains(host.Requests()[0].HTTP.Path, "{") {
				t.Fatalf("path placeholder remains: %q", host.Requests()[0].HTTP.Path)
			}
			result := host.Events()[0].Result
			if result.OperationID != command.ID {
				t.Fatalf("result operation ID = %q, want command ID %q", result.OperationID, command.ID)
			}
			if command.Operations[0].HTTP.Method == "DELETE" && (result.Shape != sdk.ResultObject || string(result.Data) != `{}`) {
				t.Fatalf("DELETE result = shape %q data %s, want object {}", result.Shape, result.Data)
			}
		})
	}
}

func TestListUsesLosslessQueryStringAndTraversesOpaqueCursors(t *testing.T) {
	call := 0
	const filter = `{"exact":9007199254740993}`
	host := sdk.NewMemoryHost(func(_ context.Context, got sdk.Request) (sdk.Responses, error) {
		call++
		if got.HTTP.Method != "GET" {
			t.Fatalf("method = %q", got.HTTP.Method)
		}
		if call == 1 {
			if len(got.HTTP.Body) != 0 || got.HTTP.ContentType != "" {
				t.Fatalf("browser-incompatible GET body emitted: %#v", got.HTTP)
			}
			if values := got.HTTP.Query["query"]; len(values) != 1 || values[0] != filter {
				t.Fatalf("lossless query parameter = %#v, want %s", values, filter)
			}
		}
		body := `{"cursor":{"pageSize":1,"hasMore":true,"next":"opaque/+1","data":[{"id":"1"}]}}`
		if call == 2 {
			if len(got.HTTP.Query) != 1 || len(got.HTTP.Query["cursor"]) != 1 || got.HTTP.Query["cursor"][0] != "opaque/+1" {
				t.Fatalf("continuation query is not cursor-only: %#v", got.HTTP.Query)
			}
			if len(got.HTTP.Body) != 0 || got.HTTP.ContentType != "" {
				t.Fatalf("continuation request replayed initial body: %#v", got.HTTP)
			}
			body = `{"cursor":{"pageSize":1,"hasMore":false,"data":[{"id":"2"}]}}`
		}
		return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(body)}), nil
	})
	err := (Plugin{}).Execute(context.Background(), validRequest("reconciliation.v1.policies.list", []sdk.FlagOccurrence{{Name: "query", Value: filter}, {Name: "page-size", Value: "1"}}, sdk.AllPagesContinuationControl()), host)
	if err != nil {
		t.Fatal(err)
	}
	if call != 2 {
		t.Fatalf("calls = %d", call)
	}
	var items []map[string]any
	if err := json.Unmarshal(host.Events()[0].Result.Data, &items); err != nil || len(items) != 2 {
		t.Fatalf("result = %s, err=%v", host.Events()[0].Result.Data, err)
	}
	if host.Events()[0].Result.Page != nil {
		t.Fatal("aggregate result exposes page cursor")
	}
}

func TestEveryFilteredListUsesTheBrowserPortableQueryParameter(t *testing.T) {
	const filter = `{"exact":9007199254740993}`
	for _, commandID := range []string{
		"reconciliation.v1.policies.list",
		"reconciliation.v1.list",
		"reconciliation.v1.rules.list",
		"reconciliation.v1.evaluations.list",
		"reconciliation.v1.alerts.list",
	} {
		t.Run(commandID, func(t *testing.T) {
			host := sdk.NewMemoryHost(func(_ context.Context, request sdk.Request) (sdk.Responses, error) {
				if len(request.HTTP.Body) != 0 || request.HTTP.ContentType != "" {
					t.Fatalf("browser-incompatible GET body emitted: %#v", request.HTTP)
				}
				if values := request.HTTP.Query["query"]; len(values) != 1 || values[0] != filter {
					t.Fatalf("lossless query parameter = %#v, want %s", values, filter)
				}
				return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(`{"cursor":{"pageSize":1,"hasMore":false,"data":[]}}`)}), nil
			})
			request := validRequest(commandID, []sdk.FlagOccurrence{{Name: "query", Value: filter}}, sdk.SinglePageContinuationControl())
			if err := (Plugin{}).Execute(context.Background(), request, host); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGeneratedRequestsPreserveFreeFormIntegerTokens(t *testing.T) {
	for _, test := range []struct {
		commandID string
		flag      string
		field     string
		status    int32
		response  string
	}{
		{"reconciliation.v1.policies.create", `{"name":"p","ledgerName":"default","ledgerQuery":{"exact":9007199254740993},"paymentsPoolID":"pool"}`, "ledgerQuery", 201, `{"data":{"id":"p"}}`},
		{"reconciliation.v1.rules.create", `{"name":"r","templateKind":"drift","templateSpec":{"exact":9007199254740993}}`, "templateSpec", 201, `{"data":{"id":"r"}}`},
	} {
		t.Run(test.field, func(t *testing.T) {
			host := sdk.NewMemoryHost(func(_ context.Context, request sdk.Request) (sdk.Responses, error) {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(request.HTTP.Body, &object); err != nil {
					t.Fatal(err)
				}
				var freeForm map[string]json.RawMessage
				if err := json.Unmarshal(object[test.field], &freeForm); err != nil {
					t.Fatal(err)
				}
				if got := string(freeForm["exact"]); got != "9007199254740993" {
					t.Fatalf("%s integer token = %s", test.field, got)
				}
				return sdk.NewResponseStream(sdk.Response{Status: test.status, ContentType: "application/json", Body: []byte(test.response)}), nil
			})
			request := validRequest(test.commandID, []sdk.FlagOccurrence{{Name: "body", Value: test.flag}}, sdk.SinglePageContinuationControl())
			if err := (Plugin{}).Execute(context.Background(), request, host); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGeneratedResponsesPreserveFreeFormIntegerTokens(t *testing.T) {
	for _, test := range []struct {
		name      string
		commandID string
		response  string
		field     string
	}{
		{"evidence", "reconciliation.v1.alerts.get", `{"data":{"id":"a","evidence":{"exact":9007199254740993}}}`, "evidence"},
		{"payload", "reconciliation.v1.alerts.events", `{"cursor":{"pageSize":1,"hasMore":false,"data":[{"id":"e","payload":{"exact":9007199254740993}}]}}`, "payload"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := sdk.NewMemoryHost(func(_ context.Context, _ sdk.Request) (sdk.Responses, error) {
				return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(test.response)}), nil
			})
			if err := (Plugin{}).Execute(context.Background(), validRequest(test.commandID, nil, sdk.SinglePageContinuationControl()), host); err != nil {
				t.Fatal(err)
			}
			data := host.Events()[0].Result.Data
			if test.name == "payload" {
				var items []json.RawMessage
				if err := json.Unmarshal(data, &items); err != nil || len(items) != 1 {
					t.Fatalf("decode items: %v (%s)", err, data)
				}
				data = items[0]
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			var freeForm map[string]json.RawMessage
			if err := json.Unmarshal(object[test.field], &freeForm); err != nil {
				t.Fatal(err)
			}
			if got := string(freeForm["exact"]); got != "9007199254740993" {
				t.Fatalf("%s integer token = %s", test.field, got)
			}
		})
	}
}

func TestPaginationMeasuresTheExactFinalJSONArray(t *testing.T) {
	item := json.RawMessage(`{"value":"exact"}`)
	encoded, err := json.Marshal([]json.RawMessage{item})
	if err != nil {
		t.Fatal(err)
	}
	control := sdk.AllPagesContinuationControl()
	exact := uint64(len(encoded))
	control.MaxBytes = exact
	host := sdk.NewMemoryHost(nil)
	if err := executeGeneratedPages(context.Background(), host, "op", nil, control, func(*int64, *string, *string) (generatedPage[json.RawMessage], error) {
		return generatedPage[json.RawMessage]{Data: []json.RawMessage{item}}, nil
	}); err != nil {
		t.Fatalf("exact %d-byte aggregate rejected: %v", exact, err)
	}
	oversized := json.RawMessage(`{"value":"exact!"}`)
	if err := executeGeneratedPages(context.Background(), sdk.NewMemoryHost(nil), "op", nil, control, func(*int64, *string, *string) (generatedPage[json.RawMessage], error) {
		return generatedPage[json.RawMessage]{Data: []json.RawMessage{oversized}}, nil
	}); err == nil {
		t.Fatalf("aggregate one byte above the exact %d-byte limit was accepted", exact)
	}
}

func TestPaginationFailsClosedOnPageItemAndFetchFailures(t *testing.T) {
	item := json.RawMessage(`{"id":"1"}`)
	endless := func(calls *int) func(*int64, *string, *string) (generatedPage[json.RawMessage], error) {
		return func(*int64, *string, *string) (generatedPage[json.RawMessage], error) {
			*calls++
			next := "cursor-" + strconv.Itoa(*calls)
			return generatedPage[json.RawMessage]{Data: []json.RawMessage{item}, Next: &next, HasMore: true}, nil
		}
	}
	t.Run("page-limit", func(t *testing.T) {
		control := sdk.AllPagesContinuationControl()
		control.MaxPages = 2
		host := sdk.NewMemoryHost(nil)
		calls := 0
		if err := executeGeneratedPages(context.Background(), host, "op", nil, control, endless(&calls)); err == nil {
			t.Fatal("page limit overflow accepted")
		}
		if calls != 2 {
			t.Fatalf("fetch calls = %d, want the exact page limit", calls)
		}
		if len(host.Events()) != 0 {
			t.Fatal("partial aggregate emitted after page limit failure")
		}
	})
	t.Run("item-limit", func(t *testing.T) {
		control := sdk.AllPagesContinuationControl()
		control.MaxItems = 1
		host := sdk.NewMemoryHost(nil)
		if err := executeGeneratedPages(context.Background(), host, "op", nil, control, func(*int64, *string, *string) (generatedPage[json.RawMessage], error) {
			return generatedPage[json.RawMessage]{Data: []json.RawMessage{item, item}}, nil
		}); err == nil {
			t.Fatal("item limit overflow accepted")
		}
		if len(host.Events()) != 0 {
			t.Fatal("partial aggregate emitted after item limit failure")
		}
	})
	t.Run("mid-pagination-fetch-error", func(t *testing.T) {
		host := sdk.NewMemoryHost(nil)
		calls := 0
		next := "second"
		if err := executeGeneratedPages(context.Background(), host, "op", nil, sdk.AllPagesContinuationControl(), func(*int64, *string, *string) (generatedPage[json.RawMessage], error) {
			calls++
			if calls > 1 {
				return generatedPage[json.RawMessage]{}, errors.New("product unavailable")
			}
			return generatedPage[json.RawMessage]{Data: []json.RawMessage{item}, Next: &next, HasMore: true}, nil
		}); err == nil {
			t.Fatal("mid-pagination fetch failure accepted")
		}
		if len(host.Events()) != 0 {
			t.Fatal("partial aggregate emitted after mid-pagination fetch failure")
		}
	})
}

func TestPaginationRejectsIncoherentCursorMetadata(t *testing.T) {
	for name, body := range map[string]string{
		"has-more-without-next": `{"cursor":{"pageSize":1,"hasMore":true,"data":[]}}`,
		"terminal-with-next":    `{"cursor":{"pageSize":1,"hasMore":false,"next":"stale","data":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			host := sdk.NewMemoryHost(func(_ context.Context, _ sdk.Request) (sdk.Responses, error) {
				return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(body)}), nil
			})
			if err := (Plugin{}).Execute(context.Background(), validRequest("reconciliation.v1.policies.list", nil, sdk.SinglePageContinuationControl()), host); err == nil {
				t.Fatal("incoherent cursor metadata accepted")
			}
			if len(host.Events()) != 0 {
				t.Fatal("incoherent cursor metadata emitted a result")
			}
		})
	}
}

func TestSinglePageReturnsOpaqueContinuation(t *testing.T) {
	host := sdk.NewMemoryHost(func(_ context.Context, _ sdk.Request) (sdk.Responses, error) {
		return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(`{"cursor":{"pageSize":1,"hasMore":true,"next":"next","data":[]}}`)}), nil
	})
	if err := (Plugin{}).Execute(context.Background(), validRequest("reconciliation.v1.alerts.events", []sdk.FlagOccurrence{{Name: "cursor", Value: "start"}, {Name: "page-size", Value: "1"}}, sdk.SinglePageContinuationControl()), host); err != nil {
		t.Fatal(err)
	}
	page := host.Events()[0].Result.Page
	if page == nil || !page.HasMore || page.NextCursor != "next" {
		t.Fatalf("page = %#v", page)
	}
}

func TestInvalidInputFailsBeforeProductTraffic(t *testing.T) {
	tests := []sdk.ExecuteRequest{
		validRequest("unknown", nil, sdk.SinglePageContinuationControl()),
		validRequest("reconciliation.v1.rules.create", []sdk.FlagOccurrence{{Name: "body", Value: "not-json"}}, sdk.SinglePageContinuationControl()),
		validRequest("reconciliation.v1.alerts.events", []sdk.FlagOccurrence{{Name: "page-size", Value: "101"}}, sdk.SinglePageContinuationControl()),
	}
	for _, request := range tests {
		host := sdk.NewMemoryHost(nil)
		if err := (Plugin{}).Execute(context.Background(), request, host); err == nil {
			t.Fatalf("%s unexpectedly succeeded", request.CommandID)
		}
		if len(host.Requests()) != 0 {
			t.Fatalf("%s made product traffic", request.CommandID)
		}
	}
}

func TestPaginationRejectsCursorCycles(t *testing.T) {
	host := sdk.NewMemoryHost(func(_ context.Context, _ sdk.Request) (sdk.Responses, error) {
		return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(`{"cursor":{"pageSize":1,"hasMore":true,"next":"same","data":[]}}`)}), nil
	})
	request := validRequest("reconciliation.v1.alerts.events", []sdk.FlagOccurrence{{Name: "cursor", Value: "same"}}, sdk.AllPagesContinuationControl())
	if err := (Plugin{}).Execute(context.Background(), request, host); err == nil {
		t.Fatal("cursor cycle accepted")
	}
}

func TestGeneratedClientLeavesAuthenticationAndRetryToTheHost(t *testing.T) {
	calls := 0
	host := sdk.NewMemoryHost(func(_ context.Context, request sdk.Request) (sdk.Responses, error) {
		calls++
		if _, ok := request.HTTP.Headers["authorization"]; ok {
			t.Fatalf("generated client injected authorization: %#v", request.HTTP.Headers)
		}
		return sdk.NewResponseStream(sdk.Response{Status: 503, ContentType: "application/json", Body: []byte(`{"errorCode":"unavailable","errorMessage":"try later"}`)}), nil
	})
	err := (Plugin{}).Execute(context.Background(), validRequest("reconciliation.v1.policies.get", nil, sdk.SinglePageContinuationControl()), host)
	if err == nil {
		t.Fatal("product failure unexpectedly succeeded")
	}
	if calls != 1 {
		t.Fatalf("Host.Request calls = %d, want one with generated retry disabled", calls)
	}
}

func TestProductHTTPErrorsSurfaceAsTypedFailuresCarryingTheirStatus(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int32
		retryable bool
	}{
		// 3xx is the class that changed shape without changing meaning: the
		// pinned SDK stopped folding it into the opaque response failure, and
		// a product redirect is the least expected of the three.
		{"redirection", 302, false},
		{"client-error", 404, false},
		{"server-error", 503, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := sdk.NewMemoryHost(func(_ context.Context, _ sdk.Request) (sdk.Responses, error) {
				return sdk.NewResponseStream(sdk.Response{Status: test.status, ContentType: "application/json", Body: []byte(`{"errorCode":"x","errorMessage":"y"}`)}), nil
			})
			err := (Plugin{}).Execute(context.Background(), validRequest("reconciliation.v1.policies.get", nil, sdk.SinglePageContinuationControl()), host)
			if err == nil {
				t.Fatalf("HTTP %d accepted", test.status)
			}
			var failure sdk.Failure
			if !errors.As(err, &failure) {
				t.Fatalf("error is not a typed SDK failure: %v", err)
			}
			if failure.Code != string(sdk.FailureProductHTTPError) {
				t.Fatalf("failure code = %q, want %q", failure.Code, sdk.FailureProductHTTPError)
			}
			if failure.Retryable != test.retryable {
				t.Fatalf("retryable = %v, want %v", failure.Retryable, test.retryable)
			}
			var details struct {
				HTTPStatus int32 `json:"httpStatus"`
			}
			if err := json.Unmarshal(failure.Details, &details); err != nil {
				t.Fatalf("decode failure details: %v (%s)", err, failure.Details)
			}
			if details.HTTPStatus != test.status {
				t.Fatalf("failure details httpStatus = %d, want %d", details.HTTPStatus, test.status)
			}
			if strings.Contains(string(failure.Details), "errorMessage") {
				t.Fatalf("failure details leaked the product response body: %s", failure.Details)
			}
			if len(host.Events()) != 0 {
				t.Fatalf("HTTP %d emitted a plausible result", test.status)
			}
		})
	}
}

func TestGeneratedDeleteRejectsUnexpectedSuccessStatus(t *testing.T) {
	host := sdk.NewMemoryHost(func(_ context.Context, _ sdk.Request) (sdk.Responses, error) {
		return sdk.NewResponseStream(sdk.Response{Status: 200, ContentType: "application/json", Body: []byte(`{}`)}), nil
	})
	err := (Plugin{}).Execute(context.Background(), validRequest("reconciliation.v1.policies.delete", nil, sdk.SinglePageContinuationControl()), host)
	if err == nil {
		t.Fatal("delete accepted a status other than the generated 204 contract")
	}
	if len(host.Events()) != 0 {
		t.Fatal("failed delete emitted a plausible result")
	}
}

func validRequest(id string, flags []sdk.FlagOccurrence, continuation sdk.ContinuationControl) sdk.ExecuteRequest {
	arguments := []string{}
	if command, _, ok := commandAndSpec(id); ok {
		arguments = make([]string, len(command.Arguments))
		for index := range arguments {
			arguments[index] = "id"
		}
	}
	return sdk.ExecuteRequest{CommandID: id, Arguments: arguments, Flags: flags, Target: sdk.TargetSelection{OrganizationID: "org", StackID: "stack"}, ServiceVersions: []sdk.ServiceVersion{{Service: sdk.ServiceReconciliation, Version: "1.0.0", Major: 1}}, Continuation: continuation}
}
