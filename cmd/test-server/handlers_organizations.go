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

// handleListOrgUsers backs org.Grants' GetOrganizationUsers: one role-filtered,
// CBP-paginated pass per role over /organizations/{id}/users.json, returning
// the org's members with that role.
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/organizations/organizations/#list-users-in-an-organization
func (srv *server) handleListOrgUsers(w http.ResponseWriter, r *http.Request) {
	orgID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "InvalidId"})
		return
	}

	q := r.URL.Query()
	size, _ := strconv.Atoi(q.Get("page[size]"))
	all := srv.state.ListOrgUsersByRole(orgID, q.Get("role"))
	page, hasMore, next := cbpPage(all, size, q.Get("page[after]"))

	users := make([]map[string]any, 0, len(page))
	for _, u := range page {
		users = append(users, userJSON(u))
	}
	writeJSON(w, map[string]any{
		"users": users,
		keyMeta: cbpMeta(hasMore, next),
	})
}
