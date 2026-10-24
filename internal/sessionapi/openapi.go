package sessionapi

import "encoding/json"

// PastureLocalAPIVersion is the frozen document-surface version for the local
// session API. It changes only when a route or wire shape changes.
const PastureLocalAPIVersion = "0.4.0"

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
				"epoch bindings. The transcript route proxies one durable Peasant " +
				"SessionDetailPayload; Pasture does not reshape transcript content.",
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
					"enum": []string{"active", "idle", "ended", "unknown"},
				},
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
						"state":     ref("ActivityState"),
						"firstSeen": map[string]any{"type": "string", "format": "date-time"},
						"lastSeen":  map[string]any{"type": "string", "format": "date-time"},
						"lastEvent": map[string]any{"type": "string"},
					},
					"state",
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
						"binding":         ref("SessionBinding"),
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
							"description": "Whether the operator enabled Peasant project enrichment. Disabled is the default and is distinct from unavailable.",
						},
						"source": map[string]any{
							"type":        "string",
							"enum":        []string{"none", "db", "api"},
							"description": "Which opt-in backend is configured: none when disabled, db for --peasant-db, api for --peasant-url.",
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
						"role":            map[string]any{"type": "string", "enum": []string{"epoch", "architect", "reviewer", "supervisor", "worker"}},
						"expectedBinding": map[string]any{"type": "string"},
					},
					"epochId", "role",
				),
				"UnbindRequest": objectSchema(
					map[string]any{
						"expectedBinding": map[string]any{"type": "string"},
					},
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
