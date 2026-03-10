.PHONY: build test lint fmt clean

VERSION ?= dev

# Binaries produced by make build
BINS := bin/pastured bin/pasture-msg bin/pasture-release

all: build

# --------------------------------------------------------------------------
# Build
# --------------------------------------------------------------------------

build: $(BINS)

bin/pastured:
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pastured ./cmd/pastured

bin/pasture-msg:
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pasture-msg ./cmd/pasture-msg

bin/pasture-release:
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pasture-release ./cmd/pasture-release

# --------------------------------------------------------------------------
# Test
# --------------------------------------------------------------------------

# CGO_ENABLED=0 for pure-Go build. Note: -race requires CGo; use
# CGO_ENABLED=1 make test-race when the race detector is needed.
test:
	CGO_ENABLED=0 go test ./...

test-race:
	go test -race ./...

# --------------------------------------------------------------------------
# Lint / Vet
# --------------------------------------------------------------------------

lint:
	go vet ./...

# --------------------------------------------------------------------------
# Format
# --------------------------------------------------------------------------

fmt:
	@formatted=$$(gofmt -l .); \
	if [ -n "$$formatted" ]; then \
		echo "Formatting:"; \
		echo "$$formatted"; \
		gofmt -w .; \
	fi

# --------------------------------------------------------------------------
# Clean
# --------------------------------------------------------------------------

clean:
	rm -rf bin/
