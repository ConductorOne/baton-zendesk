package main

import (
	"net/http"
	"strconv"
)

// handleListOrganizations backs org.List.
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/organizations/organizations/#list-organizations
func (srv *server) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size, _ := strconv.Atoi(q.Get("page[size]"))
	page, hasMore, next := cbpPage(srv.state.ListOrganizations(), size, q.Get("page[after]"))

	orgs := make([]map[string]any, 0, len(page))
	for _, o := range page {
		orgs = append(orgs, map[string]any{
			"id":    o.ID,
			keyName: o.Name,
			"url":   o.URL,
		})
	}
	writeJSON(w, map[string]any{
		"organizations": orgs,
		keyMeta:         cbpMeta(hasMore, next),
	})
}
