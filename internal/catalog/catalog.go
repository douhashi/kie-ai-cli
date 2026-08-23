// Package catalog describes the kie.ai models this CLI can drive.
//
// The catalog is generated from docs.kie.ai by cmd/catalog-gen and committed as
// catalog.json next to this file, which is embedded into the binary. Every
// model is therefore readable with no network at all; the price is that the
// catalog ages, which [Catalog.StaleWarning] reports.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SchemaVersion identifies the shape of Catalog. Bump it whenever a consumer
// would misread an older file, so a stale catalog is rejected instead of
// silently misinterpreted.
const SchemaVersion = 1

// MaxAge is how long an embedded catalog is taken at face value.
//
// Releases have no settled cadence before 1.0, so the span is wide enough not
// to nag someone running the newest binary, and short enough that one left
// alone for a quarter says so. #5 will measure how fast the catalog actually
// moves, and this line is where that answer lands.
const MaxAge = 90 * 24 * time.Hour

//go:embed catalog.json
var embeddedCatalog []byte

// GeneratedAtFile names the file beside catalog.json that holds the day the
// catalog was generated, as a single UTC date. cmd/catalog-gen writes it and
// the directive below embeds it, so the two must agree: were they to drift, the
// binary would go on embedding a date that nothing updates any more.
const GeneratedAtFile = "generated_at.txt"

// embeddedGeneratedAt holds GeneratedAtFile. The date is a file of its own
// rather than a field of catalog.json so that a regeneration finding nothing
// new produces no diff at all, and it is not taken from the build: `go install`
// records no VCS time, and a timestamp baked in at compile time would break
// reproducible builds.
//
//go:embed generated_at.txt
var embeddedGeneratedAt string

// Catalog is the generated set of models, sorted by Model.ID.
type Catalog struct {
	SchemaVersion int     `json:"schemaVersion"`
	Models        []Model `json:"models"`
	// GeneratedAt is the day the catalog was generated, in UTC. It is kept out
	// of the JSON on purpose; see embeddedGeneratedAt.
	GeneratedAt time.Time `json:"-"`
}

// load parses the embedded catalog once and hands the same value to everyone.
var load = sync.OnceValues(parseEmbedded)

// Load returns the catalog embedded in this binary.
//
// The result is shared between callers and must be treated as read-only.
func Load() (Catalog, error) {
	return load()
}

func parseEmbedded() (Catalog, error) {
	var parsed Catalog
	if err := json.Unmarshal(embeddedCatalog, &parsed); err != nil {
		return Catalog{}, fmt.Errorf("decode the embedded catalog: %w", err)
	}
	if parsed.SchemaVersion != SchemaVersion {
		return Catalog{}, fmt.Errorf("the embedded catalog is schema version %d, but this binary reads version %d",
			parsed.SchemaVersion, SchemaVersion)
	}
	generatedAt, err := time.Parse(time.DateOnly, strings.TrimSpace(embeddedGeneratedAt))
	if err != nil {
		return Catalog{}, fmt.Errorf("read the embedded generation date: %w", err)
	}
	parsed.GeneratedAt = generatedAt
	return parsed, nil
}

// StaleWarning describes the catalog's age once it exceeds MaxAge, and returns
// an empty string while it is still fresh.
//
// It only reports; the caller decides what to do with it, and writes it to
// stderr so that --json output stays a clean document.
func (c Catalog) StaleWarning(now time.Time) string {
	age := now.Sub(c.GeneratedAt)
	if age < MaxAge {
		return ""
	}
	return fmt.Sprintf("the built-in model catalog was generated on %s, %d days ago",
		c.GeneratedAt.Format(time.DateOnly), int(age/(24*time.Hour)))
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
