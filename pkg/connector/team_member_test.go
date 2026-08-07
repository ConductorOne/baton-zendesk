package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
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

// TestTeamMemberResourceType_OrgInScope_SkipsEntitlementsOnly covers the
// default (org in scope) case: ResourceType must annotate with SkipEntitlements
// only, so the SDK still calls Grants (team_member has no entitlements of its
// own, but Grants is what emits the cross-type org grants).
func TestTeamMemberResourceType_OrgInScope_SkipsEntitlementsOnly(t *testing.T) {
	tm := teamMemberBuilder(newTeamMemberTestClient(t, "http://example.invalid"), nil, false)

	rt := tm.ResourceType(context.Background())

	annos := annotations.Annotations(rt.GetAnnotations())
	if !annos.Contains(&v2.SkipEntitlements{}) {
		t.Fatalf("expected SkipEntitlements annotation when org is in scope, got %v", rt.GetAnnotations())
	}
	if annos.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Fatalf("expected no SkipEntitlementsAndGrants annotation when org is in scope, got %v", rt.GetAnnotations())
	}
}

// TestTeamMemberResourceType_OrgOutOfScope_SkipsEntitlementsAndGrants covers
// the excluded case: ResourceType must annotate with SkipEntitlementsAndGrants,
// which causes the SDK's sync engine to skip Grants() entirely (no cross-type
// org grants ever attempted) for a resource type that was never synced.
func TestTeamMemberResourceType_OrgOutOfScope_SkipsEntitlementsAndGrants(t *testing.T) {
	tm := teamMemberBuilder(newTeamMemberTestClient(t, "http://example.invalid"), nil, true)

	rt := tm.ResourceType(context.Background())

	annos := annotations.Annotations(rt.GetAnnotations())
	if !annos.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Fatalf("expected SkipEntitlementsAndGrants annotation when org is out of scope, got %v", rt.GetAnnotations())
	}
}

// TestTeamMemberResourceType_DoesNotMutateSharedResourceType guards against a
// regression where ResourceType forgets to proto.Clone before annotating:
// resourceTypeTeam is a package-level var shared across every
// teamMemberResourceType instance (and any other code that references it), so
// annotating the clone must never leak onto it.
func TestTeamMemberResourceType_DoesNotMutateSharedResourceType(t *testing.T) {
	original := annotations.Annotations(resourceTypeTeam.GetAnnotations())
	if original.Contains(&v2.SkipEntitlements{}) || original.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Fatalf("precondition failed: resourceTypeTeam already carries a skip annotation before the test runs: %v", resourceTypeTeam.GetAnnotations())
	}

	tmInScope := teamMemberBuilder(newTeamMemberTestClient(t, "http://example.invalid"), nil, false)
	tmOutOfScope := teamMemberBuilder(newTeamMemberTestClient(t, "http://example.invalid"), nil, true)

	_ = tmInScope.ResourceType(context.Background())
	_ = tmOutOfScope.ResourceType(context.Background())
	_ = tmOutOfScope.ResourceType(context.Background())

	after := annotations.Annotations(resourceTypeTeam.GetAnnotations())
	if after.Contains(&v2.SkipEntitlements{}) {
		t.Fatalf("resourceTypeTeam was mutated with SkipEntitlements: %v", resourceTypeTeam.GetAnnotations())
	}
	if after.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Fatalf("resourceTypeTeam was mutated with SkipEntitlementsAndGrants: %v", resourceTypeTeam.GetAnnotations())
	}
	if len(after) != len(original) {
		t.Fatalf("resourceTypeTeam annotation count changed: got %d, want %d", len(after), len(original))
	}
}

// TestTeamMemberGrants_EmitsOrgGrants is a minimal sanity check that Grants
// still does its original, unconditional work: gating now happens entirely at
// the ResourceType annotation level (see the tests above), so Grants itself
// must no longer early-return based on any org-scope flag.
func TestTeamMemberGrants_EmitsOrgGrants(t *testing.T) {
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

	// skipOrgResourceType=true here on purpose: Grants must emit the grant
	// regardless, since gating is now solely the SDK's job via ResourceType's
	// annotations, not something Grants itself decides.
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
