// Package audit extracts a deterministic, auditable inventory of the
// Reconciliation HTTP surface from this repository's own OpenAPI document, and
// pins the legacy fctl command baseline it has to be measured against.
//
// The package is deliberately read-only and dependency-light: it is the
// preparation artefact for the fctl Reconciliation plugin (fctl-v2 programme
// Tasks 10A/10B) and must stay usable before any plugin runtime, component ABI,
// generated SDK, or transport exists. It contains no HTTP client, no plugin
// entry point, and no generated bindings.
package audit

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// TagV1 is the only tag the Reconciliation document uses. Unlike Payments,
// this document declares a single API generation, so there is no tag-based
// partition of the surface; the tranche split is by family instead.
const TagV1 = "reconciliation.v1"

// methodsOf returns the path item's declared operations in a fixed method
// order, so extraction output is stable for a given document.
func methodsOf(item yamlPathItem) []struct {
	method string
	op     *yamlOperation
} {
	return []struct {
		method string
		op     *yamlOperation
	}{
		{"GET", item.Get},
		{"PUT", item.Put},
		{"POST", item.Post},
		{"DELETE", item.Delete},
		{"PATCH", item.Patch},
		{"HEAD", item.Head},
		{"OPTIONS", item.Options},
		{"TRACE", item.Trace},
	}
}

// Parameter is one resolved OpenAPI parameter of an operation.
type Parameter struct {
	Name     string `json:"name" yaml:"name"`
	In       string `json:"in" yaml:"in"`
	Required bool   `json:"required" yaml:"required"`
}

// Operation is one (method, path) OpenAPI operation of the Reconciliation API.
//
// Every field is read from the document; nothing is inferred. In particular
// Scopes is nil when the operation declares no security block at all, which is
// a different, weaker fact than an empty declared scope array.
type Operation struct {
	OperationID     string      `json:"operationId"`
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	Tag             string      `json:"tag"`
	SDKMethod       string      `json:"sdkMethod"`
	Deprecated      bool        `json:"deprecated"`
	HasSecurity     bool        `json:"hasSecurity"`
	SecuritySchemes []string    `json:"securitySchemes"`
	Scopes          []string    `json:"scopes"`
	Parameters      []Parameter `json:"parameters"`
	RequestBody     string      `json:"requestBody"`
	SuccessCode     string      `json:"successCode"`
	SuccessBody     string      `json:"successBody"`
}

// Paginated reports whether the operation exposes the Reconciliation cursor
// pagination parameters. Both are always declared together in this document;
// the check requires both so a single stray name cannot fake pagination.
func (o Operation) Paginated() bool {
	var cursor, pageSize bool
	for _, p := range o.Parameters {
		switch p.Name {
		case "cursor":
			cursor = true
		case "pageSize":
			pageSize = true
		}
	}
	return cursor && pageSize
}

// HasRequestBody reports whether the operation declares a JSON request body.
func (o Operation) HasRequestBody() bool { return o.RequestBody != "" }

// Mutating reports whether the operation uses a state-changing HTTP method.
func (o Operation) Mutating() bool {
	switch o.Method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

// yaml shapes: only the fields this audit reads are modelled.

type yamlSchema struct {
	Ref string `yaml:"$ref"`
}

type yamlMediaType struct {
	Schema yamlSchema `yaml:"schema"`
}

type yamlBody struct {
	Ref     string                   `yaml:"$ref"`
	Content map[string]yamlMediaType `yaml:"content"`
}

type yamlResponse struct {
	Ref     string                   `yaml:"$ref"`
	Content map[string]yamlMediaType `yaml:"content"`
}

type yamlParameter struct {
	Ref      string `yaml:"$ref"`
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
}

type yamlOperation struct {
	OperationID  string                  `yaml:"operationId"`
	Tags         []string                `yaml:"tags"`
	Deprecated   bool                    `yaml:"deprecated"`
	NameOverride string                  `yaml:"x-speakeasy-name-override"`
	Security     []map[string][]string   `yaml:"security"`
	Parameters   []yamlParameter         `yaml:"parameters"`
	RequestBody  *yamlBody               `yaml:"requestBody"`
	Responses    map[string]yamlResponse `yaml:"responses"`
}

type yamlPathItem struct {
	Parameters []yamlParameter `yaml:"parameters"`
	Get        *yamlOperation  `yaml:"get"`
	Put        *yamlOperation  `yaml:"put"`
	Post       *yamlOperation  `yaml:"post"`
	Delete     *yamlOperation  `yaml:"delete"`
	Patch      *yamlOperation  `yaml:"patch"`
	Head       *yamlOperation  `yaml:"head"`
	Options    *yamlOperation  `yaml:"options"`
	Trace      *yamlOperation  `yaml:"trace"`
}

type yamlDocument struct {
	Info struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Security   []map[string][]string   `yaml:"security"`
	Paths      map[string]yamlPathItem `yaml:"paths"`
	Components struct {
		Parameters      map[string]yamlParameter `yaml:"parameters"`
		RequestBodies   map[string]yamlBody      `yaml:"requestBodies"`
		Responses       map[string]yamlResponse  `yaml:"responses"`
		Schemas         map[string]any           `yaml:"schemas"`
		SecuritySchemes map[string]any           `yaml:"securitySchemes"`
	} `yaml:"components"`
}

// Document is the small set of document-level facts the inventory quotes,
// separate from the per-operation records.
type Document struct {
	// Title and Version are read verbatim from `info`. Version is a build-time
	// placeholder in this repository; see divergence D5.
	Title   string `json:"title"`
	Version string `json:"version"`
	// DeclaredSecuritySchemes are the names defined under
	// components.securitySchemes, sorted. Empty means the document defines none.
	DeclaredSecuritySchemes []string `json:"declaredSecuritySchemes"`
	// ReferencedSecuritySchemes are the scheme names operations actually
	// reference in their security blocks, sorted.
	ReferencedSecuritySchemes []string `json:"referencedSecuritySchemes"`
	// HasRootSecurity reports whether the document declares a root-level
	// `security` key.
	HasRootSecurity bool `json:"hasRootSecurity"`
}

// UndefinedSecuritySchemes returns referenced scheme names that the document
// never defines. A non-empty result is a contract defect: the operations name
// an authentication mechanism the document does not describe.
func (d Document) UndefinedSecuritySchemes() []string {
	defined := map[string]struct{}{}
	for _, name := range d.DeclaredSecuritySchemes {
		defined[name] = struct{}{}
	}
	var missing []string
	for _, name := range d.ReferencedSecuritySchemes {
		if _, ok := defined[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// Load parses the Reconciliation OpenAPI document at specPath and returns its
// document-level facts plus every operation it declares, sorted by operationId
// so the result is byte-stable for a given document.
func Load(specPath string) (Document, []Operation, error) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return Document{}, nil, fmt.Errorf("read openapi document: %w", err)
	}

	var doc yamlDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Document{}, nil, fmt.Errorf("parse openapi document: %w", err)
	}

	var ops []Operation
	for path, item := range doc.Paths {
		for _, entry := range methodsOf(item) {
			if entry.op == nil || entry.op.OperationID == "" {
				continue
			}
			op, err := convert(&doc, path, entry.method, item.Parameters, *entry.op)
			if err != nil {
				return Document{}, nil, err
			}
			ops = append(ops, op)
		}
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].OperationID < ops[j].OperationID })

	meta := Document{
		Title:           doc.Info.Title,
		Version:         doc.Info.Version,
		HasRootSecurity: doc.Security != nil,
	}
	for name := range doc.Components.SecuritySchemes {
		meta.DeclaredSecuritySchemes = append(meta.DeclaredSecuritySchemes, name)
	}
	sort.Strings(meta.DeclaredSecuritySchemes)

	referenced := map[string]struct{}{}
	for _, op := range ops {
		for _, name := range op.SecuritySchemes {
			referenced[name] = struct{}{}
		}
	}
	for name := range referenced {
		meta.ReferencedSecuritySchemes = append(meta.ReferencedSecuritySchemes, name)
	}
	sort.Strings(meta.ReferencedSecuritySchemes)

	return meta, ops, nil
}

func convert(doc *yamlDocument, path, method string, shared []yamlParameter, raw yamlOperation) (Operation, error) {
	op := Operation{
		OperationID: raw.OperationID,
		Method:      method,
		Path:        path,
		Deprecated:  raw.Deprecated,
		SDKMethod:   raw.NameOverride,
	}
	if op.SDKMethod == "" {
		op.SDKMethod = raw.OperationID
	}
	if len(raw.Tags) != 1 {
		return Operation{}, fmt.Errorf("operation %s: expected exactly one tag, got %v", raw.OperationID, raw.Tags)
	}
	op.Tag = raw.Tags[0]

	if raw.Security != nil {
		op.HasSecurity = true
		op.Scopes = []string{}
		op.SecuritySchemes = []string{}
		for _, scheme := range raw.Security {
			for name, scopes := range scheme {
				op.SecuritySchemes = append(op.SecuritySchemes, name)
				op.Scopes = append(op.Scopes, scopes...)
			}
		}
		sort.Strings(op.Scopes)
		sort.Strings(op.SecuritySchemes)
	}

	for _, group := range [][]yamlParameter{shared, raw.Parameters} {
		for _, p := range group {
			resolved, err := resolveParameter(doc, p)
			if err != nil {
				return Operation{}, fmt.Errorf("operation %s: %w", raw.OperationID, err)
			}
			op.Parameters = append(op.Parameters, resolved)
		}
	}
	sort.Slice(op.Parameters, func(i, j int) bool {
		if op.Parameters[i].In != op.Parameters[j].In {
			return op.Parameters[i].In < op.Parameters[j].In
		}
		return op.Parameters[i].Name < op.Parameters[j].Name
	})

	if raw.RequestBody != nil {
		body, err := resolveBody(doc, *raw.RequestBody)
		if err != nil {
			return Operation{}, fmt.Errorf("operation %s: %w", raw.OperationID, err)
		}
		op.RequestBody = schemaName(body.Content)
	}

	code, response, err := successResponse(doc, raw.Responses)
	if err != nil {
		return Operation{}, fmt.Errorf("operation %s: %w", raw.OperationID, err)
	}
	op.SuccessCode = code
	op.SuccessBody = schemaName(response.Content)

	return op, nil
}

func resolveParameter(doc *yamlDocument, p yamlParameter) (Parameter, error) {
	if p.Ref != "" {
		name, err := refName(p.Ref, "parameters")
		if err != nil {
			return Parameter{}, err
		}
		target, ok := doc.Components.Parameters[name]
		if !ok {
			return Parameter{}, fmt.Errorf("unresolved parameter ref %q", p.Ref)
		}
		p = target
	}
	return Parameter{Name: p.Name, In: p.In, Required: p.Required}, nil
}

func resolveBody(doc *yamlDocument, b yamlBody) (yamlBody, error) {
	if b.Ref == "" {
		return b, nil
	}
	name, err := refName(b.Ref, "requestBodies")
	if err != nil {
		return yamlBody{}, err
	}
	target, ok := doc.Components.RequestBodies[name]
	if !ok {
		return yamlBody{}, fmt.Errorf("unresolved requestBody ref %q", b.Ref)
	}
	return target, nil
}

func resolveResponse(doc *yamlDocument, r yamlResponse) (yamlResponse, error) {
	if r.Ref == "" {
		return r, nil
	}
	name, err := refName(r.Ref, "responses")
	if err != nil {
		return yamlResponse{}, err
	}
	target, ok := doc.Components.Responses[name]
	if !ok {
		return yamlResponse{}, fmt.Errorf("unresolved response ref %q", r.Ref)
	}
	return target, nil
}

// successResponse returns the single declared 2xx response. The Reconciliation
// document declares exactly one success code per operation plus "default";
// anything else is a shape this audit refuses to guess about.
func successResponse(doc *yamlDocument, responses map[string]yamlResponse) (string, yamlResponse, error) {
	var codes []string
	for code := range responses {
		if strings.HasPrefix(code, "2") {
			codes = append(codes, code)
		}
	}
	if len(codes) != 1 {
		return "", yamlResponse{}, fmt.Errorf("expected exactly one 2xx response, got %v", codes)
	}
	resolved, err := resolveResponse(doc, responses[codes[0]])
	if err != nil {
		return "", yamlResponse{}, err
	}
	return codes[0], resolved, nil
}

func schemaName(content map[string]yamlMediaType) string {
	media, ok := content["application/json"]
	if !ok {
		return ""
	}
	if media.Schema.Ref == "" {
		return ""
	}
	name, err := refName(media.Schema.Ref, "schemas")
	if err != nil {
		return ""
	}
	return name
}

func refName(ref, kind string) (string, error) {
	prefix := "#/components/" + kind + "/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("ref %q is not a local %s ref", ref, kind)
	}
	return strings.TrimPrefix(ref, prefix), nil
}

// Index returns the operations keyed by operationId.
func Index(ops []Operation) map[string]Operation {
	out := make(map[string]Operation, len(ops))
	for _, op := range ops {
		out[op.OperationID] = op
	}
	return out
}
