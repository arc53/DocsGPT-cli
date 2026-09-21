package cmd

import (
	"runtime/debug"
	"testing"
)

func TestVersionFromBuildInfo(t *testing.T) {
	const sum = "h1:wz0k2xUM9Z45fCKjfSZcoY4lt/Jym6B5H6z1B1g4Xi4="

	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "go install at a release",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.9.10", Sum: sum}},
			want: "v1.9.10",
		},
		{
			// Go >= 1.24 stamps Main.Version from VCS tags, so a plain
			// `go build` on a clean checkout of a tag looks just like an
			// installed release. Trusting it would let auto-update replace a
			// developer's own build with the latest release.
			name: "go build on a clean checkout of a tag",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v1.5.1"},
				Settings: []debug.BuildSetting{
					{Key: "vcs", Value: "git"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: "",
		},
		{
			name: "go build off-tag (pseudo-version)",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v1.5.2-0.20260921125649-8818795c99fa"},
				Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}},
			},
			want: "",
		},
		{
			name: "go install at a prerelease is not a release build",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.6.0-rc1", Sum: sum}},
			want: "",
		},
		{
			name: "no version recorded",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionFromBuildInfo(tc.info); got != tc.want {
				t.Errorf("versionFromBuildInfo() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Under `go test` the binary is built from the working tree, so the build info
// must never override a stamped version.
func TestResolveVersionDeclinesForTestBinary(t *testing.T) {
	if got := resolveVersion(); got != "" {
		t.Errorf("resolveVersion() = %q for a test binary, want \"\"", got)
	}
}
