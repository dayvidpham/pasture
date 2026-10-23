.PHONY: build test test-pr test-recovery test-race test-race-correctness test-performance test-race-ci lint fmt clean release-local release-all generate check-operational-tracker

VERSION ?= devel

# Binaries produced by make build
#
# pasture-migrate-crash is a TEST-ONLY binary used by the crash-mid-migration
# recovery test. It performs the v2→v3 audit migration but os.Exit(137)s after
# staging audit_schema_meta(version=3) and BEFORE tx.Commit, so WAL recovery on
# the next open is exercised. Built by the standard target so the test can spawn
# it via os/exec.Cmd without a build-time toggle.
BINS := bin/pastured bin/pasture-release bin/pasture bin/pasture-migrate-crash

all: build

# --------------------------------------------------------------------------
# Generate
# --------------------------------------------------------------------------

generate:
	go generate ./internal/lifecycle/ingress/internal/hostcontract
	go generate ./internal/lifecycle/activation/...
	go generate ./internal/lifecycle/definitions
	go generate ./internal/codegen/...
	go generate ./internal/target/opencode/...
	go generate ./internal/target/claudecode/...
	go generate ./internal/target/codex/...
	go generate ./internal/lifecycle/metamodel/...
	go run ./cmd/pasture-specgen -out internal/sessionapi/openapi/pasture-local-api-0.1.0.json
	$(MAKE) check-operational-tracker

# Unconditional read-only sweep; also runs through Codegen Drift's make generate
# and the race suite's shipped-tree test. No environment bypass or baseline.
check-operational-tracker:
	go run ./tools/operational-check

# --------------------------------------------------------------------------
# Build
# --------------------------------------------------------------------------

build: $(BINS)

bin/pastured:
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pastured ./cmd/pastured

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

# Test-only binary; see BINS comment above for rationale.
bin/pasture-migrate-crash:
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pasture-migrate-crash ./cmd/pasture-migrate-crash

# --------------------------------------------------------------------------
# Test
# --------------------------------------------------------------------------

# Local non-race path. The two measured daemon/load rows are skipped because
# Controlled performance owns them; that also keeps a developer's live store
# out of the unit run.
test:
	GOWORK=off CGO_ENABLED=0 go test -count=1 -timeout 30m -p=4 -parallel=4 \
		-skip '^(TestDisposableStoreLoadHarnessIsTheRegressionTripwire|TestEighteenConcurrentHooksThroughTheDaemonLaneIsTheAcceptanceRow)$$' \
		./...

# Pull-request fast path. In addition to the measured daemon/load rows, the
# two broad executable-discovery tables run only in the main-push race suite;
# their focused discovery cases remain here. This keeps PR feedback under six
# minutes without removing the tables from the release authority.
test-pr:
	GOWORK=off CGO_ENABLED=0 go test -count=1 -timeout 30m -p=4 -parallel=4 \
		-skip '^(TestDisposableStoreLoadHarnessIsTheRegressionTripwire|TestEighteenConcurrentHooksThroughTheDaemonLaneIsTheAcceptanceRow|TestClaudeDefaultDiscoverySelectsOneExecutable|TestNativeVersionSelectionAndRefusal)$$' \
		./...

test-recovery:
	GOWORK=off CGO_ENABLED=0 go test -count=1 -timeout 30m -tags recovery ./internal/engine/...

test-race:
	$(MAKE) test-race-correctness
	$(MAKE) test-performance

test-race-correctness:
	GOWORK=off PASTURE_TEST_PHASE=race-correctness CGO_ENABLED=1 go test -race -count=1 -timeout 30m ./...

# Run only after the race wave has exited, with no independent heavy builders.
# Empty GITHUB_ACTIONS deliberately retains unscaled reference-host thresholds.
test-performance:
	GOWORK=off GITHUB_ACTIONS= PASTURE_TEST_PHASE=performance CGO_ENABLED=0 go test -count=1 -timeout 30m -v ./cmd/pastured -run '^(TestEighteenConcurrentHooksThroughTheDaemonLaneIsTheAcceptanceRow|TestDisposableStoreLoadHarnessIsTheRegressionTripwire)$$'

test-race-ci:
	CGO_ENABLED=1 go test -race -timeout 30m ./internal/engine/ ./internal/handlers/ ./internal/provadapter/ ./internal/tasks/

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

# Build all 4 binaries for the current platform (stripped, no CGO).
# Outputs: dist/<binary>-<goos>-<goarch>
release-local:
	@GOOS=$$(go env GOOS); \
	GOARCH=$$(go env GOARCH); \
	SUFFIX="$${GOOS}-$${GOARCH}"; \
	mkdir -p dist; \
	for cmd in pastured pasture-release pasture; do \
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
		for cmd in pastured pasture-release pasture; do \
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
