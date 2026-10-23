# pasture web shell

The local session-epoch console. It uses `@peasant-labs/fairtrade/ui` for the
shell, tokens, status marks, and table, and reads the loopback Pasture API.

## run

Start the API on a fixed loopback port so Vite can proxy it:

```bash
pasture serve --db ~/.local/share/pasture/pasture.db --listen 127.0.0.1:37373
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

Commit the generated file with the contract change. `pnpm build` runs the
TypeScript check and the production Vite build; `web/dist` is not committed.

## scope

This shell shows session and epoch inventory, activity, bindings, and the
snapshot stream. The transcript rail is a placeholder until the Peasant join
and `adaptTranscript` / `TranscriptViewer` integration lands.
