VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build clean

build:
	go build -ldflags "-X 'docsgpt-cli/cmd.Version=$(VERSION)'" -o docsgpt-cli .

clean:
	rm -f docsgpt-cli
