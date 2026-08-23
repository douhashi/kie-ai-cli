// Package gen builds the model catalog from docs.kie.ai.
//
// It reads llms.txt for the page list and the axes to filter models by, reads
// each page's embedded OpenAPI document for the endpoints and the input schema,
// and refuses to produce a catalog if any page fails to fit. A quietly dropped
// model is worse than no catalog: nobody notices until the model is needed.
package gen

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/llms"
	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/openapi"
	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/pairs"
)

// errForeignID marks a page dropped because it states another page's model id.
var errForeignID = errors.New("page states a model id that belongs to another page")

// Fetcher retrieves documentation pages by URL. It is the generator's only
// contact with the network.
type Fetcher interface {
	Fetch(ctx context.Context, urls []string) (map[string]string, error)
}

// Build returns the catalog described by llms.txt and the pages it lists.
//
// Every page that fails is reported, not just the first: a kie.ai change
// usually breaks a family of pages at once, and fixing them one run at a time
// would take a crawl per page.
func Build(ctx context.Context, llmsText string, fetcher Fetcher) (*catalog.Catalog, error) {
	entries, err := llms.ParseAPIDocs(llmsText)
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(entries))
	for _, entry := range entries {
		urls = append(urls, entry.URL)
	}
	pageBodies, err := fetcher.Fetch(ctx, urls)
	if err != nil {
		return nil, fmt.Errorf("fetch pages: %w", err)
	}

	operations := make(map[string]*openapi.Operation, len(entries))
	var problems []error
	for _, entry := range entries {
		operation, err := openapi.Parse(pageBodies[entry.URL])
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", entry.URL, err))
			continue
		}
		operations[entry.DocsPath()] = operation
	}

	models := make([]catalog.Model, 0, len(entries))
	for _, entry := range entries {
		operation, ok := operations[entry.DocsPath()]
		if !ok {
			continue // already reported as unparsable
		}
		if !operation.ReturnsTaskID {
			continue // synchronous: nothing to record in the ledger
		}
		if _, excluded := pairs.Excluded(entry.DocsPath()); excluded {
			continue
		}
		model, err := buildModel(entry, operation, operations)
		if errors.Is(err, errForeignID) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", entry.URL, err))
			continue
		}
		models = append(models, model)
	}

	slices.SortFunc(models, func(a, b catalog.Model) int { return strings.Compare(a.ID, b.ID) })
	problems = append(problems, duplicateIDs(models)...)
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return &catalog.Catalog{SchemaVersion: catalog.SchemaVersion, Models: models}, nil
}

func buildModel(entry llms.Entry, operation *openapi.Operation, operations map[string]*openapi.Operation) (catalog.Model, error) {
	category, vendor, err := llms.Taxonomy(entry.Breadcrumb)
	if err != nil {
		return catalog.Model{}, err
	}

	id, input, queryPath, err := creation(entry, operation)
	if err != nil {
		return catalog.Model{}, err
	}
	// The schema is corrected once the model is named, because what a page
	// says about a property is only worth as much as the request that was
	// made against that model's endpoint; see required.go.
	if err := correctRequired(id, input); err != nil {
		return catalog.Model{}, err
	}
	query, err := queryEndpoint(queryPath, operations)
	if err != nil {
		return catalog.Model{}, err
	}
	if operation.Summary == "" {
		return catalog.Model{}, fmt.Errorf("operation has no summary to name the model with")
	}

	style := catalog.StyleDirect
	marketModel := ""
	if operation.Path == pairs.MarketCreatePath {
		style, marketModel = catalog.StyleMarket, id
	}
	// llms.txt copies the first line of the page, which is often a heading, so
	// it only helps when it happens to be prose.
	description := operation.Description
	if description == "" {
		description = openapi.ProseLine(entry.Description)
	}

	return catalog.Model{
		ID:          id,
		Name:        operation.Summary,
		Description: description,
		Category:    category,
		Vendor:      vendor,
		DocsURL:     entry.URL,
		Create: catalog.Create{
			Method: operation.Method,
			Path:   operation.Path,
			Style:  style,
			Model:  marketModel,
		},
		Query: query,
		Input: input,
	}, nil
}

// creation derives the model id, the schema of what the user supplies, and the
// page documenting how to follow the task.
//
// Market models are told apart by the endpoint they post to, not by their docs
// URL: a handful of them are filed outside /market/.
func creation(entry llms.Entry, operation *openapi.Operation) (id string, input map[string]any, queryPath string, err error) {
	if operation.Path == pairs.MarketCreatePath {
		modelProperty, err := operation.RequestProperty("model")
		if err != nil {
			return "", nil, "", err
		}
		id, err = openapi.SingleEnumString(modelProperty)
		if err != nil {
			return "", nil, "", fmt.Errorf("model property: %w", err)
		}
		if pairs.ClaimsForeignID(entry.DocsPath(), id) {
			return "", nil, "", errForeignID
		}
		// Everything outside "input" is envelope the CLI fills in itself.
		input, err = operation.RequestProperty("input")
		if err != nil {
			return "", nil, "", err
		}
		return id, input, pairs.MarketQuery, nil
	}

	id = entry.DocsPath()
	queryPath, ok := pairs.Query(id)
	if !ok {
		return "", nil, "", fmt.Errorf(
			"page returns a task id but no query endpoint is paired with it; add %s to internal/catalog/gen/pairs", id)
	}
	if operation.RequestSchema == nil {
		return "", nil, "", fmt.Errorf("create endpoint has no application/json request body")
	}
	return id, operation.RequestSchema, queryPath, nil
}

func queryEndpoint(docsPath string, operations map[string]*openapi.Operation) (catalog.Query, error) {
	operation, ok := operations[docsPath]
	if !ok {
		return catalog.Query{}, fmt.Errorf("query endpoint page %s is missing from llms.txt or failed to parse", docsPath)
	}
	if len(operation.QueryParams) != 1 {
		return catalog.Query{}, fmt.Errorf("query endpoint %s takes %d query parameters, want exactly 1: %v",
			docsPath, len(operation.QueryParams), operation.QueryParams)
	}
	return catalog.Query{
		Method: operation.Method,
		Path:   operation.Path,
		Param:  operation.QueryParams[0],
	}, nil
}

// duplicateIDs reports ids claimed by more than one page. models must be
// sorted.
func duplicateIDs(models []catalog.Model) []error {
	var problems []error
	for i := 1; i < len(models); i++ {
		if models[i].ID != models[i-1].ID {
			continue
		}
		problems = append(problems, fmt.Errorf("model id %s is claimed by both %s and %s",
			models[i].ID, models[i-1].DocsURL, models[i].DocsURL))
	}
	return problems
}
