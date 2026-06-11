package pool

import "testing"

func TestReleaseAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"darwin", "arm64", "claude-pool-darwin-arm64"},
		{"darwin", "amd64", "claude-pool-darwin-amd64"},
		{"linux", "amd64", "claude-pool-linux-amd64"},
		{"linux", "arm64", "claude-pool-linux-arm64"},
		{"windows", "amd64", "claude-pool-windows-amd64.exe"},
		{"windows", "arm64", "claude-pool-windows-arm64.exe"},
	}
	for _, c := range cases {
		got := ReleaseAssetName(c.goos, c.goarch)
		if got != c.want {
			t.Errorf("ReleaseAssetName(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestReleaseAssetURL(t *testing.T) {
	got := ReleaseAssetURL("v0.2.0", "windows", "amd64")
	want := "https://github.com/unsafe9/claude-pool/releases/download/v0.2.0/claude-pool-windows-amd64.exe"
	if got != want {
		t.Errorf("ReleaseAssetURL = %q, want %q", got, want)
	}
}
