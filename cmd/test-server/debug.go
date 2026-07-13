package main

import "net/http"

// handleDebugCalls is not part of the Zendesk API — it exists so a
// validation run can assert the old per-org GetOrganizationUsers endpoint
// (/organizations/{id}/users.json) was never called during a sync, and that
// team_member.List's admin+agent passes didn't fan out beyond the seeded
// roster. See README.md "Validating the fix" section.
func (srv *server) handleDebugCalls(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"calls": srv.state.CallCounts()})
}
