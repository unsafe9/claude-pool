package pool

import "fmt"

// ReleaseRepo is the GitHub <owner>/<repo> that hosts claude-pool releases.
const ReleaseRepo = "unsafe9/claude-pool"

// ReleaseAssetName returns the release asset filename for a platform:
// "claude-pool-<os>-<arch>", plus ".exe" when goos is "windows".
// This naming is the contract shared by goreleaser, the self-updater, and
// the install scripts — change all of them together or not at all.
func ReleaseAssetName(goos, goarch string) string {
	name := fmt.Sprintf("claude-pool-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// ReleaseAssetURL returns the download URL for the asset of release tag
// (tag includes the leading "v", e.g. "v0.2.0"):
// https://github.com/<ReleaseRepo>/releases/download/<tag>/<asset>
func ReleaseAssetURL(tag, goos, goarch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		ReleaseRepo, tag, ReleaseAssetName(goos, goarch))
}
