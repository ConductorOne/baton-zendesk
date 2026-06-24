package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nukosuke/go-zendesk/zendesk"
)

func newTestClient(t *testing.T, baseURL string) *ZendeskClient {
	t.Helper()
	c, err := New(context.Background(), nil, "", "test@example.com", "token", baseURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// CXH-1908: when a user belongs to multiple organizations and the API returns
// every user-matching membership, GetOrganizationMembershipByUser must pick the
// one whose OrganizationID matches the caller's request. The pre-fix loop
// matched on UserID only and returned the first row, deleting the wrong org.
func TestGetOrganizationMembershipByUser_PicksMatchingOrgWhenUserInMultipleOrgs(t *testing.T) {
	const (
		userID       = int64(42)
		orgAID       = int64(555)
		orgBID       = int64(777)
		orgAMemberID = int64(1001)
		orgBMemberID = int64(1002)
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/organization_memberships.json", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"organization_memberships": []map[string]any{
				{"id": orgAMemberID, "user_id": userID, "organization_id": orgAID},
				{"id": orgBMemberID, "user_id": userID, "organization_id": orgBID},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	got, _, err := c.GetOrganizationMembershipByUser(context.Background(), zendesk.OrganizationMembershipListOptions{
		UserID:         userID,
		OrganizationID: orgBID,
	})
	if err != nil {
		t.Fatalf("GetOrganizationMembershipByUser: %v", err)
	}
	if got != "1002" {
		t.Fatalf("CXH-1908: expected membership 1002 (org %d), got %q (matched UserID only, returned first row for org %d)", orgBID, got, orgAID)
	}
}
