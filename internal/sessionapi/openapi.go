package sessionapi

import "encoding/json"

// PastureLocalAPIVersion is the frozen document-surface version for the local
// session API. It changes only when a route or wire shape changes.
const PastureLocalAPIVersion = "0.7.0"

// OpenAPIBytes returns the canonical OpenAPI 3.1 document. The generator and
// the freshness test call this same function, so the committed artifact cannot
// drift from the Go source without a failing test.
func OpenAPIBytes() []byte {
	raw, err := json.MarshalIndent(openAPIDocument(), "", "  ")
	if err != nil {
		panic("sessionapi: OpenAPI document is not encodable: " + err.Error())
	}
	return append(raw, '\n')
}

func openAPIDocument() map[string]any {
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	roleNames := []string{"epoch", "architect", "reviewer", "supervisor", "worker"}
	jsonResponse := func(schema map[string]any) map[string]any {
		return map[string]any{
			"description": "Successful response",
			"content": map[string]any{
				"application/json": map[string]any{"schema": schema},
			},
		}
	}
	errorResponse := func(description string) map[string]any {
		return map[string]any{
			"description": description,
			"content": map[string]any{
				"application/json": map[string]any{"schema": ref("ErrorEnvelope")},
			},
		}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Pasture Local Session API",
			"version": PastureLocalAPIVersion,
			"description": "Loopback-only session and epoch API for active harness sessions mapped to " +
				"epoch bindings. It also records explicit project-to-epoch mappings: a project hash " +
				"is Peasant-owned, the mapped epoch is a Pasture task, and the relationship is " +
				"independent of per-session bindings. The transcript route proxies one durable Peasant " +
				"SessionDetailPayload; Pasture does not reshape transcript content. The transcript catalog " +
				"lists Peasant session ids that have a durable transcript and does not write the Peasant store.",
		},
		"paths": map[string]any{
			"/api/v1/epochs": map[string]any{
				"get": map[string]any{
					"operationId": "listEpochs",
					"summary":     "List projected epochs",
					"responses": map[string]any{
						"200": jsonResponse(ref("EpochPage")),
						"403": errorResponse("Non-loopback Host or Origin"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The epoch store could not be read"),
					},
				},
			},
			"/api/v1/epochs/{epochId}": map[string]any{
				"get": map[string]any{
					"operationId": "getEpoch",
					"summary":     "Get one projected epoch",
					"parameters": []any{
						pathParameter("epochId", "Epoch task id (URL-encode it when it contains slashes)"),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("EpochDetail")),
						"400": errorResponse("Invalid epoch id"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("Epoch not found"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The epoch store could not be read"),
					},
				},
			},
			"/api/v1/epochs/{epochId}/sessions": map[string]any{
				"get": map[string]any{
					"operationId": "listEpochSessions",
					"summary":     "List sessions bound to an epoch",
					"parameters": []any{
						pathParameter("epochId", "Epoch task id (URL-encode it when it contains slashes)"),
						queryParameter("state", "Filter by derived activity state", "string", []string{"active", "idle", "ended", "unknown"}),
						queryParameter("harness", "Filter by harness id", "string", nil),
						queryParameter("limit", "Page size (default 50, maximum 100)", "integer", nil),
						queryParameter("cursor", "Opaque page token from a previous response", "string", nil),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("Page")),
						"400": errorResponse("Invalid filter or page token"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("Epoch not found"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The session store could not be read"),
					},
				},
			},
			"/api/v1/inspect": map[string]any{
				"get": map[string]any{
					"operationId": "inspectSessionAPI",
					"summary":     "Snapshot epochs and sessions together",
					"responses": map[string]any{
						"200": jsonResponse(ref("InspectPayload")),
						"403": errorResponse("Non-loopback Host or Origin"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The session store could not be read"),
					},
				},
			},
			"/api/v1/settings/peasant": map[string]any{
				"post": map[string]any{
					"operationId": "updatePeasantSettings",
					"summary":     "Turn Peasant project enrichment on or off",
					"description": "Runtime toggle for the opt-in Peasant project source. With the switch off, Pasture performs no Peasant access and session project fields stay empty. Turning it on opens the configured source, or the conventional Peasant SQLite database when serve was started without --peasant-db/--peasant-url.",
					"requestBody": jsonBody(ref("PeasantSettingsRequest")),
					"responses": map[string]any{
						"200": jsonResponse(ref("PeasantIntegration")),
						"400": errorResponse("Malformed body or a missing enabled boolean"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"409": errorResponse("The runtime source or the default Peasant database opener could not be opened; enrichment stays off"),
						"503": errorResponse("This serve process has no configurable Peasant source"),
					},
				},
			},
			"/api/v1/projects": map[string]any{
				"get": map[string]any{
					"operationId": "listProjects",
					"summary":     "List projects with an active epoch mapping",
					"responses": map[string]any{
						"200": jsonResponse(ref("ProjectPage")),
						"403": errorResponse("Non-loopback Host or Origin"),
						"503": errorResponse("Project mapping routes are unavailable"),
						"500": errorResponse("The project mapping store could not be read"),
					},
				},
			},
			"/api/v1/projects/{projectHash}": map[string]any{
				"get": map[string]any{
					"operationId": "getProject",
					"summary":     "Get one project's active mapping and interval history",
					"parameters": []any{
						projectHashPathParameter(),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("ProjectView")),
						"400": errorResponse("Invalid project hash"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("The project has no mapping history"),
						"503": errorResponse("Project mapping routes are unavailable"),
						"500": errorResponse("The project mapping store could not be read"),
					},
				},
			},
			"/api/v1/projects/{projectHash}/bind": map[string]any{
				"post": map[string]any{
					"operationId": "bindProject",
					"summary":     "Map a project to an epoch",
					"description": "Closes any active mapping for the project and opens the requested one. " +
						"Binding the already-active epoch and role is idempotent. The mapping is a project-level " +
						"relationship and never writes or overrides a session binding.",
					"parameters": []any{
						projectHashPathParameter(),
					},
					"requestBody": jsonBody(ref("ProjectBindRequest")),
					"responses": map[string]any{
						"200": jsonResponse(ref("ProjectView")),
						"400": errorResponse("Invalid project hash or body"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("Unknown epoch"),
						"409": errorResponse("Binding conflict"),
						"503": errorResponse("Project mapping writes are unavailable"),
						"500": errorResponse("The project mapping store could not be written"),
					},
				},
			},
			"/api/v1/projects/{projectHash}/unbind": map[string]any{
				"post": map[string]any{
					"operationId": "unbindProject",
					"summary":     "Close a project's active epoch mapping",
					"parameters": []any{
						projectHashPathParameter(),
					},
					"requestBody": jsonBody(ref("ProjectUnbindRequest")),
					"responses": map[string]any{
						"200": jsonResponse(ref("ProjectView")),
						"400": errorResponse("Invalid project hash or body"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"409": errorResponse("No open mapping or binding conflict"),
						"503": errorResponse("Project mapping writes are unavailable"),
						"500": errorResponse("The project mapping store could not be written"),
					},
				},
			},
			"/api/v1/ws": map[string]any{
				"get": map[string]any{
					"operationId": "sessionWebSocket",
					"summary":     "Subscribe to bounded latest-frame session and epoch snapshots",
					"description": "Upgrades to a WebSocket. The server sends a ready frame, then a snapshot frame for each subscribed topic. " +
						"Snapshots carry revision and asOf at the frame level. A newer frame replaces an unread frame, so a slow client always sees the latest snapshot.",
					"responses": map[string]any{
						"101": map[string]any{"description": "Switching Protocols; subsequent frames are JSON text messages."},
						"400": errorResponse("The request is not a valid WebSocket upgrade"),
						"403": errorResponse("Non-loopback Host or Origin"),
					},
				},
			},
			"/api/v1/transcripts": map[string]any{
				"get": map[string]any{
					"operationId": "listTranscripts",
					"summary":     "List Peasant session ids that have a durable transcript",
					"description": "Prefers a read-only list of ingested session ids from the Peasant database when project enrichment has opened that database. That list is complete, so an id absent from items has no durable transcript and truncated is false. When no database catalog is available, the route falls back to a bounded, read-only `peasant sessions list --json` and reuses a successful result briefly so repeated reads do not spawn a process each time. On that fallback, truncated is true when the list filled its page: an id absent from items is then not evidence that Peasant has no transcript. Items are native session ids. The revision and asOf use the same certified session-cache read as the other session routes. A source failure is 503, which the console treats as unknown rather than none.",
					"responses": map[string]any{
						"200": jsonResponse(ref("TranscriptCatalog")),
						"403": errorResponse("Non-loopback Host or Origin"),
						"503": errorResponse("The peasant executable is absent, the catalog could not be read, or the session cache is stale"),
						"500": errorResponse("The session store could not be read"),
					},
				},
			},
			"/api/v1/sessions": map[string]any{
				"get": map[string]any{
					"operationId": "listSessions",
					"summary":     "List sessions",
					"parameters": []any{
						queryParameter("epoch", "Filter by the latest binding epoch id", "string", nil),
						queryParameter("state", "Filter by derived activity state", "string", []string{"active", "idle", "ended", "unknown"}),
						queryParameter("harness", "Filter by harness id", "string", nil),
						queryParameter("limit", "Page size (default 50, maximum 100)", "integer", nil),
						queryParameter("cursor", "Opaque page token from a previous response", "string", nil),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("Page")),
						"400": errorResponse("Invalid filter or page token"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The session store could not be read"),
					},
				},
			},
			"/api/v1/sessions/{harness}/{nativeSessionId}/bind": map[string]any{
				"post": map[string]any{
					"operationId": "bindSession",
					"summary":     "Bind a session to an epoch",
					"parameters": []any{
						pathParameter("harness", "Harness id"),
						pathParameter("nativeSessionId", "Native session id"),
					},
					"requestBody": jsonBody(ref("BindRequest")),
					"responses": map[string]any{
						"200": jsonResponse(ref("Detail")),
						"400": errorResponse("Invalid session key or body"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("Unknown epoch"),
						"409": errorResponse("Binding conflict"),
						"503": errorResponse("The session cache is stale or writes are unavailable"),
						"500": errorResponse("The session store could not be written"),
					},
				},
			},
			"/api/v1/sessions/{harness}/{nativeSessionId}/unbind": map[string]any{
				"post": map[string]any{
					"operationId": "unbindSession",
					"summary":     "Unbind a session from its epoch",
					"parameters": []any{
						pathParameter("harness", "Harness id"),
						pathParameter("nativeSessionId", "Native session id"),
					},
					"requestBody": jsonBody(ref("UnbindRequest")),
					"responses": map[string]any{
						"200": jsonResponse(ref("Detail")),
						"400": errorResponse("Invalid session key or body"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"409": errorResponse("No open binding or binding conflict"),
						"503": errorResponse("The session cache is stale or writes are unavailable"),
						"500": errorResponse("The session store could not be written"),
					},
				},
			},
			"/api/v1/sessions/{harness}/{nativeSessionId}": map[string]any{
				"get": map[string]any{
					"operationId": "getSession",
					"summary":     "Get one session",
					"parameters": []any{
						pathParameter("harness", "Harness id"),
						pathParameter("nativeSessionId", "Native session id"),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("Detail")),
						"400": errorResponse("Invalid session key"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("Session not found"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The session store could not be read"),
					},
				},
			},
			"/api/v1/sessions/{harness}/{nativeSessionId}/execution": map[string]any{
				"get": map[string]any{
					"operationId": "listSessionExecution",
					"summary":     "List a session's normalized execution observations",
					"description": "Returns one snapshot-pinned page of occurrence.v3 execution records, newest first. The page includes auxiliary and child records; the session summary uses only the newest primary or selection record. Each attribution field carries its own status, source, vocabulary, and bounded candidates rather than one guessed value.",
					"parameters": []any{
						pathParameter("harness", "Harness id"),
						pathParameter("nativeSessionId", "Native session id"),
						queryParameter("limit", "Maximum observations to return (1-100, default 50)", "integer", nil),
						queryParameter("cursor", "Resume after the cursor printed by a previous page; the cursor pins one journal snapshot", "string", nil),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("ExecutionHistoryPage")),
						"400": errorResponse("Invalid session key, limit, or cursor"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"503": errorResponse("The session cache is stale"),
						"500": errorResponse("The session store could not be read"),
					},
				},
			},
			"/api/v1/sessions/{harness}/{nativeSessionId}/transcript": map[string]any{
				"get": map[string]any{
					"operationId": "getSessionTranscript",
					"summary":     "Fetch the durable Peasant transcript for one session",
					"description": "Validates the harness and native session id against the shared @peasant-labs/schema contract, runs `peasant export sessions --session <nativeSessionId>` beside the API in a private temporary directory, and streams the durable SessionDetailPayload document unchanged. The route never returns a last-good transcript when Peasant refuses a newer incomplete attempt. The browser validates the body against the same shared contract before rendering it.",
					"parameters": []any{
						pathParameter("harness", "Harness id"),
						pathParameter("nativeSessionId", "Native session id"),
					},
					"responses": map[string]any{
						"200": jsonResponse(ref("TranscriptPayload")),
						"400": errorResponse("Invalid session key, harness, or native session id under the shared schema contract"),
						"403": errorResponse("Non-loopback Host or Origin"),
						"404": errorResponse("Peasant has no transcript for that session"),
						"409": errorResponse("Peasant refused an incomplete attempt, cannot express the session id, or returned a different identity"),
						"500": errorResponse("Peasant failed, or returned a malformed or oversized document"),
						"503": errorResponse("The peasant executable is absent or cannot be started"),
						"504": errorResponse("The bounded Peasant export wait ended"),
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"ActivityState": map[string]any{
					"type": "string",
					"enum": activityStateStrings(),
					"description": "Prompt-based liveness. A recent user prompt makes an unbound session active. " +
						"Tool, permission, binding, claim, model-switch, and role-change activity is not a prompt.",
				},
				"StateBasis": map[string]any{
					"type":        "string",
					"enum":        stateBasisStrings(),
					"description": "The evidence that decided `state`. `none` means no prompt evidence exists; `prompt` means the age of the last user prompt decided active or idle; `inactivity` means the prompt aged past the idle window and carries no end marker; `explicit-end` means an unsuperseded host session-end, an applicable explicit user unbind, or a legacy binding closed by session_end with no proved resume.",
				},
				"ActorClassification": map[string]any{
					"type":        "string",
					"enum":        actorClassificationStrings(),
					"description": "`generic-llm` is the exact reserved attribution-only actor; `explicit` is any other stored claim identity. The classification is a fact about the stored claim, never an inference from execution metadata.",
				},
				"SessionActor": objectSchema(
					map[string]any{
						"id":             map[string]any{"type": "string"},
						"classification": ref("ActorClassification"),
					},
					"id", "classification",
				),
				"ExecutionKind": map[string]any{
					"type": "string",
					"enum": executionKindStrings(),
				},
				"ExecutionStatus": map[string]any{
					"type": "string",
					"enum": executionStatusStrings(),
				},
				"ExecutionSource": map[string]any{
					"type": "string",
					"enum": executionSourceStrings(),
				},
				"Vocabulary": map[string]any{
					"type":        "string",
					"enum":        executionVocabularyStrings(),
					"description": "Native agent names and Pasture protocol roles are different vocabularies and are never translated into one another.",
				},
				"ExecutionCandidate": objectSchema(
					map[string]any{
						"source": ref("ExecutionSource"),
						"field":  map[string]any{"type": "string"},
						"value":  map[string]any{"type": "string"},
					},
					"source", "field", "value",
				),
				"AttributionValue": objectSchema(
					map[string]any{
						"status":     ref("ExecutionStatus"),
						"value":      map[string]any{"type": "string"},
						"source":     ref("ExecutionSource"),
						"field":      map[string]any{"type": "string"},
						"vocabulary": ref("Vocabulary"),
						"reason":     map[string]any{"type": "string"},
						"candidates": map[string]any{
							"type": "array", "items": ref("ExecutionCandidate"), "maxItems": 3,
							"description": "Source-tagged valid candidates retained on invalid or conflict. A conflicted field has no chosen value.",
						},
					},
					"status", "source",
				),
				"SessionExecution": objectSchema(
					map[string]any{
						"occurrenceId": map[string]any{"type": "integer", "format": "int64"},
						"observedAt":   map[string]any{"type": "string", "format": "date-time"},
						"kind":         ref("ExecutionKind"),
						"agentId":      map[string]any{"type": "string"},
						"turnId":       map[string]any{"type": "string"},
						"requestId":    map[string]any{"type": "string"},
						"provider":     ref("AttributionValue"),
						"model":        ref("AttributionValue"),
						"variant":      ref("AttributionValue"),
						"role":         ref("AttributionValue"),
						"protocolRole": ref("AttributionValue"),
					},
					"occurrenceId", "observedAt", "kind", "provider", "model", "variant", "role", "protocolRole",
				),
				"ExecutionHistoryPage": objectSchema(
					map[string]any{
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"items":    map[string]any{"type": "array", "items": ref("SessionExecution")},
						"next":     map[string]any{"type": "string"},
					},
					"revision", "asOf", "items",
				),
				"EndReason": map[string]any{
					"type": "string",
					"enum": []string{"unbind", "session_end"},
				},
				"EpochRef": objectSchema(
					map[string]any{
						"id":    map[string]any{"type": "string"},
						"title": map[string]any{"type": "string"},
					},
					"id",
				),
				"SessionBinding": objectSchema(
					map[string]any{
						"bindingId": map[string]any{"type": "string"},
						"role": map[string]any{
							"type":        "string",
							"description": "The protocol session role the bind recorded (epoch, architect, reviewer, supervisor, or worker).",
						},
						"boundAt":    map[string]any{"type": "string", "format": "date-time"},
						"source":     ref("BindingSource"),
						"confidence": ref("Confidence"),
					},
					"bindingId", "role", "boundAt", "source", "confidence",
				),
				"BindingSource": map[string]any{
					"type": "string",
					"enum": []string{"explicit", "inferred"},
					"description": "explicit rows are written by bind, unbind, or the session-end tail and are authoritative. " +
						"inferred rows are derived from store observations, never override an explicit binding, and are absent when the derivation is ambiguous.",
				},
				"Confidence": map[string]any{
					"type": "string",
					"enum": confidenceTokenStrings(),
					"description": "How strongly the binding claims to be true, using the lifecycle confidence vocabulary. " +
						"Explicit bindings are verified. An inferred binding is observed unless the store recorded a stronger derivation; " +
						"the claim-ownership rule is observed, because it correlates a session claim with active ownership rather than a command that recorded this session's intent.",
				},
				"SessionBindingInterval": objectSchema(
					map[string]any{
						"bindingId": map[string]any{"type": "string"},
						"role": map[string]any{
							"type": "string",
							"description": "For an explicit interval, the protocol session role a bind command recorded. " +
								"For an inferred interval, the assignment slot the derivation read (currently owner-responsibility), because no command gave the session a role; clients must not read an inferred role as a protocol session role.",
						},
						"boundAt":    map[string]any{"type": "string", "format": "date-time"},
						"unboundAt":  map[string]any{"type": "string", "format": "date-time"},
						"endReason":  map[string]any{"type": "string"},
						"source":     ref("BindingSource"),
						"confidence": ref("Confidence"),
						"epoch":      ref("EpochRef"),
					},
					"bindingId", "role", "boundAt", "source", "confidence",
				),
				"SessionActivity": objectSchema(
					map[string]any{
						"state":      ref("ActivityState"),
						"stateBasis": ref("StateBasis"),
						"firstSeen":  map[string]any{"type": "string", "format": "date-time"},
						"lastSeen": map[string]any{
							"type": "string", "format": "date-time",
							"description": "Broad activity instant (journal, binding, claim, and state updates). This is not prompt recency.",
						},
						"lastPromptAt": map[string]any{
							"type": "string", "format": "date-time",
							"description": "The newest user-prompt instant. Absent when the store has no prompt evidence.",
						},
						"lastEvent": map[string]any{"type": "string"},
					},
					"state", "stateBasis",
				),
				"EndMarker": objectSchema(
					map[string]any{
						"endedAt":   map[string]any{"type": "string", "format": "date-time"},
						"endReason": ref("EndReason"),
					},
					"endedAt", "endReason",
				),
				"SessionView": objectSchema(
					map[string]any{
						"harness":         map[string]any{"type": "string"},
						"nativeSessionId": map[string]any{"type": "string"},
						"epoch":           ref("EpochRef"),
						"activity":        ref("SessionActivity"),
						"actor": map[string]any{
							"allOf":       []any{ref("SessionActor")},
							"description": "The stored session claimant, shown separately from execution attribution and epoch binding. Absent when the store has no claim.",
						},
						"execution": map[string]any{
							"allOf":       []any{ref("SessionExecution")},
							"description": "The newest primary OR selection observation by journal id, as one coherent record. Auxiliary and child observations appear only in the execution history. Absent when the session has no execution evidence.",
						},
						"binding": ref("SessionBinding"),
						"bindings": map[string]any{
							"type":        "array",
							"items":       ref("SessionBindingInterval"),
							"description": "Every known binding interval for the session, ordered by boundAt ascending and then bindingId. `binding` is the effective current binding: the latest explicit binding when one exists, otherwise the open inferred derivation.",
						},
						"end": ref("EndMarker"),
						"project": map[string]any{
							"type": "string",
							"description": "Peasant-owned project display label for the session. The label depends on the configured source: " +
								"with --peasant-db it is the canonical remote label when the remote is recognizable, else the canonical cwd, else the project hash; " +
								"with --peasant-url it is Peasant's resolved project label. " +
								"Present only when the operator enabled project enrichment and Peasant mapped this session; absent otherwise. Pasture never derives it.",
						},
						"projectHash": map[string]any{
							"type": "string",
							"description": "Peasant-owned opaque project hash for the session, preserved exactly. " +
								"Present only when the operator enabled project enrichment and Peasant mapped this session; absent otherwise. Pasture never recomputes it.",
						},
					},
					"harness", "nativeSessionId", "activity", "bindings",
				),
				"Page": objectSchema(
					map[string]any{
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"items":    map[string]any{"type": "array", "items": ref("SessionView")},
						"next":     map[string]any{"type": "string"},
					},
					"revision", "asOf", "items",
				),
				"Detail": objectSchema(
					map[string]any{
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"session":  ref("SessionView"),
					},
					"revision", "asOf", "session",
				),
				"EpochView": objectSchema(
					map[string]any{
						"id":           map[string]any{"type": "string"},
						"title":        map[string]any{"type": "string"},
						"phase":        map[string]any{"type": "string"},
						"role":         map[string]any{"type": "string"},
						"sessionCount": map[string]any{"type": "integer"},
						"activeCount":  map[string]any{"type": "integer"},
					},
					"id", "phase", "role", "sessionCount", "activeCount",
				),
				"EpochPage": objectSchema(
					map[string]any{
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"items":    map[string]any{"type": "array", "items": ref("EpochView")},
					},
					"revision", "asOf", "items",
				),
				"EpochDetail": objectSchema(
					map[string]any{
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"epoch":    ref("EpochView"),
					},
					"revision", "asOf", "epoch",
				),
				"PeasantIntegration": objectSchema(
					map[string]any{
						"enabled": map[string]any{
							"type":        "boolean",
							"description": "Whether the runtime enrichment switch is on. Off is the default and is distinct from unavailable; it does not mean no source was configured.",
						},
						"source": map[string]any{
							"type":        "string",
							"enum":        []string{"none", "db", "api"},
							"description": "Which opt-in backend the switch opens when on: none while the switch is off; db for --peasant-db or the conventional Peasant database that the toggle opens when no flag was supplied; api for --peasant-url.",
						},
						"status": map[string]any{
							"type":        "string",
							"enum":        []string{"disabled", "ok", "unavailable"},
							"description": "disabled when the integration is off; ok after the configured source has answered a lookup successfully; unavailable when it has not been contacted yet or could not be opened, reached, or read.",
						},
						"message": map[string]any{
							"type":        "string",
							"description": "Actionable detail for disabled or unavailable; empty when the source is ok.",
						},
					},
					"enabled", "source", "status", "message",
				),
				"PeasantSettingsRequest": objectSchema(
					map[string]any{
						"enabled": map[string]any{
							"type":        "boolean",
							"description": "Turn the runtime Peasant project-enrichment switch on (true) or off (false).",
						},
					},
					"enabled",
				),
				"InspectPayload": objectSchema(
					map[string]any{
						"revision":           map[string]any{"type": "integer", "format": "int64"},
						"asOf":               map[string]any{"type": "string", "format": "date-time"},
						"truncated":          map[string]any{"type": "boolean"},
						"epochs":             map[string]any{"type": "array", "items": ref("EpochView")},
						"sessions":           map[string]any{"type": "array", "items": ref("SessionView")},
						"peasantIntegration": ref("PeasantIntegration"),
					},
					"revision", "asOf", "truncated", "epochs", "sessions", "peasantIntegration",
				),
				"WebSocketClientMessage": objectSchema(
					map[string]any{
						"type":  map[string]any{"type": "string", "enum": []string{"subscribe", "unsubscribe", "ping"}},
						"topic": map[string]any{"type": "string", "enum": []string{"sessions", "epochs"}},
					},
					"type",
				),
				"WebSocketReadyFrame": objectSchema(
					map[string]any{
						"type":   map[string]any{"type": "string", "enum": []string{"ready"}},
						"topics": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"type", "topics",
				),
				"WebSocketSnapshotFrame": objectSchema(
					map[string]any{
						"type":     map[string]any{"type": "string", "enum": []string{"sessions", "epochs"}},
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"payload":  map[string]any{"type": "object"},
					},
					"type", "revision", "asOf", "payload",
				),
				"WebSocketErrorFrame": objectSchema(
					map[string]any{
						"type":  map[string]any{"type": "string", "enum": []string{"error"}},
						"error": ref("ErrorBody"),
					},
					"type", "error",
				),
				"BindRequest": objectSchema(
					map[string]any{
						"epochId":         map[string]any{"type": "string"},
						"role":            map[string]any{"type": "string", "enum": roleNames},
						"expectedBinding": map[string]any{"type": "string"},
					},
					"epochId", "role",
				),
				"UnbindRequest": objectSchema(
					map[string]any{
						"expectedBinding": map[string]any{"type": "string"},
					},
				),
				"ProjectBindingSource": map[string]any{
					"type": "string",
					"enum": []string{"project"},
					"description": "Project mapping provenance. A project mapping is an explicit operator relation; " +
						"it is never inferred from a working directory or a session binding.",
				},
				"ProjectBinding": objectSchema(
					map[string]any{
						"bindingId": map[string]any{"type": "string"},
						"role": map[string]any{
							"type":        "string",
							"enum":        roleNames,
							"description": "The role the operator recorded for the project in this epoch.",
						},
						"boundAt":    map[string]any{"type": "string", "format": "date-time"},
						"source":     ref("ProjectBindingSource"),
						"confidence": ref("Confidence"),
					},
					"bindingId", "role", "boundAt", "source", "confidence",
				),
				"ProjectBindingInterval": objectSchema(
					map[string]any{
						"bindingId":  map[string]any{"type": "string"},
						"role":       map[string]any{"type": "string", "enum": roleNames},
						"boundAt":    map[string]any{"type": "string", "format": "date-time"},
						"unboundAt":  map[string]any{"type": "string", "format": "date-time"},
						"endReason":  map[string]any{"type": "string", "enum": []string{"rebind", "unbind"}},
						"source":     ref("ProjectBindingSource"),
						"confidence": ref("Confidence"),
						"epoch":      ref("EpochRef"),
					},
					"bindingId", "role", "boundAt", "source", "confidence",
				),
				"ProjectView": objectSchema(
					map[string]any{
						"projectHash": map[string]any{
							"type":        "string",
							"pattern":     "^[0-9a-f]{64}$",
							"description": "Peasant-owned project identity, preserved exactly. Pasture never derives or recomputes it.",
						},
						"binding": ref("ProjectBinding"),
						"epoch":   ref("EpochRef"),
						"intervals": map[string]any{
							"type":        "array",
							"items":       ref("ProjectBindingInterval"),
							"description": "Full mapping history ordered by boundAt ascending. Present on detail responses; omitted from list rows.",
						},
					},
					"projectHash",
				),
				"ProjectPage": objectSchema(
					map[string]any{
						"items": map[string]any{
							"type":        "array",
							"items":       ref("ProjectView"),
							"description": "Projects with an active mapping. A project whose mappings are all closed is omitted.",
						},
					},
					"items",
				),
				"ProjectBindRequest": objectSchema(
					map[string]any{
						"epochId": map[string]any{"type": "string"},
						"role": map[string]any{
							"type":        "string",
							"enum":        roleNames,
							"default":     "worker",
							"description": "Role recorded for the project in this epoch. Omit for worker.",
						},
						"expectedBinding": map[string]any{
							"type":        "string",
							"description": "Only change the mapping when this binding ID is still active.",
						},
					},
					"epochId",
				),
				"ProjectUnbindRequest": objectSchema(
					map[string]any{
						"expectedBinding": map[string]any{
							"type":        "string",
							"description": "Only close the mapping when this binding ID is still active.",
						},
					},
				),
				"TranscriptCatalog": objectSchema(
					map[string]any{
						"revision": map[string]any{"type": "integer", "format": "int64"},
						"asOf":     map[string]any{"type": "string", "format": "date-time"},
						"items": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Peasant session ids that have a durable transcript. When truncated is false, an id absent from this list has no durable transcript. When truncated is true, absence is not that evidence.",
						},
						"truncated": map[string]any{
							"type":        "boolean",
							"description": "True when the backing list filled its page. The console must not treat an absent id as having no transcript while this is true.",
						},
					},
					"revision", "asOf", "items", "truncated",
				),
				"TranscriptPayload": map[string]any{
					"type": "object",
					"description": "The durable Peasant SessionDetailPayload document, proxied unchanged. " +
						"Pasture guarantees only the identity fields; every other field belongs to " +
						"the shared @peasant-labs/schema session contract and is validated by the browser.",
					"properties": map[string]any{
						"id":      map[string]any{"type": "string"},
						"harness": map[string]any{"type": "string"},
						"turns":   map[string]any{"type": "array", "items": map[string]any{"description": "One Peasant TurnDetail; validated by the browser against the shared contract"}},
					},
					"required":             []string{"id", "harness", "turns"},
					"additionalProperties": true,
				},
				"ErrorBody": objectSchema(
					map[string]any{
						"code":    map[string]any{"type": "string"},
						"message": map[string]any{"type": "string"},
					},
					"code", "message",
				),
				"ErrorEnvelope": objectSchema(
					map[string]any{"error": ref("ErrorBody")},
					"error",
				),
			},
		},
	}
}

func jsonBody(schema map[string]any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{"schema": schema},
		},
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	req := make([]string, len(required))
	copy(req, required)
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   req,
	}
}

func queryParameter(name, description, typ string, enum []string) map[string]any {
	schema := map[string]any{"type": typ}
	if enum != nil {
		schema["enum"] = enum
	}
	if name == "limit" {
		schema["minimum"] = 1
		schema["maximum"] = 100
		schema["default"] = 50
	}
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      schema,
	}
}

func pathParameter(name, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": description,
		"schema":      map[string]any{"type": "string", "maxLength": 512},
	}
}

// projectHashPathParameter documents the exact Peasant-owned project identity
// the route accepts. The hash is never trimmed or coerced.
func projectHashPathParameter() map[string]any {
	return map[string]any{
		"name":        "projectHash",
		"in":          "path",
		"required":    true,
		"description": "Peasant-owned project hash: exactly 64 lowercase hex characters",
		"schema": map[string]any{
			"type":      "string",
			"pattern":   "^[0-9a-f]{64}$",
			"minLength": 64,
			"maxLength": 64,
		},
	}
}
