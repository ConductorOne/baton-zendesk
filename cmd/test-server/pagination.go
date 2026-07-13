package main

import (
	"strconv"
	"strings"
)

// cbpPage slices items per Zendesk's cursor-based-pagination contract
// (page[size] / page[after]), matching the convention baton-zendesk's own
// pkg/client/client_test.go mocks already use: the cursor is the decimal
// offset of the next item, prefixed with "p" (e.g. "p3").
//
// Returns (page, hasMore, nextCursor).
func cbpPage[T any](items []T, size int, after string) ([]T, bool, string) {
	start := 0
	if after != "" {
		if n, err := strconv.Atoi(strings.TrimPrefix(after, "p")); err == nil {
			start = n
		}
	}
	if start >= len(items) {
		return nil, false, ""
	}
	if size <= 0 {
		size = 100
	}
	end := start + size
	if end >= len(items) {
		return items[start:], false, ""
	}
	return items[start:end], true, "p" + strconv.Itoa(end)
}
