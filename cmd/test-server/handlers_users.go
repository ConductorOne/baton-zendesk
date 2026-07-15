package main

import (
	"net/http"
	"strconv"
	"strings"
)

// handleListUsers backs team_member.List — called once with role=admin and
// once with role=agent (see pagination.Bag in pkg/connector/team_member.go).
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/users/users/#list-users
func (srv *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	role := q.Get("role")
	size, _ := strconv.Atoi(q.Get("page[size]"))

	all := srv.state.ListUsersByRole(role)
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

// handleGetUser backs resolveMemberRole's cache-miss fallback (GetUser).
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/users/users/#show-user
func (srv *server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSuffix(r.PathValue("idWithExt"), ".json")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "InvalidId"})
		return
	}
	u, ok := srv.state.GetUser(id)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{
			keyError:       "RecordNotFound",
			keyDescription: "Not found",
		})
		return
	}
	writeJSON(w, map[string]any{"user": userJSON(u)})
}

func userJSON(u *TeamMember) map[string]any {
	return map[string]any{
		"id":    u.ID,
		keyName: u.Name,
		"email": u.Email,
		"role":  u.Role,
	}
}
