// Package pairs records which docs.kie.ai pages form a create/query pair.
//
// Market models need no table: one endpoint creates every task and one endpoint
// reports on it. The standard APIs predate that design and each carries its own
// record endpoint, and no page states which one belongs to which. The table
// below is therefore read off the documentation by hand; anything it does not
// cover fails the generation rather than producing a model nobody can poll.
package pairs

// MarketCreatePath is the single endpoint every Market model is created with.
// It is what distinguishes a Market page from a standard API page — the docs
// URL does not, because a few Market models are filed outside /market/.
const MarketCreatePath = "/api/v1/jobs/createTask"

// MarketQuery is the docs page of the endpoint that reports on every Market
// task.
const MarketQuery = "market/common/get-task-detail"

// queryOf maps the docs path of a create page to the docs path of the page
// documenting the endpoint that reports on the tasks it produces.
var queryOf = map[string]string{
	"4o-image-api/generate-4-o-image":         "4o-image-api/get-4-o-image-details",
	"flux-kontext-api/generate-or-edit-image": "flux-kontext-api/get-image-details",

	"runway-api/generate-ai-video":    "runway-api/get-ai-video-details",
	"runway-api/extend-ai-video":      "runway-api/get-ai-video-details",
	"runway-api/generate-aleph-video": "runway-api/get-aleph-video-details",

	"suno-api/generate-music":          "suno-api/get-music-details",
	"suno-api/extend-music":            "suno-api/get-music-details",
	"suno-api/upload-and-cover-audio":  "suno-api/get-music-details",
	"suno-api/upload-and-extend-audio": "suno-api/get-music-details",
	"suno-api/add-instrumental":        "suno-api/get-music-details",
	"suno-api/add-vocals":              "suno-api/get-music-details",
	"suno-api/generate-mashup":         "suno-api/get-music-details",
	"suno-api/replace-section":         "suno-api/get-music-details",
	"suno-api/generate-sounds":         "suno-api/get-music-details",

	"suno-api/cover-suno":         "suno-api/get-cover-suno-details",
	"suno-api/generate-lyrics":    "suno-api/get-lyrics-details",
	"suno-api/convert-to-wav":     "suno-api/get-wav-details",
	"suno-api/separate-vocals":    "suno-api/get-vocal-separation-details",
	"suno-api/generate-midi":      "suno-api/get-midi-details",
	"suno-api/create-music-video": "suno-api/get-music-video-details",

	"suno-api/suno-voice-validate":   "suno-api/suno-voice-validate-info",
	"suno-api/suno-voice-regenerate": "suno-api/suno-voice-validate-info",
	"suno-api/suno-voice-generate":   "suno-api/suno-voice-record-info",

	"veo3-api/generate-veo-3-video": "veo3-api/get-veo-3-video-details",
	"veo3-api/extend-video":         "veo3-api/get-veo-3-video-details",
	// Upgrading to 4K is a task of its own: it answers with a new task id and
	// its own callback, and the response documents Get Video Details as the way
	// to follow it.
	"veo3-api/get-veo-3-4k-video": "veo3-api/get-veo-3-video-details",
}

// notModels are the pages that return a task id yet cannot be run as a model.
// Query endpoints are added at init, so only the odd ones are listed here.
var notModels = map[string]string{
	"suno-api/boost-music-style": "returns the boosted style in the response itself, " +
		"and kie.ai documents no endpoint to query its task id with",
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
	for _, query := range queryOf {
		notModels[query] = "reports on tasks a create endpoint produced"
	}
	notModels[MarketQuery] = "reports on every Market task"
}

// Query returns the docs page of the query endpoint paired with a create page.
func Query(createPath string) (queryPath string, ok bool) {
	queryPath, ok = queryOf[createPath]
	return queryPath, ok
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
