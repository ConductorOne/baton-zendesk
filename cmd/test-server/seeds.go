package main

// seed populates s with a scenario sized to exercise the org/team_member
// grant-inversion fix (CXH-1955/CXH-29):
//   - an org with zero team members (org 5 — Wayne Enterprises)
//   - a team member in three orgs (101 — Alice, admin)
//   - a team member in four orgs (103 — Carol, agent)
//   - a team member with no org memberships at all (104 — Dave, admin) —
//     exercises the empty-grants path
//   - an org with two distinct team members (org 1 — Acme, gets both an
//     admin and an agent grant)
//
// See cmd/test-server/README.md for the full expected-grants table this
// seed is designed to produce.
func seed(s *State) {
	orgs := []*Organization{
		{ID: 1, Name: "Acme Corp", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/1.json"},
		{ID: 2, Name: "Globex", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/2.json"},
		{ID: 3, Name: "Initech", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/3.json"},
		{ID: 4, Name: "Umbrella Corp", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/4.json"},
		{ID: 5, Name: "Wayne Enterprises", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/5.json"}, // zero team members
		{ID: 6, Name: "Stark Industries", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/6.json"},
		{ID: 7, Name: "Wonka Inc", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/7.json"},
		{ID: 8, Name: "Hooli", URL: "https://d3v-baton.zendesk.com/api/v2/organizations/8.json"},
	}
	for _, o := range orgs {
		s.addOrg(o)
	}

	users := []*TeamMember{
		{ID: 101, Name: "Alice Admin", Email: "alice@example.com", Role: "admin"},
		{ID: 102, Name: "Bob Agent", Email: "bob@example.com", Role: "agent"},
		{ID: 103, Name: "Carol Agent", Email: "carol@example.com", Role: "agent"},
		{ID: 104, Name: "Dave Admin", Email: "dave@example.com", Role: "admin"}, // no memberships
	}
	for _, u := range users {
		s.addUser(u)
	}

	// user_id, organization_id pairs. IDs are assigned sequentially starting
	// at 1001 (see NewState's nextMembershipID).
	type pair struct{ userID, orgID int64 }
	memberships := []pair{
		{101, 1}, // Alice: org 1 (admin)
		{101, 2}, // Alice: org 2 (admin)
		{101, 3}, // Alice: org 3 (admin)
		{102, 1}, // Bob: org 1 (agent) — org 1 gets both an admin and an agent grant
		{103, 4}, // Carol: org 4 (agent)
		{103, 6}, // Carol: org 6 (agent)
		{103, 7}, // Carol: org 7 (agent)
		{103, 8}, // Carol: org 8 (agent)
	}
	for i, p := range memberships {
		s.addMembership(&OrgMembership{ID: int64(1001 + i), UserID: p.userID, OrganizationID: p.orgID})
	}

	groups := []*Group{
		{ID: 201, Name: "Support"},
		{ID: 202, Name: "Engineering"},
	}
	for _, g := range groups {
		s.addGroup(g)
	}
	s.addGroupMembership(&GroupMembership{ID: 301, UserID: 102, GroupID: 201})
	s.addGroupMembership(&GroupMembership{ID: 302, UserID: 103, GroupID: 201})
	s.addGroupMembership(&GroupMembership{ID: 303, UserID: 101, GroupID: 202})
}
