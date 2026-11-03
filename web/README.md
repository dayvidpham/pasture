# pasture web shell

The local session-epoch console. It uses `@peasant-labs/fairtrade/ui` for the
shell, tokens, status marks, table, and transcript viewer, and reads the
loopback Pasture API.

## run

The production path is `pasture serve`: the console is compiled into
`internal/webui/assets`, embedded in the `pasture` binary, and served at `/`
from the same loopback origin as the API. No Node or Vite process is needed at
runtime.

```bash
cd web
pnpm install --frozen-lockfile
pnpm build                 # writes ../internal/webui/assets (committed)
cd ..
make build                 # or: go build -o bin/pasture ./cmd/pasture
./bin/pasture serve --db ~/.local/share/pasture/pasture.db --listen 127.0.0.1:37373
```

Open `http://127.0.0.1:37373/`. The API remains at `/api/v1/*` and
`/openapi.json`. `pnpm build` writes the committed embedded bundle; a plain
`go build` and the release/Nix builds consume that committed tree and never run
Node. `make web-verify-bundle` rebuilds it and fails if the committed bundle is
stale.

The transcript rail shells out to the local `peasant` CLI, so Peasant must be
installed and on the `PATH` of the `pasture serve` process. The browser never
talks to Peasant directly.

Peasant project enrichment is off by default and no Peasant state is opened
while it is off. Turn it on with the toggle in the console's "peasant projects"
rail; it reopens the configured source. With no `--peasant-db`/`--peasant-url`
flag that source is the conventional Peasant database
(`$XDG_DATA_HOME/peasant/peasant.db`, otherwise
`~/.local/share/peasant/peasant.db`), opened lazily. Turning it off closes that
source and the next session read omits project fields. Opening a WAL-mode
Peasant database read-only may leave `peasant.db-shm` and `peasant.db-wal`
beside the database; Pasture never writes to or migrates the main Peasant
database. For a different source,
start serve with `--peasant-db` or `--peasant-url`; that starts enabled, and the
toggle can still turn it off and on. The switch lasts for this serve process:
restarting returns to the startup default (off without flags, on with an
explicit flag). A missing or incompatible source returns an actionable error
and leaves enrichment off without affecting the session API.

## reading liveness, actor, and execution

These are three separate facts; the console never merges them.

- **State and basis.** Liveness is prompt-based, not binding-based. A prompt
  younger than `--active-window` (default 30m) is `active`; a prompt older than
  `--idle-window` (default 24h) is `ended` with basis `inactivity` and **no**
  end marker; a prompt between them is `idle`. A session with no prompt
  evidence is `unknown` even when it has an open binding, and an unbound
  session with a recent prompt is `active`. `lastSeen`/`lastEvent` are broad
  activity, not prompt recency. An unsuperseded host end or an applicable user
  unbind is `ended` with basis `explicit-end`. `serve` refuses a zero,
  negative, equal, or inverted window pair.
- **Actor.** The rail shows the stored session claim and classifies it as the
  reserved `pasture/default-llm` actor (`generic-llm`, attribution only) or
  `explicit`. A claim is first-claim-wins: a later explicit actor never
  rewrites it. To enroll a real actor, start a new harness session with
  `PASTURE_ACTOR_ID` set; do not expect an existing generic claim to change.
  No claim means no actor is shown.
- **Execution.** The summary is the newest `primary` or `selection`
  observation by journal id, as one coherent record. Provider, model, variant,
  native role, and protocol role each carry their own status (`observed`,
  `unavailable`, `invalid`, `conflict`), evidence source, and (for roles)
  vocabulary. Native agent names are never translated into Pasture roles. A
  conflict shows its bounded source-tagged candidates instead of a winner, and
  an absent field is stated, not filled from an older observation. Optional
  per-invocation declarations are `PASTURE_EXECUTION_PROVIDER`,
  `PASTURE_EXECUTION_MODEL`, and `PASTURE_EXECUTION_ROLE`; host evidence wins,
  and a concrete registered actor's metadata is an additional labeled source.
- **History.** The execution history is a snapshot-pinned, newest-first page
  that includes auxiliary and child observations; it never replaces the
  summary. `load more` uses the opaque cursor, so a live replay committed after
  the first page does not move its boundaries.
- **Resume and unbind.** A genuine same-session resume supersedes a host end
  and re-evaluates liveness from the unchanged actual prompt (it does not
  refresh `lastPromptAt`). A user `unbind` is an explicit mapping/end decision
  until a later deliberate bind; resume does not undo it.

Historical bindings and activity are refreshed from the lifecycle journal on reads.
To force a full rebuild before serving, pass `--backfill`:

```bash
./bin/pasture serve --db ~/.local/share/pasture/pasture.db --listen 127.0.0.1:37373 --backfill
```

For UI development, run the API on a fixed loopback port and use the Vite dev
server. `pnpm dev` is only for UI work: it proxies `/api` and `/openapi.json` to
the API.

```bash
./bin/pasture serve --db ~/.local/share/pasture/pasture.db --listen 127.0.0.1:37373
cd web && pnpm dev
```

Open `http://127.0.0.1:5180`. Override the proxy target when the API is on a
different port:

```bash
PASTURE_API=http://127.0.0.1:46199 pnpm dev
```

To serve an already-built external bundle without rebuilding the Go binary, pass
`--web-dir` to `serve` (a development override). The directory must be a trusted
Vite build tree (it contains `index.html` at its root) and must not contain
symlinks: a symlink could otherwise serve a file outside the directory.

```bash
./bin/pasture serve --web-dir /path/to/built/console --listen 127.0.0.1:37373
```

The API is loopback-only. Do not bind `pasture serve` to a non-loopback address.

## contract

`src/generated/api.ts` is generated from the canonical OpenAPI artifact. After
changing the Go contract or the committed document:

```bash
pnpm generate:api
```

Commit the generated file with the contract change. `make web-verify-generated`
regenerates the TypeScript to a temporary path and diffs it, so the Go JSON and
the committed TypeScript cannot drift. `pnpm build` runs the TypeScript check
and the production Vite build, which writes `../internal/webui/assets`; `web/dist`
is not used.

`pnpm test` runs the Vitest boundary suite. It covers the shared-session-schema
parse and `adaptTranscript` hand-off, plus the actionable copy for 404, 409,
503, 504, and generic failures. The suite is hermetic: no network and no
Peasant store.

## scope

This shell shows session and epoch inventory, activity, explicit and inferred
binding provenance (source, confidence, and every recorded interval), the
snapshot stream, and the durable Peasant transcript rendered by fairtrade's
`TranscriptViewer` with read-only capabilities. Transcript errors distinguish
a session identity the shared contract cannot express (400), no transcript
(404), an incomplete/incompatible Peasant refusal (409), Peasant missing from
`PATH` (503), and a bounded export timeout (504).

After upgrading an existing store, run a normal pasture command or start `pastured` or `pasture serve` before enabling host hooks, so the default actor is initialized before the first hook.
