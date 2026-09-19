// Package pairs records which docs.kie.ai pages form a create/query pair.
//
// Every model is a Market model: one endpoint creates every task and one
// endpoint reports on it, so no pair needs a table. What is left to read off
// the documentation by hand is which pages look like a model and are not one.
package pairs

// MarketCreatePath is the single endpoint every model is created with. It is
// what tells a model's page apart — the docs URL does not, because Suno,
// Veo3.1, Runway, 4o Image and Flux Kontext are filed outside /market/.
const MarketCreatePath = "/api/v1/jobs/createTask"

// MarketQuery is the docs page of the endpoint that reports on every Market
// task.
const MarketQuery = "market/common/get-task-detail"

// notModels are the pages that return a task id yet cannot be run as a model.
// The query endpoint is added at init, so only the odd ones are listed here.
var notModels = map[string]string{
	"suno-api/boost-music-style": "returns the boosted style in the response itself, " +
		"so there is no task left to follow",
}

// claimsModelID names pages that state a model id another page owns. Two pages
// with one id is not a catalog anyone can index by id, so the page listed here
// is dropped — but only while it still states that id, so a correction on
// kie.ai's side brings the model back without anyone editing this table.
//
// Two kinds end up here:
//
//   - Chinese pages that escaped the /cn/ filter, either through opaque ids or
//     through a /cnmarket/ typo, and repeat an English page.
//   - Pages whose model enum was copied from a neighbour. Their own prose and
//     examples name a different model, so the enum cannot be trusted and no id
//     can be derived from the page at all.
var claimsModelID = map[string]string{
	"38308980e0":                           "happyhorse-1-1/image-to-video",
	"38309290e0":                           "happyhorse-1-1/text-to-video",
	"38309489e0":                           "happyhorse-1-1/reference-to-video",
	"41313512e0":                           "seedream/5-pro-layer-decomposition",
	"cnmarket/pixverse/reference-to-video": "pixverse-v6/reference-to-video",

	"market/qwen2/text-to-image":                "qwen2/image-edit",
	"market/kling/v25-turbo-image-to-video-pro": "kling/v2-1-master-image-to-video",
}

func init() {
	notModels[MarketQuery] = "reports on every Market task"
}

// Excluded reports why a page that submits nothing the CLI can track is not a
// model, for pages that would otherwise look like one.
func Excluded(docsPath string) (reason string, ok bool) {
	reason, ok = notModels[docsPath]
	return reason, ok
}

// ClaimsForeignID reports whether a page states a model id that belongs to
// another page, and so has to be left out of the catalog.
func ClaimsForeignID(docsPath, modelID string) bool {
	claimed, ok := claimsModelID[docsPath]
	return ok && claimed == modelID
}
