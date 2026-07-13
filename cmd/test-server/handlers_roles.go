package main

import "net/http"

// handleListCustomRoles backs role.List. Returns an empty list rather than
// simulating the SupportProductInactive 403 some real tenants return (e.g.
// conductorone-2178) — that path is unrelated to what this server exists to
// validate (the org/team_member grant inversion) and an empty 200 lets a
// full sync complete instead of aborting.
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/users/custom_roles/#list-custom-roles
func (srv *server) handleListCustomRoles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"custom_roles": []map[string]any{}})
}
