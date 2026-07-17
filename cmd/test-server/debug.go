package main

import "net/http"

// handleDebugCalls is not part of the Zendesk API — it exists so a
// validation run can inspect which endpoints a sync hit, e.g. that org.Grants
// made its per-org GetOrganizationUsers passes over
// /organizations/{id}/users.json and that team_member.List's admin+agent
// passes didn't fan out beyond the seeded roster. See README.md.
func (srv *server) handleDebugCalls(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"calls": srv.state.CallCounts()})
}
