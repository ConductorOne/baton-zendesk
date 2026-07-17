package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/nukosuke/go-zendesk/zendesk"
)

// CXH-1907 quirk: empty `query=` on role-filtered users endpoints disables CBP,
// so meta.has_more / after_cursor are dropped and sync stops after one page.
func zendeskCBPMock(t *testing.T, total int, requireParam, requireValue string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	respond := func(w http.ResponseWriter, r *http.Request) {
		rawQuery := r.URL.RawQuery
		values := r.URL.Query()

		if requireParam != "" && values.Get(requireParam) != requireValue {
			http.Error(w, fmt.Sprintf("missing %s=%s", requireParam, requireValue), http.StatusBadRequest)
			return
		}

		users := make([]zendesk.User, 0, 100)
		// Empty query= disables CBP on these endpoints. Detect the literal
		// substring so an empty value still trips the bug.
		cbpDisabled := containsEmptyQueryParam(rawQuery, "query")

		after := values.Get("page[after]")
		start := 0
		if after != "" {
			// Cursors are "p<N>" where N is the index of the first user in the next page.
			_, err := fmt.Sscanf(after, "p%d", &start)
			if err != nil {
				http.Error(w, "bad cursor", http.StatusBadRequest)
				return
			}
		}

		end := start + 100
		if end > total {
			end = total
		}
		for i := start; i < end; i++ {
			users = append(users, zendesk.User{ID: int64(i + 1), Name: fmt.Sprintf("user-%d", i+1)})
		}

		body := map[string]any{"users": users}
		if !cbpDisabled && end < total {
			body["meta"] = map[string]any{
				"has_more":     true,
				"after_cursor": fmt.Sprintf("p%d", end),
			}
		} else if !cbpDisabled {
			body["meta"] = map[string]any{
				"has_more":     false,
				"after_cursor": "",
			}
		}
		// When cbpDisabled is true, meta is omitted entirely (matches Zendesk's
		// observed wire behaviour).

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}

	mux.HandleFunc("/users.json", respond)
	mux.HandleFunc("/organizations/", func(w http.ResponseWriter, r *http.Request) {
		// /organizations/{id}/users.json
		if !strings.HasSuffix(r.URL.Path, "/users.json") {
			http.NotFound(w, r)
			return
		}
		respond(w, r)
	})

	return httptest.NewServer(mux)
}

// containsEmptyQueryParam reports whether rawQuery serialises `key=` with an
// empty value (the exact wire shape that disables CBP on Zendesk's role-filtered
// users endpoints).
func containsEmptyQueryParam(rawQuery, key string) bool {
	for _, part := range strings.Split(rawQuery, "&") {
		if part == key+"=" || strings.HasPrefix(part, key+"=&") {
			return true
		}
	}
	return false
}

func newTestClient(t *testing.T, baseURL string) *ZendeskClient {
	t.Helper()
	c, err := New(context.Background(), nil, "", "test@example.com", "token", baseURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func drainListUsers(t *testing.T, c *ZendeskClient, role string) int {
	t.Helper()
	ctx := context.Background()
	total := 0
	token := ""
	for {
		users, next, err := c.ListUsers(ctx, role, token)
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		total += len(users)
		if next == "" {
			return total
		}
		token = next
	}
}

func drainListUsersByRole(t *testing.T, c *ZendeskClient, roleID int64) int {
	t.Helper()
	ctx := context.Background()
	total := 0
	token := ""
	for {
		users, next, err := c.ListUsersByRole(ctx, roleID, token)
		if err != nil {
			t.Fatalf("ListUsersByRole: %v", err)
		}
		total += len(users)
		if next == "" {
			return total
		}
		token = next
	}
}

func drainGetOrganizationUsers(t *testing.T, c *ZendeskClient, orgID *v2.ResourceId, role string) int {
	t.Helper()
	ctx := context.Background()
	total := 0
	token := ""
	for {
		users, next, err := c.GetOrganizationUsers(ctx, orgID, role, token)
		if err != nil {
			t.Fatalf("GetOrganizationUsers: %v", err)
		}
		total += len(users)
		if next == "" {
			return total
		}
		token = next
	}
}

func TestListUsers_RoleFilteredCBPPaginatesPastFirstPage(t *testing.T) {
	srv := zendeskCBPMock(t, 106, "role", "agent")
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got := drainListUsers(t, c, "agent")
	if got != 106 {
		t.Fatalf("CXH-1907: expected 106 users across both pages, got %d (truncation regression)", got)
	}
}

func TestListUsersByRole_PermissionSetCBPPaginatesPastFirstPage(t *testing.T) {
	srv := zendeskCBPMock(t, 106, "permission_set", "42")
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got := drainListUsersByRole(t, c, 42)
	if got != 106 {
		t.Fatalf("CXH-1907: expected 106 users across both pages, got %d (truncation regression)", got)
	}
}

func TestGetOrganizationUsers_RoleFilteredCBPPaginatesPastFirstPage(t *testing.T) {
	srv := zendeskCBPMock(t, 106, "role", "agent")
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got := drainGetOrganizationUsers(t, c, &v2.ResourceId{Resource: "9001"}, "agent")
	if got != 106 {
		t.Fatalf("CXH-1907: expected 106 users across both pages, got %d (truncation regression)", got)
	}
}

// CXH-1908 (pagination): the matching membership lives on page 2; the pre-fix
// callers discarded nextPage so the lookup returned "" and revoke silently
// became GrantAlreadyRevoked, leaving the user with access.
func TestGetOrganizationMembershipByUser_FindsMembershipOnPage2(t *testing.T) {
	const (
		userID       = int64(42)
		orgAID       = int64(555) // on page 1 — not what we want
		orgBID       = int64(777) // on page 2 — the target
		orgAMemberID = int64(1001)
		orgBMemberID = int64(1002)
	)

	nextPageURL := "/organization_memberships.json?page=2"

	mux := http.NewServeMux()
	mux.HandleFunc("/organization_memberships.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			// Page 2: contains the target membership, no next page.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"organization_memberships": []map[string]any{
					{"id": orgBMemberID, "user_id": userID, "organization_id": orgBID},
				},
			})
		} else {
			// Page 1: only Org A, next_page points to page 2.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"organization_memberships": []map[string]any{
					{"id": orgAMemberID, "user_id": userID, "organization_id": orgAID},
				},
				"next_page": nextPageURL,
			})
		}
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
		t.Fatalf("pagination: expected membership 1002 (org %d, page 2), got %q", orgBID, got)
	}
}

// Symmetric pagination test for GetGroupMembershipByGroup: matching membership
// on page 2, wrong group on page 1 — ensures the loop fix is locked in for
// the group path just as it is for the org path above.
func TestGetGroupMembershipByGroup_FindsMembershipOnPage2(t *testing.T) {
	const (
		userID        = int64(42)
		groupAID      = int64(555) // on page 1 — not what we want
		groupBID      = int64(777) // on page 2 — the target
		groupAMemID   = int64(1001)
		groupBMemID   = int64(1002)
	)

	nextPageURL := "/group_memberships.json?page=2"

	mux := http.NewServeMux()
	mux.HandleFunc("/group_memberships.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"group_memberships": []map[string]any{
					{"id": groupBMemID, "user_id": userID, "group_id": groupBID},
				},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"group_memberships": []map[string]any{
					{"id": groupAMemID, "user_id": userID, "group_id": groupAID},
				},
				"next_page": nextPageURL,
			})
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	got, _, err := c.GetGroupMembershipByGroup(context.Background(), zendesk.GroupMembership{
		UserID:  userID,
		GroupID: groupBID,
	})
	if err != nil {
		t.Fatalf("GetGroupMembershipByGroup: %v", err)
	}
	if got != "1002" {
		t.Fatalf("pagination: expected membership 1002 (group %d, page 2), got %q", groupBID, got)
	}
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

// orgMembershipsCBPMock serves /organization_memberships.json with cursor
// pagination, requiring the user_id filter. Mirrors zendeskCBPMock but for the
// membership payload consumed by GetUserOrganizationMemberships, and trips the
// same empty-query= CBP bug so a regression to the hand-rolled URL is caught.
func orgMembershipsCBPMock(t *testing.T, total int, requireUserID string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organization_memberships.json", func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		if values.Get("user_id") != requireUserID {
			http.Error(w, fmt.Sprintf("missing user_id=%s", requireUserID), http.StatusBadRequest)
			return
		}
		cbpDisabled := containsEmptyQueryParam(r.URL.RawQuery, "query")

		start := 0
		if after := values.Get("page[after]"); after != "" {
			if _, err := fmt.Sscanf(after, "p%d", &start); err != nil {
				http.Error(w, "bad cursor", http.StatusBadRequest)
				return
			}
		}
		end := start + 100
		if end > total {
			end = total
		}
		memberships := make([]zendesk.OrganizationMembership, 0, 100)
		for i := start; i < end; i++ {
			memberships = append(memberships, zendesk.OrganizationMembership{
				ID:             int64(i + 1),
				UserID:         9001,
				OrganizationID: int64(i + 1),
			})
		}

		body := map[string]any{"organization_memberships": memberships}
		if !cbpDisabled && end < total {
			body["meta"] = map[string]any{"has_more": true, "after_cursor": fmt.Sprintf("p%d", end)}
		} else if !cbpDisabled {
			body["meta"] = map[string]any{"has_more": false, "after_cursor": ""}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	return httptest.NewServer(mux)
}

func drainGetUserOrganizationMemberships(t *testing.T, c *ZendeskClient, userID int64) int {
	t.Helper()
	ctx := context.Background()
	total := 0
	token := ""
	for {
		memberships, next, err := c.GetUserOrganizationMemberships(ctx, userID, token)
		if err != nil {
			t.Fatalf("GetUserOrganizationMemberships: %v", err)
		}
		total += len(memberships)
		if next == "" {
			return total
		}
		token = next
	}
}

// GetUserOrganizationMemberships must page past the first 100 memberships or a
// user in many orgs would silently lose memberships beyond page 1. Guards the
// hand-rolled CBP URL against the empty-query= regression (CXH-1907).
func TestGetUserOrganizationMemberships_CBPPaginatesPastFirstPage(t *testing.T) {
	srv := orgMembershipsCBPMock(t, 106, "9001")
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got := drainGetUserOrganizationMemberships(t, c, 9001)
	if got != 106 {
		t.Fatalf("expected 106 memberships across both pages, got %d (CBP truncation regression)", got)
	}
}
