// Package catalog describes the kie.ai models this CLI can drive.
//
// The catalog is generated from docs.kie.ai by cmd/catalog-gen and committed as
// catalog.json next to this file, so a build never depends on the network.
package catalog

// SchemaVersion identifies the shape of Catalog. Bump it whenever a consumer
// would misread an older file, so a stale catalog is rejected instead of
// silently misinterpreted.
const SchemaVersion = 1

// Catalog is the generated set of models, sorted by Model.ID.
//
// It carries no generation timestamp on purpose: the file is committed, so a
// byte-identical regeneration must produce no diff. Freshness is the build's
// concern, not the catalog's.
type Catalog struct {
	SchemaVersion int     `json:"schemaVersion"`
	Models        []Model `json:"models"`
}

// Style distinguishes the two ways kie.ai accepts a task creation request.
type Style string

const (
	// StyleMarket is the unified Market endpoint: one path for every model,
	// selected by the "model" field of the request body.
	StyleMarket Style = "market"
	// StyleDirect is a standard API with its own path per operation.
	StyleDirect Style = "direct"
)

// Model is one create/query pair, which is what a user invokes.
type Model struct {
	// ID is what the user types. Market models use their kie.ai "model" value
	// (e.g. "bytedance/seedream-v4-text-to-image"); standard APIs use their
	// docs path (e.g. "suno-api/generate-music"), a naming rule of our own.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Vendor      string `json:"vendor"`
	DocsURL     string `json:"docsUrl"`
	Create      Create `json:"create"`
	Query       Query  `json:"query"`
	// Input is the JSON Schema of the request payload the user supplies.
	Input map[string]any `json:"input"`
}

// Create is the endpoint that submits a task and returns a task id.
type Create struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Style  Style  `json:"style"`
	// Model is the value the Market endpoint requires in its request body.
	// Empty for StyleDirect.
	Model string `json:"model,omitempty"`
}

// Query is the endpoint that reports the state of a submitted task.
type Query struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Param is the query-string parameter that carries the task id.
	Param string `json:"param"`
}
