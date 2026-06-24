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
