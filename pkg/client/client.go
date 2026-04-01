package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/nukosuke/go-zendesk/zendesk"
)

const teamMembersRoleAdmin = "admin"
const teamMembersRoleAgent = "agent"

// Zendesk API endpoint paths for direct API calls.
const (
	// https://developer.zendesk.com/api-reference/ticketing/users/users/
	// Permissions: Admins or agents with permission to edit end-user profiles.
	pathUser = "/users/%d.json"

	// https://developer.zendesk.com/api-reference/ticketing/users/users/#permanently-delete-user
	// Permissions: Admins or agents with access to all tickets.
	pathDeletedUser = "/deleted_users/%d.json"
)

type ZendeskClient struct {
	client *zendesk.Client
}

func New(ctx context.Context, httpClient *http.Client, subdomain string, email string, apiToken string) (*ZendeskClient, error) {
	zc := &ZendeskClient{}
	client, err := zendesk.NewClient(httpClient)
	if err != nil {
		return nil, err
	}
	err = client.SetSubdomain(subdomain)
	if err != nil {
		return nil, err
	}
	client.SetCredential(zendesk.NewAPITokenCredential(email, apiToken))
	zc.client = client
	return zc, nil
}

// ListUsers returns all ZendeskClient users.
func (z *ZendeskClient) ListUsers(ctx context.Context, pageToken string) ([]zendesk.User, string, error) {
	users, meta, err := z.client.GetUsersCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{Roles: []string{teamMembersRoleAdmin, teamMembersRoleAgent}},
	})
	if err != nil {
		return nil, "", err
	}
	return users, getNextPageToken(meta), nil
}

// ListUsersByRole returns users assigned to a specific custom role, with cursor pagination.
func (z *ZendeskClient) ListUsersByRole(ctx context.Context, roleID int64, pageToken string) ([]zendesk.User, string, error) {
	users, meta, err := z.client.GetUsersCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{PermissionSet: roleID},
	})
	if err != nil {
		return nil, "", err
	}
	return users, getNextPageToken(meta), nil
}

// ListGroups returns all ZendeskClient user groups.
func (z *ZendeskClient) ListGroups(ctx context.Context, pageToken string) ([]zendesk.Group, string, error) {
	groups, meta, err := z.client.GetGroupsCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageAfter: pageToken},
	})
	if err != nil {
		return nil, "", err
	}
	return groups, getNextPageToken(meta), nil
}

// ListOrganizations fetch organization list.
func (z *ZendeskClient) ListOrganizations(ctx context.Context, pageToken string) ([]zendesk.Organization, string, error) {
	orgs, meta, err := z.client.GetOrganizationsCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageAfter: pageToken},
	})
	if err != nil {
		return nil, "", err
	}
	return orgs, getNextPageToken(meta), nil
}

// GetGroupMemberships get the memberships of the specified group.
func (z *ZendeskClient) GetGroupMemberships(ctx context.Context, groupId int64, pageToken string) ([]zendesk.GroupMembership, string, error) {
	memberships, meta, err := z.client.GetGroupMembershipsCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{GroupID: groupId},
	})
	if err != nil {
		return nil, "", err
	}
	return memberships, getNextPageToken(meta), nil
}

// GetUser get an existing user.
func (z *ZendeskClient) GetUser(ctx context.Context, userID int64) (zendesk.User, error) {
	user, err := z.client.GetUser(ctx, userID)
	if err != nil {
		return zendesk.User{}, err
	}

	return user, err
}

// GetGroupDetails get an existing group.
func (z *ZendeskClient) GetGroupDetails(ctx context.Context, groupID int64) (zendesk.Group, error) {
	group, err := z.client.GetGroup(ctx, groupID)
	if err != nil {
		return zendesk.Group{}, err
	}

	return group, err
}

// GetOrgName get an existing organization name.
func (z *ZendeskClient) GetOrgName(ctx context.Context, orgID *v2.ResourceId) (string, error) {
	oID, err := strconv.ParseInt(orgID.Resource, 10, 64)
	if err != nil {
		return "", err
	}

	org, err := z.client.GetOrganization(ctx, oID)
	if err != nil {
		return "", err
	}

	return org.Name, nil
}

// GetOrganizationUsers fetch organization users list.
func (z *ZendeskClient) GetOrganizationUsers(ctx context.Context, orgID *v2.ResourceId, pageToken string) ([]zendesk.User, string, error) {
	oID, err := strconv.ParseInt(orgID.Resource, 10, 64)
	if err != nil {
		return nil, "", err
	}
	users, meta, err := z.client.GetOrganizationUsersCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{Id: oID, Roles: []string{teamMembersRoleAdmin, teamMembersRoleAgent}},
	})
	if err != nil {
		return nil, "", err
	}
	return users, getNextPageToken(meta), nil
}

// GetUserAccountResource creates a new connector resource for a Jamf user account.
func (z *ZendeskClient) GetUserAccountResource(account *zendesk.User, resourceTypeUser *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	var (
		firstName, lastName string
		userStatus          v2.UserTrait_Status_Status
	)
	names := strings.SplitN(account.Name, " ", 2)

	switch len(names) {
	case 1:
		firstName = names[0]
	case 2:
		firstName = names[0]
		lastName = names[1]
	}

	profile := map[string]interface{}{
		"user_id":    fmt.Sprintf("account:%d", account.ID),
		"first_name": firstName,
		"last_name":  lastName,
		"login":      account.Email,
	}
	if account.Active {
		userStatus = v2.UserTrait_Status_STATUS_ENABLED
	} else {
		userStatus = v2.UserTrait_Status_STATUS_DISABLED
	}

	userTraitOptions := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithEmail(account.Email, true),
		rs.WithStatus(userStatus),
	}

	ret, err := rs.NewUserResource(
		account.Name,
		resourceTypeUser,
		account.ID,
		userTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// CreateGroupMembership Assigns an agent to a given group.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships
func (z *ZendeskClient) CreateGroupMembership(ctx context.Context, groupMemberships zendesk.GroupMembership) (zendesk.GroupMembership, error) {
	var data, result struct {
		GroupMemberships zendesk.GroupMembership `json:"group_membership"`
	}

	data.GroupMemberships = groupMemberships
	body, err := z.client.Post(ctx, "/group_memberships.json", data)
	if err != nil {
		return zendesk.GroupMembership{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.GroupMembership{}, err
	}

	return result.GroupMemberships, nil
}

// GetGroupMembershipByGroup gets an existing group membership.
func (z *ZendeskClient) GetGroupMembershipByGroup(ctx context.Context, groupMemberships zendesk.GroupMembership) (string, zendesk.Page, error) {
	groups, nextPage, err := z.client.GetGroupMemberships(ctx, &zendesk.GroupMembershipListOptions{
		UserID:  groupMemberships.UserID,
		GroupID: groupMemberships.GroupID,
	})
	if err != nil {
		return "", zendesk.Page{}, err
	}

	for _, group := range groups {
		if groupMemberships.UserID == group.UserID {
			return fmt.Sprintf("%d", group.ID), nextPage, nil
		}
	}

	return "", zendesk.Page{}, err
}

// GetOrganizationMembershipByUser gets an existing organization membership.
func (z *ZendeskClient) GetOrganizationMembershipByUser(ctx context.Context, organizationMemberships zendesk.OrganizationMembershipListOptions) (string, zendesk.Page, error) {
	organizations, nextPage, err := z.client.GetOrganizationMemberships(ctx, &zendesk.OrganizationMembershipListOptions{
		UserID:         organizationMemberships.UserID,
		OrganizationID: organizationMemberships.OrganizationID,
	})
	if err != nil {
		return "", zendesk.Page{}, err
	}

	for _, organization := range organizations {
		if organizationMemberships.UserID == organization.UserID {
			return fmt.Sprintf("%d", organization.ID), nextPage, nil
		}
	}

	return "", zendesk.Page{}, err
}

// RemoveGroupMembershipByID removes a user from a group, given a specified
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships
func (z *ZendeskClient) RemoveGroupMembershipByID(ctx context.Context, groupMemberships zendesk.GroupMembership) (string, error) {
	groupMembershipID, _, err := z.GetGroupMembershipByGroup(ctx, groupMemberships)
	if err != nil {
		return "", err
	}

	err = z.client.Delete(ctx, fmt.Sprintf("/group_memberships/%s", groupMembershipID))
	if err != nil {
		return "", err
	}

	return groupMembershipID, err
}

// RemoveOrganizationMembershipByID removes a user from an organization, given a specified
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#list-memberships
func (z *ZendeskClient) RemoveOrganizationMembershipByID(ctx context.Context, organizationMemberships zendesk.OrganizationMembershipListOptions) (string, error) {
	organizationMembershipID, _, err := z.GetOrganizationMembershipByUser(ctx, organizationMemberships)
	if err != nil {
		return "", err
	}

	err = z.client.Delete(ctx, fmt.Sprintf("/organization_memberships/%s", organizationMembershipID))
	if err != nil {
		return "", err
	}

	return organizationMembershipID, err
}

// CreateOrganizationMembership creates an organization membership for an existing user and org
// https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#create-membership
func (z *ZendeskClient) CreateOrganizationMembership(ctx context.Context, opts zendesk.OrganizationMembership) (zendesk.OrganizationMembership, error) {
	var data, result struct {
		OrganizationMembership zendesk.OrganizationMembership `json:"organization_membership"`
	}

	data.OrganizationMembership = opts
	body, err := z.client.Post(ctx, "/organization_memberships.json", data)

	if err != nil {
		return zendesk.OrganizationMembership{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.OrganizationMembership{}, err
	}

	return result.OrganizationMembership, err
}

// GetCustomRoles fetch CustomRoles list.
func (z *ZendeskClient) GetCustomRoles(ctx context.Context) ([]zendesk.CustomRole, error) {
	customRole, err := z.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, err
	}

	return customRole, nil
}

// CreateUser creates a new user.
//
// Allowed for: Admins or agents with permission to manage team members
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#create-user
func (z *ZendeskClient) CreateUser(ctx context.Context, user zendesk.User) (zendesk.User, error) {
	return z.client.CreateUser(ctx, user)
}

// UpdateUser updates a user via direct HTTP PUT request with raw data.
//
// This function allows updating users with arbitrary fields via direct API call.
func (z *ZendeskClient) UpdateUser(ctx context.Context, userID int64, data map[string]any) (zendesk.User, error) {
	body, err := z.client.Put(ctx, fmt.Sprintf(pathUser, userID), data)
	if err != nil {
		return zendesk.User{}, err
	}

	var result struct {
		User zendesk.User `json:"user"`
	}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.User{}, err
	}
	return result.User, nil
}

// DeleteUser soft deletes a user.
//
// Allowed for: Admins or agents with permission to edit end-user profiles
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#delete-user
func (z *ZendeskClient) DeleteUser(ctx context.Context, userID int64) error {
	err := z.client.Delete(ctx, fmt.Sprintf(pathUser, userID))
	if err != nil {
		var zErr *zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			if zErr.Status() == http.StatusOK {
				return nil
			}
		}
		return err
	}
	return nil
}

// PermanentlyDeleteUser permanently deletes a soft deleted user.
//
// Allowed for: Admins or agents with access to all tickets
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#permanently-delete-user
func (z *ZendeskClient) PermanentlyDeleteUser(ctx context.Context, userID int64) error {
	err := z.client.Delete(ctx, fmt.Sprintf(pathDeletedUser, userID))
	if err != nil {
		var zErr *zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			if zErr.Status() == http.StatusOK {
				return nil
			}
		}
		return err
	}
	return nil
}

func (z *ZendeskClient) GetZendeskClient() *zendesk.Client {
	return z.client
}
