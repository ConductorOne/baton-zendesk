package main

import (
	"net/http"
	"strconv"
)

// handleListGroups backs group.List. Groups/group memberships aren't touched
// by the org/team_member grant-inversion fix, but the connector syncs them
// unconditionally, so they need to work for a full sync to complete.
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/groups/groups/#list-groups
func (srv *server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size, _ := strconv.Atoi(q.Get("page[size]"))
	page, hasMore, next := cbpPage(srv.state.ListGroups(), size, q.Get("page[after]"))

	groups := make([]map[string]any, 0, len(page))
	for _, g := range page {
		groups = append(groups, map[string]any{"id": g.ID, keyName: g.Name})
	}
	writeJSON(w, map[string]any{
		"groups": groups,
		keyMeta:  cbpMeta(hasMore, next),
	})
}

// handleListGroupMemberships backs group.Grants (GetGroupMemberships, CBP
// only — this server does not implement the group-provisioning endpoints,
// out of scope for the org/team_member fix this server validates).
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships
func (srv *server) handleListGroupMemberships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var groupID int64
	if v := q.Get("group_id"); v != "" {
		groupID, _ = strconv.ParseInt(v, 10, 64)
	}
	size, _ := strconv.Atoi(q.Get("page[size]"))
	page, hasMore, next := cbpPage(srv.state.ListGroupMembershipsByGroup(groupID), size, q.Get("page[after]"))

	memberships := make([]map[string]any, 0, len(page))
	for _, m := range page {
		memberships = append(memberships, map[string]any{
			"id":       m.ID,
			"user_id":  m.UserID,
			"group_id": m.GroupID,
		})
	}
	writeJSON(w, map[string]any{
		"group_memberships": memberships,
		keyMeta:             cbpMeta(hasMore, next),
	})
}
