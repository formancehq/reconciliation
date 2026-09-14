package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostprocessorRemovesInventedAuthAndRepairsGeneratedContract(t *testing.T) {
	clientRoot := t.TempDir()
	writeFixture(t, filepath.Join(clientRoot, "internal/utils/json.go"), strings.Repeat("json.Unmarshal(", expectedUnmarshalCalls))
	writeFixture(t, filepath.Join(clientRoot, "formance.go"), generatedFormanceFixture)
	writeFixture(t, filepath.Join(clientRoot, "README.md"), generatedReadmeFixture)
	writeFixture(t, filepath.Join(clientRoot, "USAGE.md"), generatedUsageFixture)
	writeFixture(t, filepath.Join(clientRoot, "docs/sdks/v1/README.md"), generatedUsageFixture)
	writeFixture(t, filepath.Join(clientRoot, "docs/models/operations/option.md"), generatedOptionsFixture)
	writeFixture(t, filepath.Join(clientRoot, "models/components/security.go"), "package components\n\ntype Security struct{}\n")
	writeFixture(t, filepath.Join(clientRoot, "docs/models/components/security.md"), "# Security\n")

	command := exec.Command("go", "run", ".", "-client", clientRoot)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("postprocess generated client: %v\n%s", err, output)
	}

	formance := readFixture(t, filepath.Join(clientRoot, "formance.go"))
	if strings.Contains(formance, "WithSecurity") || strings.Index(formance, "ServerURL: serverURL") > strings.Index(formance, "for _, opt := range opts") {
		t.Fatalf("generated client retained auth or applies constructor URL after options:\n%s", formance)
	}
	if strings.Contains(formance, "\n\n\n\tsdk.sdkConfiguration = sdk.hooks.SDKInit") {
		t.Fatal("generated client retained an authentication-removal blank line")
	}
	for _, removed := range []string{"models/components/security.go", "docs/models/components/security.md"} {
		if _, err := os.Stat(filepath.Join(clientRoot, removed)); !os.IsNotExist(err) {
			t.Fatalf("generated auth artefact %s was not removed", removed)
		}
	}

	readme := readFixture(t, filepath.Join(clientRoot, "README.md"))
	for _, forbidden := range []string{"client.WithSecurity", "go get github.com/formancehq/reconciliation/pkg/client", "This SDK is in beta", "client.New(client.WithClient"} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("generated README retained unsupported contract %q", forbidden)
		}
	}
	if !strings.Contains(readme, `client.New("https://api.example.com", client.WithClient(httpClient))`) {
		t.Fatal("generated README does not contain a compilable injected-client example")
	}
	for _, relativePath := range []string{"USAGE.md", "docs/sdks/v1/README.md", "docs/models/operations/option.md"} {
		contents := readFixture(t, filepath.Join(clientRoot, relativePath))
		if strings.Contains(contents, "WithSecurity") {
			t.Fatalf("generated documentation %s retained invented auth", relativePath)
		}
	}
}

func writeFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

const generatedFormanceFixture = `package client

import "github.com/formancehq/reconciliation/pkg/client/models/components"

type SDKConfiguration struct { ServerURL string }
type Formance struct { sdkConfiguration SDKConfiguration }
type SDKOption func(*Formance)
func WithServerURL(serverURL string) SDKOption { return func(sdk *Formance) { sdk.sdkConfiguration.ServerURL = serverURL } }
func WithSecurity(authorization string) SDKOption {
	return func(sdk *Formance) { _ = components.Security{Authorization: authorization} }
}
func WithSecuritySource(source func() components.Security) SDKOption {
	return func(sdk *Formance) { _ = source }
}
func New(serverURL string, opts ...SDKOption) *Formance {
	sdk := &Formance{}
	for _, opt := range opts {
		opt(sdk)
	}
	sdk.sdkConfiguration.ServerURL = serverURL


	sdk.sdkConfiguration = sdk.hooks.SDKInit(sdk.sdkConfiguration)
	return sdk
}
`

const generatedReadmeFixture = `<!-- Start SDK Installation [installation] -->
go get github.com/formancehq/reconciliation/pkg/client
<!-- End SDK Installation [installation] -->
		client.WithSecurity("<YOUR_API_KEY_HERE>"),
<!-- Start Authentication [security] -->
API key
<!-- End Authentication [security] -->
sdkClient  = client.New(client.WithClient(httpClient))
## Maturity
This SDK is in beta.
## Contributions
`

const generatedUsageFixture = `s := client.New(
	"https://api.example.com",
	client.WithSecurity("<YOUR_API_KEY_HERE>"),
)
`

const generatedOptionsFixture = `# Options

### WithSecurity

client.WithSecurity(/* ... */)

### WithSecuritySource

client.WithSecuritySource(/* ... */)

### WithRetryConfig
`
