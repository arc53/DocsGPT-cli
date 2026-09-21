# --match 'v*' keeps an sdk/vX.Y.Z tag, which is usually the nearest one, from
# becoming the CLI's version.
VERSION ?= $(shell git describe --tags --always --dirty --match 'v*' 2>/dev/null || echo dev)
RELEASE_BRANCH ?= main
# The remote that releases are cut on. It is `origin` in a direct clone, but a
# fork-based clone has the canonical repository under another name:
#   make release VERSION=v1.7.0 RELEASE_REMOTE=upstream
RELEASE_REMOTE ?= origin

.PHONY: build clean test release

build:
	go build -ldflags "-X 'github.com/arc53/DocsGPT-cli/cmd.Version=$(VERSION)'" -o docsgpt-cli ./cmd/docsgpt-cli

# sdk/ is a separate module: ./... from the root never reaches it, and
# ./sdk/... only resolves in workspace mode. Running it from its own directory
# works with or without go.work.
test:
	go test ./...
	go -C sdk test ./...

clean:
	rm -f docsgpt-cli

# Cut a release: make release VERSION=v1.6.0
#
# Pushing the tag is the only manual step in the release. Everything after it
# (archives, checksums, the installers, the Homebrew cask) is GoReleaser's job,
# so the checks below all run before the tag exists anywhere but locally.
release:
	@test "$(origin VERSION)" = "command line" \
		|| { echo "set the version explicitly: make release VERSION=v1.6.0"; exit 1; }
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' \
		|| { echo "VERSION must look like v1.6.0, got '$(VERSION)'"; exit 1; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = "$(RELEASE_BRANCH)" \
		|| { echo "releases are cut from $(RELEASE_BRANCH), not $$(git rev-parse --abbrev-ref HEAD)"; exit 1; }
	@test -z "$$(git status --porcelain)" \
		|| { echo "working tree is dirty; commit or stash first"; exit 1; }
	@git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null \
		&& { echo "tag $(VERSION) already exists locally"; exit 1; } || true
	@git fetch --quiet "$(RELEASE_REMOTE)" "$(RELEASE_BRANCH)"
	@remote_tag="$$(git ls-remote --tags "$(RELEASE_REMOTE)" "refs/tags/$(VERSION)")" \
		|| { echo "could not reach $(RELEASE_REMOTE) to check for tag $(VERSION)"; exit 1; }; \
		test -z "$$remote_tag" || { echo "tag $(VERSION) already exists on $(RELEASE_REMOTE)"; exit 1; }
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse $(RELEASE_REMOTE)/$(RELEASE_BRANCH))" \
		|| { echo "HEAD is not $(RELEASE_REMOTE)/$(RELEASE_BRANCH); push or pull first"; exit 1; }
	# Build and test the way the release will: against the sdk version pinned in
	# go.mod, not the working tree. go.work otherwise hides a forgotten pin bump
	# until release.yml fails — by which point the tag is pushed, and once the
	# proxy has served that version it is immutable.
	GOWORK=off go build ./...
	GOWORK=off go test ./...
	go -C sdk test ./...
	git tag -a "$(VERSION)" -m "$(VERSION)"
	git push "$(RELEASE_REMOTE)" "$(VERSION)"
	@echo
	@echo "Tagged and pushed $(VERSION). Watch the release run:"
	@echo "  https://github.com/arc53/DocsGPT-cli/actions/workflows/release.yml"
