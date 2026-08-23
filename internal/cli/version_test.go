package cli

import "testing"

// The release pipeline stamps releaseVersion in with -ldflags -X, so it is
// unexported and has no other way in: the test lives inside the package.
func TestVersionPrefersTheStampedRelease(t *testing.T) {
	previous := releaseVersion
	t.Cleanup(func() { releaseVersion = previous })

	releaseVersion = "v1.2.3"
	if got := version(); got != "v1.2.3" {
		t.Errorf("version() = %q, want the stamped %q", got, "v1.2.3")
	}

	// An unstamped build still has to answer, which is what every build
	// that is not a release -- `go build`, `go install`, `mise run build`
	// -- is. It falls back to what the toolchain recorded.
	releaseVersion = ""
	if got := version(); got == "" {
		t.Error("version() is empty without a stamped release")
	}
}
