module github.com/arc53/DocsGPT-cli/sdk

// Deliberately older than the CLI's own go directive: this module has no
// dependencies and uses nothing recent, so there is no reason to force a
// toolchain upgrade on anyone importing it.
go 1.21
