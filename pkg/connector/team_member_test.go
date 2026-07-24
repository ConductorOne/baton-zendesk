package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
)

// newTeamMemberTestClient builds a *client.ZendeskClient pointed at the given
// httptest server, mirroring newTestClient in pkg/client/client_test.go and
// newTicketTestConnector in ticket_test.go.
func newTeamMemberTestClient(t *testing.T, baseURL string) *client.ZendeskClient {
	t.Helper()
	c, err := client.New(context.Background(), nil, "", "test@example.com", "token", baseURL)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

// TestTeamMemberGrants_SyncOrgsTrue_EmitsOrgGrants covers the "org" resource
// type in scope case (syncOrgs=true, which is also what a nil/unfiltered
// *cli.ConnectorOpts produces via New): Grants must resolve the member's role
// and emit an org grant per organization membership.
func TestTeamMemberGrants_SyncOrgsTrue_EmitsOrgGrants(t *testing.T) {
	const userID = int64(9001)
	const orgID = int64(555)

	mux := http.NewServeMux()
	mux.HandleFunc("/users/9001.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]any{"id": userID, "name": "Alice Admin", "role": "admin"},
		})
	})
	mux.HandleFunc("/organization_memberships.json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user_id") != "9001" {
			http.Error(w, "missing user_id", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organization_memberships": []map[string]any{
				{"id": 1, "user_id": userID, "organization_id": orgID, "organization_name": "Acme"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, true)
	resource := &v2.Resource{Id: &v2.ResourceId{ResourceType: resourceTypeTeam.Id, Resource: "9001"}}

	grants, _, err := tm.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected 1 org grant, got %d", len(grants))
	}
	gotEntitlementResource := grants[0].GetEntitlement().GetResource()
	if gotEntitlementResource.GetId().GetResourceType() != resourceTypeOrg.Id {
		t.Fatalf("expected grant against resource type %q, got %q", resourceTypeOrg.Id, gotEntitlementResource.GetId().GetResourceType())
	}
	if gotEntitlementResource.GetId().GetResource() != "555" {
		t.Fatalf("expected grant against org 555, got %q", gotEntitlementResource.GetId().GetResource())
	}
}

// TestTeamMemberGrants_SyncOrgsFalse_NoGrantsNoHTTPCalls covers the bug this
// change fixes: when the sync filter excludes "org", Grants must short-circuit
// before making any org-membership (or role-resolution) API calls, not just
// filter the result set down to zero.
func TestTeamMemberGrants_SyncOrgsFalse_NoGrantsNoHTTPCalls(t *testing.T) {
	var calls int

	mux := http.NewServeMux()
	// Any hit on these endpoints means the guard failed to short-circuit.
	mux.HandleFunc("/users/9001.json", func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	})
	mux.HandleFunc("/organization_memberships.json", func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, false)
	resource := &v2.Resource{Id: &v2.ResourceId{ResourceType: resourceTypeTeam.Id, Resource: "9001"}}

	grants, res, err := tm.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected 0 grants when org sync is disabled, got %d", len(grants))
	}
	if res == nil {
		t.Fatalf("expected non-nil SyncOpResults")
	}
	if calls != 0 {
		t.Fatalf("expected 0 HTTP calls when org sync is disabled, got %d", calls)
	}
}
