package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleOrgMemberships dispatches GET (dual pagination mode) and POST
// (create) on the same path, matching the real API's shape: one resource
// path serving both cursor-based listing (team_member.Grants, via
// GetUserOrganizationMemberships) and offset-based listing
// (GetOrganizationMembershipByUser, the revoke-lookup path), distinguished
// by which pagination params are present — page[size]/page[after] selects
// CBP, anything else (including no pagination params) selects OBP.
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#list-memberships
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#create-membership
func (srv *server) handleOrgMemberships(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		srv.handleListOrgMemberships(w, r)
	case http.MethodPost:
		srv.handleCreateOrgMembership(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (srv *server) handleListOrgMemberships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var userIDFilter, orgIDFilter int64
	if v := q.Get("user_id"); v != "" {
		userIDFilter, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("organization_id"); v != "" {
		orgIDFilter, _ = strconv.ParseInt(v, 10, 64)
	}

	if q.Has("page[size]") || q.Has("page[after]") {
		// CBP mode: team_member.Grants always sets user_id.
		all := srv.state.ListMembershipsByUser(userIDFilter)
		size, _ := strconv.Atoi(q.Get("page[size]"))
		page, hasMore, next := cbpPage(all, size, q.Get("page[after]"))
		writeJSON(w, map[string]any{
			"organization_memberships": membershipsJSON(srv, page),
			keyMeta:                    cbpMeta(hasMore, next),
		})
		return
	}

	// OBP mode: GetOrganizationMembershipByUser filters by user_id and/or
	// organization_id and expects the full (unpaginated for our seed size)
	// result with next_page absent/null.
	var matches []*OrgMembership
	for _, m := range srv.state.ListMembershipsByUser(userIDFilter) {
		if orgIDFilter != 0 && m.OrganizationID != orgIDFilter {
			continue
		}
		matches = append(matches, m)
	}
	writeJSON(w, map[string]any{
		"organization_memberships": membershipsJSON(srv, matches),
		"next_page":                nil,
		"previous_page":            nil,
		"count":                    len(matches),
	})
}

func (srv *server) handleCreateOrgMembership(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrganizationMembership struct {
			UserID         int64 `json:"user_id"`
			OrganizationID int64 `json:"organization_id"`
		} `json:"organization_membership"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "BadRequest", keyDescription: err.Error()})
		return
	}

	m, alreadyExists := srv.state.CreateMembership(body.OrganizationMembership.UserID, body.OrganizationMembership.OrganizationID)
	if alreadyExists {
		// Matches the shape isAlreadyExistsError (pkg/connector/helpers.go)
		// looks for: HTTP 422 with "DuplicateValue" in the body.
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]any{
			keyError:       "RecordInvalid",
			keyDescription: "Record validation errors",
			"details": map[string]any{
				"base": []map[string]any{
					{keyDescription: "Membership already exists and has already been taken", keyError: "DuplicateValue"},
				},
			},
		})
		return
	}

	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"organization_membership": membershipJSON(srv, m),
	})
}

// handleDeleteOrgMembership backs RemoveOrganizationMembershipByID's DELETE
// call. Note the real path has no .json suffix (see pkg/client/client.go).
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#delete-membership
func (srv *server) handleDeleteOrgMembership(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "InvalidId"})
		return
	}
	if !srv.state.DeleteMembership(id) {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{
			keyError:       "RecordNotFound",
			keyDescription: "Not found",
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func membershipsJSON(srv *server, ms []*OrgMembership) []map[string]any {
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, membershipJSON(srv, m))
	}
	return out
}

// membershipJSON includes organization_name — team_member.Grants' allow-list
// filter (orgInScope) matches on this field, so it must be populated the same
// way the real API populates it (a passthrough of the org's own name).
func membershipJSON(srv *server, m *OrgMembership) map[string]any {
	orgName := ""
	for _, o := range srv.state.ListOrganizations() {
		if o.ID == m.OrganizationID {
			orgName = o.Name
			break
		}
	}
	return map[string]any{
		"id":                m.ID,
		"user_id":           m.UserID,
		"organization_id":   m.OrganizationID,
		"organization_name": orgName,
	}
}
