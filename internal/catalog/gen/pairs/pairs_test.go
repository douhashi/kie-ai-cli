package pairs_test

import (
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/pairs"
)

func TestQueryPairsCreateWithItsRecordEndpoint(t *testing.T) {
	tests := map[string]string{
		"suno-api/generate-music":                 "suno-api/get-music-details",
		"suno-api/convert-to-wav":                 "suno-api/get-wav-details",
		"suno-api/suno-voice-regenerate":          "suno-api/suno-voice-validate-info",
		"veo3-api/get-veo-3-4k-video":             "veo3-api/get-veo-3-video-details",
		"runway-api/extend-ai-video":              "runway-api/get-ai-video-details",
		"4o-image-api/generate-4-o-image":         "4o-image-api/get-4-o-image-details",
		"flux-kontext-api/generate-or-edit-image": "flux-kontext-api/get-image-details",
	}
	for create, want := range tests {
		got, ok := pairs.Query(create)
		if !ok {
			t.Errorf("Query(%q) not found", create)
			continue
		}
		if got != want {
			t.Errorf("Query(%q) = %q, want %q", create, got, want)
		}
	}
}

func TestQueryReportsUnknownCreatePage(t *testing.T) {
	if _, ok := pairs.Query("suno-api/some-future-endpoint"); ok {
		t.Fatal("want an unknown page to be reported, so the table cannot go stale unnoticed")
	}
}

// Every page a create is paired with is itself not a model, and neither is the
// Market query endpoint. Deriving that keeps one table instead of two.
func TestExcludedCoversQueryEndpoints(t *testing.T) {
	for _, path := range []string{
		"suno-api/get-music-details",
		"runway-api/get-aleph-video-details",
		pairs.MarketQuery,
	} {
		reason, ok := pairs.Excluded(path)
		if !ok {
			t.Errorf("Excluded(%q) = false, want true", path)
		}
		if reason == "" {
			t.Errorf("Excluded(%q) gave no reason", path)
		}
	}
}

func TestExcludedCoversHandPickedPages(t *testing.T) {
	// Boost Music Style returns its result inline and kie.ai documents no
	// endpoint to query it with, so it cannot become a ledger entry.
	if _, ok := pairs.Excluded("suno-api/boost-music-style"); !ok {
		t.Error("Excluded(suno-api/boost-music-style) = false, want true")
	}
}

func TestClaimsForeignIDDropsOnlyTheDuplicatedID(t *testing.T) {
	// kie.ai mis-links one Chinese page as /cnmarket/, escaping the /cn/ filter
	// and repeating an English model.
	if !pairs.ClaimsForeignID("cnmarket/pixverse/reference-to-video", "pixverse-v6/reference-to-video") {
		t.Error("the duplicated Chinese page was not recognised")
	}
	// This page's model enum was copied from its neighbour; its own prose names
	// kling/v2-5-turbo-image-to-video-pro.
	if !pairs.ClaimsForeignID("market/kling/v25-turbo-image-to-video-pro", "kling/v2-1-master-image-to-video") {
		t.Error("the mis-copied model enum was not recognised")
	}
	// Once kie.ai corrects the page, it stops matching and the model returns
	// without this table being touched.
	if pairs.ClaimsForeignID("market/kling/v25-turbo-image-to-video-pro", "kling/v2-5-turbo-image-to-video-pro") {
		t.Error("a corrected page must not stay excluded")
	}
	if pairs.ClaimsForeignID("market/seedream/seedream-v4-text-to-image", "bytedance/seedream-v4-text-to-image") {
		t.Error("an ordinary page must not be excluded")
	}
}

func TestExcludedLeavesCreatePagesAlone(t *testing.T) {
	if _, ok := pairs.Excluded("suno-api/generate-music"); ok {
		t.Fatal("a create page must not be excluded")
	}
}
