package sessionapi

import "encoding/json"

// PastureLocalAPIVersion is the frozen document-surface version for the local
// session API. It changes only when a route or wire shape changes.
const PastureLocalAPIVersion = "0.2.0"

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
			"description": "Loopback-only read API for active harness sessions mapped to " +
				"epoch bindings. Transcripts are not served by this API.",
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
						"role":      map[string]any{"type": "string"},
						"boundAt":   map[string]any{"type": "string", "format": "date-time"},
					},
					"bindingId", "role", "boundAt",
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
						"end":             ref("EndMarker"),
					},
					"harness", "nativeSessionId", "activity",
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
				"InspectPayload": objectSchema(
					map[string]any{
						"revision":  map[string]any{"type": "integer", "format": "int64"},
						"asOf":      map[string]any{"type": "string", "format": "date-time"},
						"truncated": map[string]any{"type": "boolean"},
						"epochs":    map[string]any{"type": "array", "items": ref("EpochView")},
						"sessions":  map[string]any{"type": "array", "items": ref("SessionView")},
					},
					"revision", "asOf", "truncated", "epochs", "sessions",
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
