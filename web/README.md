# pasture web shell

The local session-epoch console. It uses `@peasant-labs/fairtrade/ui` for the
shell, tokens, status marks, table, and transcript viewer, and reads the
loopback Pasture API.

## run

Start the API on a fixed loopback port so Vite can proxy it:

```bash
pasture serve --db ~/.local/share/pasture/pasture.db --listen 127.0.0.1:37373
```

The transcript rail shells out to the local `peasant` CLI, so Peasant must be
installed and on the `PATH` of the `pasture serve` process. The proxy runs
`peasant export sessions` per selected session; the browser never talks to
Peasant directly.

Historical bindings and activity are projected from the lifecycle journal. On a
fresh store, pass `--backfill` once so older sessions appear in the inventory:

```bash
pasture serve --db ~/.local/share/pasture/pasture.db --listen 127.0.0.1:37373 --backfill
```

Then, from `web/`:

```bash
pnpm install
pnpm dev
```

Open `http://127.0.0.1:5180`. Vite proxies `/api` and `/openapi.json` to
`http://127.0.0.1:37373` by default. Override the target when the API is on a
different port:

```bash
PASTURE_API=http://127.0.0.1:46199 pnpm dev
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
and the production Vite build; `web/dist` is not committed.

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
