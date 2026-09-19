package pairs_test

import (
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/pairs"
)

// The Market query endpoint reports on tasks and is not a model itself.
func TestExcludedCoversTheQueryEndpoint(t *testing.T) {
	reason, ok := pairs.Excluded(pairs.MarketQuery)
	if !ok {
		t.Errorf("Excluded(%q) = false, want true", pairs.MarketQuery)
	}
	if reason == "" {
		t.Errorf("Excluded(%q) gave no reason", pairs.MarketQuery)
	}
}

func TestExcludedCoversHandPickedPages(t *testing.T) {
	// Boost Music Style returns its result inline, so there is no task to
	// record in the ledger.
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
