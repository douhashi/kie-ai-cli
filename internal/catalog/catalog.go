// Package catalog describes the kie.ai models this CLI can drive.
//
// The catalog is generated from docs.kie.ai by cmd/catalog-gen and committed as
// catalog.json next to this file, which is embedded into the binary. Every
// model is therefore readable with no network at all; the price is that the
// catalog ages, which [Catalog.StaleWarning] reports.
//
// [Update] downloads the published catalog into a directory of the user's own,
// and [Load] reads that copy in preference to the embedded one. The embedded
// catalog is never overwritten: it is what the binary falls back to when the
// directory is emptied, and what a fresh install runs on.
package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// CatalogFile names the generated catalog itself. The published asset, the
// downloaded copy and the embedded one all go by it, so that a user who looks
// in the directory sees the same file the repository holds.
const CatalogFile = "catalog.json"

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

// Origin says which of the two catalogs a value came from. It is reported to
// the user, because the two age for different reasons and are refreshed by
// different acts: one by installing a new binary, the other by downloading.
type Origin string

const (
	// OriginBuiltIn is the catalog embedded in this binary.
	OriginBuiltIn Origin = "built-in"
	// OriginDownloaded is the catalog `catalog update` fetched.
	OriginDownloaded Origin = "downloaded"
)

// Catalog is the generated set of models, sorted by Model.ID.
type Catalog struct {
	SchemaVersion int     `json:"schemaVersion"`
	Models        []Model `json:"models"`
	// GeneratedAt is the day the catalog was generated, in UTC. It is kept out
	// of the JSON on purpose; see embeddedGeneratedAt.
	GeneratedAt time.Time `json:"-"`
	// Origin is where this value was read from, and Path the directory holding
	// it, which is empty for OriginBuiltIn. Both describe this copy rather
	// than the generated catalog, so neither belongs in the file.
	Origin Origin `json:"-"`
	Path   string `json:"-"`
}

// Load returns the catalog in effect: the one downloaded into dir if there is
// one, and the one embedded in this binary otherwise.
//
// A dir holding a catalog that cannot be read is a failure rather than an
// absence. Falling back would answer from the embedded catalog while a
// downloaded one sits unread beside it, so the origin reported to the user
// would be a lie and the broken files would never be noticed.
func Load(dir string) (Catalog, error) {
	downloaded, found, err := loadDir(dir)
	if err != nil || found {
		return downloaded, err
	}
	embedded, err := parse(embeddedCatalog, embeddedGeneratedAt)
	if err != nil {
		return Catalog{}, fmt.Errorf("the catalog built into this binary is unusable: %w", err)
	}
	embedded.Origin = OriginBuiltIn
	return embedded, nil
}

// loadDir reads the downloaded pair, reporting false when dir holds neither
// half of it. Holding one half is not an absence: it is an update that was
// interrupted between its two renames, and the missing file is the one thing
// that would go unnoticed.
func loadDir(dir string) (Catalog, bool, error) {
	catalogJSON, catalogErr := os.ReadFile(filepath.Join(dir, CatalogFile))
	generatedAt, dateErr := os.ReadFile(filepath.Join(dir, GeneratedAtFile))
	if errors.Is(catalogErr, fs.ErrNotExist) && errors.Is(dateErr, fs.ErrNotExist) {
		return Catalog{}, false, nil
	}
	if catalogErr != nil {
		return Catalog{}, false, unusable(dir, catalogErr)
	}
	if dateErr != nil {
		return Catalog{}, false, unusable(dir, dateErr)
	}
	parsed, err := parse(catalogJSON, string(generatedAt))
	if err != nil {
		return Catalog{}, false, unusable(dir, err)
	}
	parsed.Origin = OriginDownloaded
	parsed.Path = dir
	return parsed, true, nil
}

// unusable reports a downloaded catalog that cannot be read, with both ways
// out of it: the user is not expected to know that emptying the directory is
// what returns the binary to the catalog it carries.
func unusable(dir string, err error) error {
	return fmt.Errorf("the downloaded catalog in %s is unusable: %w; run `catalog update` to download it again, or delete the directory to go back to the catalog built into this binary", dir, err)
}

// parse turns the two files a catalog is made of into one value, whichever
// copy they came from. Both copies are checked the same way, so a published
// catalog this binary could not read is refused before it is written to disk.
func parse(catalogJSON []byte, generatedAt string) (Catalog, error) {
	var parsed Catalog
	if err := json.Unmarshal(catalogJSON, &parsed); err != nil {
		return Catalog{}, fmt.Errorf("decode %s: %w", CatalogFile, err)
	}
	if parsed.SchemaVersion != SchemaVersion {
		return Catalog{}, fmt.Errorf("%s is schema version %d, but this binary reads version %d",
			CatalogFile, parsed.SchemaVersion, SchemaVersion)
	}
	day, err := time.Parse(time.DateOnly, strings.TrimSpace(generatedAt))
	if err != nil {
		return Catalog{}, fmt.Errorf("read %s: %w", GeneratedAtFile, err)
	}
	parsed.GeneratedAt = day
	return parsed, nil
}

// StaleWarning describes the catalog's age once it exceeds MaxAge, and returns
// an empty string while it is still fresh.
//
// It names the origin because that is what decides the remedy: a built-in
// catalog is refreshed by downloading one, a downloaded one only by
// downloading it again -- and if that changes nothing, the publishing side has
// stopped, which is worth knowing.
//
// It only reports; the caller decides what to do with it, and writes it to
// stderr so that --json output stays a clean document.
func (c Catalog) StaleWarning(now time.Time) string {
	age := now.Sub(c.GeneratedAt)
	if age < MaxAge {
		return ""
	}
	return fmt.Sprintf("the %s model catalog was generated on %s, %d days ago",
		c.Origin, c.GeneratedAt.Format(time.DateOnly), int(age/(24*time.Hour)))
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
