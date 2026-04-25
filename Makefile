.PHONY: build test lint fmt clean release-local release-all generate

VERSION ?= dev

# Binaries produced by make build
BINS := bin/pastured bin/pasture-msg bin/pasture-release bin/pasture

all: build

# --------------------------------------------------------------------------
# Generate
# --------------------------------------------------------------------------

generate:
	go generate ./internal/codegen/...

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

bin/pasture:
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pasture ./cmd/pasture

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
# Release
# --------------------------------------------------------------------------

# Build all 3 binaries for the current platform (stripped, no CGO).
# Outputs: dist/<binary>-<goos>-<goarch>
release-local:
	@GOOS=$$(go env GOOS); \
	GOARCH=$$(go env GOARCH); \
	SUFFIX="$${GOOS}-$${GOARCH}"; \
	mkdir -p dist; \
	for cmd in pastured pasture-msg pasture-release; do \
		echo "Building $${cmd}-$${SUFFIX}..."; \
		CGO_ENABLED=0 go build \
			-ldflags "-s -w -X main.version=$(VERSION)" \
			-o "dist/$${cmd}-$${SUFFIX}" \
			./cmd/$${cmd}; \
	done; \
	echo "Binaries written to dist/"

# Cross-compile all 3 binaries for all 4 supported release platforms.
# Outputs: dist/<binary>-<platform>
release-all:
	@mkdir -p dist; \
	for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		GOOS=$$(echo $$target | cut -d/ -f1); \
		GOARCH=$$(echo $$target | cut -d/ -f2); \
		SUFFIX="$${GOOS}-$${GOARCH}"; \
		for cmd in pastured pasture-msg pasture-release pasture; do \
			echo "Building $${cmd}-$${SUFFIX}..."; \
			CGO_ENABLED=0 GOOS=$${GOOS} GOARCH=$${GOARCH} go build \
				-ldflags "-s -w -X main.version=$(VERSION)" \
				-o "dist/$${cmd}-$${SUFFIX}" \
				./cmd/$${cmd}; \
		done; \
	done; \
	echo "All binaries written to dist/"

# --------------------------------------------------------------------------
# Clean
# --------------------------------------------------------------------------

clean:
	rm -rf bin/ dist/
