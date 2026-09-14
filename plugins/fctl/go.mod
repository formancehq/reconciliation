module github.com/formancehq/reconciliation/plugins/fctl

go 1.26.0

require (
	github.com/formancehq/fctl-v2-poc/pkg/plugin v0.0.0
	github.com/formancehq/reconciliation/pkg/client v0.0.0
	go.bytecodealliance.org/pkg v0.2.3
	gopkg.in/yaml.v3 v3.0.1
)

require google.golang.org/protobuf v1.36.12

replace github.com/formancehq/reconciliation/pkg/client => ../../pkg/client
