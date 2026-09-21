package gitvcs

import "testing"

// A tag that is not a version is not a release.
//
// The showcase's release page used to open with `scaffold`, the marker its
// generator replays the story onto — dated, and captioned "No task changed in
// this release". A repository carries tags that are not releases, and a page
// headed Releases said they had shipped (DKT-65).
func TestOnlyAVersionTagIsARelease(t *testing.T) {
	yes := []string{"v1.0.0", "1.0.0", "v0.5", "v2", "v1.2.3-rc1", "v1.0.0+build7", "2026.9"}
	no := []string{"scaffold", "latest", "nightly", "release-2026-09", "v", "stable", "wip/thing"}

	for _, tag := range yes {
		if !isVersion(tag) {
			t.Errorf("%q is a version and was not taken for one", tag)
		}
	}
	for _, tag := range no {
		if isVersion(tag) {
			t.Errorf("%q is not a version and was taken for one", tag)
		}
	}
}
