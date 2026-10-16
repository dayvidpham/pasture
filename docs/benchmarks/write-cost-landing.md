# Write-cost integration and publication boundary

The landing preparation preserves production **20 records / 25 ms** and all
existing timeout, failure-policy, authority and integrity guarantees. No measured
parameter change is proposed. Historical benchmark sources and raw private
artifacts remain immutable; the integration source is not represented as the
exact binary used by those historical campaigns.

## Change boundary

Pasture integrates corrected, explicit opt-in profiling, experiment-tag-only
lane inputs/observers, offline campaign/analysis tools, tests and privacy-safe
measurement documentation. Normal builds do not accept experiment budgets.
The lane queue timing correction changes observability, not batch admission or
commit decisions. The normal command path only adds disabled profiling hooks.
No production hook/permission policy or generated harness payload is changed.

Provenance owns the production optimization: exact replay reuses an existing
verified proof only for the same transaction state and falls back on uncertainty.
Canonical identity/authority/reconstruction and new-write gates remain intact.
The SQL recorder is private, build-tagged, opt-in instrumentation, not a public
API or a default behavior. General artifact safety must be reviewed on the final
upstream integration, separately from the accepted restricted benchmark usage.

## Unpublished dependency verification

Publication prerequisite was satisfied on2026-10-16: Provenance v0.10.1 tags the
actual merge commit `f0572d1a6759f3bd160f85ef487a5a33a4069510`, incorporating the
reviewed `126f06f` tree. The candidate now adopts the real module and computed
vendor closure, without a local replacement. All final candidate gates must use
`GOWORK=off`; the workspace recipe below remains only a record of prepublication
verification and is not substituted for published-pin gates.

Before publication, consumer checks must explicitly use a private external
workspace pointing to both final integration trees. Do not commit a `replace`,
workspace, downloaded binary or artificial module checksum:

```sh
GATE_DIR=$(mktemp -d /tmp/opencode/write-cost-integration-XXXXXX)
printf 'go 1.26.0\nuse (\n"%s"\n"%s"\n)\n' "$PASTURE_TREE" "$PROVENANCE_TREE" > "$GATE_DIR/go.work"
export GOWORK="$GATE_DIR/go.work"
CGO_ENABLED=1 go test -race -shuffle=on -fullpath -timeout=30m ./...
CGO_ENABLED=1 go test -race -shuffle=on -fullpath -timeout=30m -tags=pasture_experiment ./...
CGO_ENABLED=1 go test -race -shuffle=on -fullpath -timeout=30m -tags=recovery ./internal/engine/...
go vet ./...
go vet -tags=pasture_experiment ./...
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go build -tags=pasture_experiment ./...
```

These are **local-workspace integration checks**, never evidence that a new
published dependency pin or Nix package has passed. Full-suite failures remain
failures even if a narrowed retry passes. Record the original shuffle seed/log
and reproduce on the unchanged base without weakening tests or resource limits.

## Supervisor-owned follow-on sequence

1. Freeze and independently review the exact upstream tree on all three axes.
   If no exported API is added, propose a patch release; confirm its actual
   current version, tags and release workflow before approval. A release-prep
   commit is not a published release, tag, or permission to merge remotely.
2. After explicit supervisor authorization, publish the reviewed Provenance
   release through its real workflow. Wait until the exact module version is
   available; do not invent a tag/version/checksum to unblock downstream tests.
3. In a separate Pasture dependency adoption commit, run
   `GOWORK=off go get github.com/dayvidpham/provenance@"$PUBLISHED_VERSION"`,
   tidy/verify the module graph and record actual resulting `go.mod`/`go.sum`.
   Update the approved-dependency description and Nix `vendorHash` from the
   actual new vendored closure, not from an earlier workspace build or guess.
4. Run the full default/tagged/recovery race suites, vet, CGO0 builds, module
   verification, formatting, Python checks and two-pass generation drift checks
   with **`GOWORK=off`**. Run Nix evaluation and build the actual updated package
   and relevant checks. Review the complete dependency adoption diff and tested
   tree. Nix evaluation before that point does not validate the unpublished patch.
5. Only after final review/approval may the supervisor push, merge/tag/release,
   deploy/switch settings, or close the tracked task. Worker preparation performs
   none of those actions. Preserve benchmark limitations: copied serial create
   improved with equal work; copied C18 create still committed only28/72 in the
   matched optimization run; hook preparation remains costly; burst timers were
   bypassed; transaction scope is not whole-turn or physical-lock duration; peer
   tails remain sparse. No single optimum or production SLA is established.
