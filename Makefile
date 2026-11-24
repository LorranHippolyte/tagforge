BINARY := tagforge
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/LorranHippolyte/tagforge/internal/version.Version=$(VERSION) \
	-X github.com/LorranHippolyte/tagforge/internal/version.Commit=$(COMMIT) \
	-X github.com/LorranHippolyte/tagforge/internal/version.Date=$(DATE)

.PHONY: build test vet fmt check clean

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) ./cmd/tagforge

test:
	go test -race -cover ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	$(MAKE) vet
	$(MAKE) test
	$(MAKE) build

clean:
	rm -rf $(BIN_DIR) dist coverage.out
