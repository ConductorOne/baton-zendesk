package main

import "sync"

// Organization mirrors the fields baton-zendesk's org.List reads off
// zendesk.Organization (id, name, url).
type Organization struct {
	ID   int64
	Name string
	URL  string
}

// TeamMember mirrors the fields baton-zendesk reads off zendesk.User for
// admin/agent accounts (team_member.List / team_member.Grants / GetUser).
type TeamMember struct {
	ID    int64
	Name  string
	Email string
	Role  string // "admin" or "agent" — end-users are out of scope for this server
}

// OrgMembership mirrors zendesk.OrganizationMembership.
type OrgMembership struct {
	ID             int64
	UserID         int64
	OrganizationID int64
}

// Group mirrors zendesk.Group.
type Group struct {
	ID   int64
	Name string
}

// GroupMembership mirrors zendesk.GroupMembership.
type GroupMembership struct {
	ID      int64
	UserID  int64
	GroupID int64
}

// TicketField mirrors the fields baton-zendesk reads off zendesk.TicketField.
type TicketField struct {
	ID        int64
	Type      string
	Title     string
	Active    bool
	Removable bool
	Required  bool
	Options   []map[string]any // custom_field_options entries: {"name","value"}.
}

// TicketForm mirrors zendesk.TicketForm.
type TicketForm struct {
	ID             int64
	Name           string
	Active         bool
	TicketFieldIDs []int64
}

// Ticket is a created mock ticket, echoed back by GET /tickets/{id}.
type Ticket struct {
	ID           int64
	Subject      string
	Description  string
	Status       string
	Priority     string
	Type         string
	RequesterID  int64
	TicketFormID int64
	Tags         []string
	CustomFields []map[string]any
}

// State is the in-memory backing store for every handler. All access goes
// through its methods so the lock scope is always one method call — see
// CLAUDE.md-style state container conventions.
type State struct {
	mu sync.Mutex

	orgs    map[int64]*Organization
	orgList []*Organization

	users    map[int64]*TeamMember
	userList []*TeamMember

	memberships      map[int64]*OrgMembership
	membershipList   []*OrgMembership
	nextMembershipID int64

	groups           map[int64]*Group
	groupList        []*Group
	groupMemberships []*GroupMembership

	ticketFields []*TicketField
	ticketForms  []*TicketForm
	tickets      map[int64]*Ticket
	nextTicketID int64

	// callCounts records every request path this server has served, keyed by
	// r.Method+" "+r.URL.Path (query string stripped). Used by /__debug/calls
	// to assert the old per-org GetOrganizationUsers endpoint
	// (/organizations/{id}/users.json) is never hit after the org.Grants ->
	// team_member.Grants inversion.
	callCounts map[string]int
}

func NewState() *State {
	s := &State{
		orgs:             make(map[int64]*Organization),
		users:            make(map[int64]*TeamMember),
		memberships:      make(map[int64]*OrgMembership),
		groups:           make(map[int64]*Group),
		nextMembershipID: 1001,
		tickets:          make(map[int64]*Ticket),
		nextTicketID:     9001,
		callCounts:       make(map[string]int),
	}
	seed(s)
	return s
}

// RecordCall increments the counter for method+path. Called from the logging
// middleware in main.go for every request, before auth/routing.
func (s *State) RecordCall(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCounts[key]++
}

func (s *State) CallCounts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]int, len(s.callCounts))
	for k, v := range s.callCounts {
		cp[k] = v
	}
	return cp
}

func (s *State) addOrg(o *Organization) {
	cp := *o
	s.orgs[o.ID] = &cp
	s.orgList = append(s.orgList, &cp)
}

func (s *State) ListOrganizations() []*Organization {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Organization, len(s.orgList))
	copy(out, s.orgList)
	return out
}

func (s *State) addUser(u *TeamMember) {
	cp := *u
	s.users[u.ID] = &cp
	s.userList = append(s.userList, &cp)
}

// ListUsersByRole returns every seeded team member with the given role, in
// insertion order. role is required — this server only seeds admin/agent
// accounts, matching team_member.List's two-pass admin+agent bag.
func (s *State) ListUsersByRole(role string) []*TeamMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*TeamMember
	for _, u := range s.userList {
		if u.Role == role {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out
}

func (s *State) GetUser(id int64) (*TeamMember, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return nil, false
	}
	cp := *u
	return &cp, true
}

func (s *State) addMembership(m *OrgMembership) {
	cp := *m
	s.memberships[m.ID] = &cp
	s.membershipList = append(s.membershipList, &cp)
	if m.ID >= s.nextMembershipID {
		s.nextMembershipID = m.ID + 1
	}
}

// ListMembershipsByUser returns every membership for userID, in insertion
// order — backs GetUserOrganizationMemberships (team_member.Grants).
func (s *State) ListMembershipsByUser(userID int64) []*OrgMembership {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*OrgMembership
	for _, m := range s.membershipList {
		if m.UserID == userID {
			cp := *m
			out = append(out, &cp)
		}
	}
	return out
}

// FindMembership returns the membership matching userID+organizationID, or
// ok=false if none exists — backs GetOrganizationMembershipByUser (the
// lookup RemoveOrganizationMembershipByID does before deleting by ID).
func (s *State) FindMembership(userID, organizationID int64) (*OrgMembership, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.membershipList {
		if m.UserID == userID && m.OrganizationID == organizationID {
			cp := *m
			return &cp, true
		}
	}
	return nil, false
}

// CreateMembership is idempotent-aware: it reports alreadyExists=true (and
// returns the existing row) rather than creating a duplicate, so the
// provisioning handler can reproduce Zendesk's 422 DuplicateValue response.
func (s *State) CreateMembership(userID, organizationID int64) (*OrgMembership, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.membershipList {
		if m.UserID == userID && m.OrganizationID == organizationID {
			cp := *m
			return &cp, true
		}
	}
	m := &OrgMembership{ID: s.nextMembershipID, UserID: userID, OrganizationID: organizationID}
	s.nextMembershipID++
	cp := *m
	s.memberships[m.ID] = &cp
	s.membershipList = append(s.membershipList, m)
	return m, false
}

// DeleteMembership removes a membership by ID. Returns false if it didn't
// exist, so the handler can reproduce Zendesk's 404 RecordNotFound response.
func (s *State) DeleteMembership(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberships[id]; !ok {
		return false
	}
	delete(s.memberships, id)
	for i, m := range s.membershipList {
		if m.ID == id {
			s.membershipList = append(s.membershipList[:i], s.membershipList[i+1:]...)
			break
		}
	}
	return true
}

func (s *State) addGroup(g *Group) {
	cp := *g
	s.groups[g.ID] = &cp
	s.groupList = append(s.groupList, &cp)
}

func (s *State) ListGroups() []*Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Group, len(s.groupList))
	copy(out, s.groupList)
	return out
}

func (s *State) addGroupMembership(m *GroupMembership) {
	cp := *m
	s.groupMemberships = append(s.groupMemberships, &cp)
}

func (s *State) ListGroupMembershipsByGroup(groupID int64) []*GroupMembership {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*GroupMembership
	for _, m := range s.groupMemberships {
		if m.GroupID == groupID {
			cp := *m
			out = append(out, &cp)
		}
	}
	return out
}

func (s *State) addTicketField(f *TicketField) {
	cp := *f
	s.ticketFields = append(s.ticketFields, &cp)
}

func (s *State) addTicketForm(f *TicketForm) {
	cp := *f
	s.ticketForms = append(s.ticketForms, &cp)
}

// Copy-on-read like the sibling accessors (ListOrganizations et al.) so
// callers never alias shared state.
func (s *State) ListTicketFields() []*TicketField {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TicketField, len(s.ticketFields))
	copy(out, s.ticketFields)
	return out
}

func (s *State) ListTicketForms() []*TicketForm {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TicketForm, len(s.ticketForms))
	copy(out, s.ticketForms)
	return out
}

func (s *State) CreateTicket(t *Ticket) *Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextTicketID
	s.nextTicketID++
	if t.Status == "" {
		t.Status = "new"
	}
	s.tickets[t.ID] = t
	return t
}

func (s *State) GetTicket(id int64) (*Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	return t, ok
}
