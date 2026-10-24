.PHONY: build test test-pr test-recovery test-race test-race-correctness test-performance test-race-ci lint fmt clean release-local release-all generate web-install web-generate web-build web-test web-verify-generated web-verify-bundle check-operational-tracker FORCE

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
	go run ./cmd/pasture-specgen -out internal/sessionapi/openapi/pasture-local-api-0.5.0.json
	$(MAKE) check-operational-tracker

web-install:
	cd web && pnpm install

web-generate: web-install
	cd web && pnpm generate:api

web-build: web-install
	cd web && pnpm build

web-test: web-install
	cd web && pnpm test

# Regenerate the TypeScript bindings to a temporary path and diff them with the
# committed file. This is the web half of the contract freshness gate: the Go
# JSON source cannot drift from the committed OpenAPI artifact without failing
# the Go freshness test, and the artifact cannot drift from the TypeScript
# without failing this target.
web-verify-generated: web-install
	cd web && tmp=$$(mktemp) && trap 'rm -f "$$tmp"' EXIT && \
		pnpm exec openapi-typescript ../internal/sessionapi/openapi/pasture-local-api-0.5.0.json -o "$$tmp" >/dev/null && \
		if ! cmp -s "$$tmp" src/generated/api.ts; then \
			echo "web/src/generated/api.ts is stale; run 'make web-generate' and commit the result."; \
			exit 1; \
		fi

# Rebuild the production console and fail when the committed embedded bundle is
# stale. This is the only build/release gate that needs Node: `make build`,
# Nix, and the release jobs consume the committed internal/webui/assets tree
# directly and never run pnpm. It installs frozen dependencies itself so it can
# run standalone in CI.
web-verify-bundle:
	cd web && pnpm install --frozen-lockfile && pnpm build
	@stale=$$(git status --porcelain -- internal/webui/assets); \
	if [ -n "$$stale" ]; then \
		echo "internal/webui/assets is stale: it does not match 'cd web && pnpm build'."; \
		echo "Rebuild and commit the bundle before landing:"; \
		echo "    cd web && pnpm install --frozen-lockfile && pnpm build"; \
		echo "    git add internal/webui/assets"; \
		echo "$$stale"; \
		exit 1; \
	fi

# Unconditional read-only sweep; also runs through Codegen Drift's make generate
# and the race suite's shipped-tree test. No environment bypass or baseline.
check-operational-tracker:
	go run ./tools/operational-check

# --------------------------------------------------------------------------
# Build
# --------------------------------------------------------------------------

build: $(BINS)

# The bin/ outputs are ignored files, so Make cannot derive their Go source
# dependency graph. Force every `make build` to rebuild; otherwise a checkout
# that already has bin/pasture silently keeps stale binaries, including an old
# embedded web bundle.
FORCE:

bin/pastured: FORCE
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pastured ./cmd/pastured

bin/pasture-release: FORCE
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pasture-release ./cmd/pasture-release

bin/pasture: FORCE
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(VERSION)" \
		-o bin/pasture ./cmd/pasture

# Test-only binary; see BINS comment above for rationale.
bin/pasture-migrate-crash: FORCE
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
