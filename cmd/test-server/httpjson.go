package main

import (
	"encoding/json"
	"net/http"
)

// JSON key constants shared across handlers, per golangci-lint's goconst check.
const (
	keyError       = "error"
	keyDescription = "description"
	keyName        = "name"
	keyMeta        = "meta"
)

func writeJSON(w http.ResponseWriter, body map[string]any) {
	writeJSONStatus(w, http.StatusOK, body)
}

func writeJSONStatus(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// cbpMeta builds the {"has_more", "after_cursor"} object cbpPage's result
// maps onto — the shape zendesk.CursorPaginationMeta unmarshals.
func cbpMeta(hasMore bool, cursor string) map[string]any {
	return map[string]any{
		"has_more":     hasMore,
		"after_cursor": cursor,
	}
}
