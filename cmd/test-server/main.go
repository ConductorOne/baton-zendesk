// Command test-server is a mock Zendesk API used to validate baton-zendesk's
// sync and provisioning paths locally, without a real tenant. See README.md
// in this directory for what it mocks and how to run it.
package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

const listenAddr = "127.0.0.1:8765"

type server struct {
	state *State
	oauth *oauthState
}

func newServer() *server {
	return &server{state: NewState(), oauth: newOAuthState()}
}

// newMux builds the route table shared by run() and the integration tests.
func newMux(srv *server) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /oauth/tokens", srv.handleOAuthTokens)
	mux.HandleFunc("GET /organizations.json", srv.requireAuth(srv.handleListOrganizations))
	mux.HandleFunc("GET /users.json", srv.requireAuth(srv.handleListUsers))
	// Go's ServeMux requires a wildcard to occupy the whole path segment, so
	// the ".json" suffix can't sit in the same {id} wildcard — capture it and
	// strip the suffix in the handler instead.
	mux.HandleFunc("GET /users/{idWithExt}", srv.requireAuth(srv.handleGetUser))
	mux.HandleFunc("GET /organization_memberships.json", srv.requireAuth(srv.handleOrgMemberships))
	mux.HandleFunc("POST /organization_memberships.json", srv.requireAuth(srv.handleOrgMemberships))
	mux.HandleFunc("DELETE /organization_memberships/{id}", srv.requireAuth(srv.handleDeleteOrgMembership))
	mux.HandleFunc("GET /groups.json", srv.requireAuth(srv.handleListGroups))
	mux.HandleFunc("GET /group_memberships.json", srv.requireAuth(srv.handleListGroupMemberships))
	mux.HandleFunc("GET /custom_roles.json", srv.requireAuth(srv.handleListCustomRoles))

	mux.HandleFunc("POST /tickets.json", srv.requireAuth(srv.handleCreateTicket))
	mux.HandleFunc("GET /tickets/{idWithExt}", srv.requireAuth(srv.handleGetTicket))
	mux.HandleFunc("GET /ticket_fields.json", srv.requireAuth(srv.handleListTicketFields))
	mux.HandleFunc("GET /ticket_forms.json", srv.requireAuth(srv.handleListTicketForms))

	// Debug-only, not part of the Zendesk API: exposes the per-path call
	// counters so a validation script can assert the old per-org
	// GetOrganizationUsers endpoint (/organizations/{id}/users.json) is never
	// hit after the org.Grants -> team_member.Grants inversion.
	mux.HandleFunc("GET /__debug/calls", srv.handleDebugCalls)

	return mux
}

func run() error {
	srv := newServer()
	mux := newMux(srv)

	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           recordingMiddleware(srv.state, mux),
		ReadHeaderTimeout: 30 * time.Second,
	}

	teamMembers := append(srv.state.ListUsersByRole("admin"), srv.state.ListUsersByRole("agent")...)
	membershipCount := 0
	for _, u := range teamMembers {
		membershipCount += len(srv.state.ListMembershipsByUser(u.ID))
	}

	log.Printf("test-server: listening on http://%s", listenAddr)
	log.Printf("test-server: seeded %d orgs, %d team members, %d org memberships, %d groups",
		len(srv.state.ListOrganizations()), len(teamMembers), membershipCount, len(srv.state.ListGroups()))
	log.Printf("test-server: auth is Basic %s/token : %s", testEmail, testAPIToken)
	log.Printf("test-server: or OAuth client credentials %s : %s", testOAuthClientID, testOAuthClientSecret)

	return httpSrv.ListenAndServe()
}

// recordingMiddleware tallies every request by method+path in state, so a
// validation run can inspect GET /__debug/calls afterward (e.g. to assert
// the old per-org GetOrganizationUsers endpoint was never hit). It
// deliberately does not log the raw path/query to stdout — request data
// reaching a log sink is exactly the log-injection pattern gosec's G706
// flags, and the call-count map already gives the same visibility safely.
func recordingMiddleware(state *State, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.RecordCall(r.Method + " " + r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
