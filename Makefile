# --match 'v*' keeps an sdk/vX.Y.Z tag, which is usually the nearest one, from
# becoming the CLI's version.
VERSION ?= $(shell git describe --tags --always --dirty --match 'v*' 2>/dev/null || echo dev)

.PHONY: build clean test

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
