package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/nukosuke/go-zendesk/zendesk"
)

const teamMembersRoleAdmin = "admin"
const teamMembersRoleAgent = "agent"

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
func (z *ZendeskClient) ListUsers(ctx context.Context, pageToken int) ([]zendesk.User, string, error) {
	var nextPageToken string
	users, page, err := z.client.GetUsers(ctx, &zendesk.UserListOptions{
		Roles: []string{teamMembersRoleAdmin, teamMembersRoleAgent}, // exclude end-users
		PageOptions: zendesk.PageOptions{
			Page: pageToken,
		},
	})
	if err != nil {
		return nil, "", err
	}

	if page.NextPage != nil {
		nextPageToken, err = parseNextPage(*page.NextPage)
		if err != nil {
			return nil, "", err
		}
	}

	return users, nextPageToken, err
}

// ListGroups returns all ZendeskClient user groups.
func (z *ZendeskClient) ListGroups(ctx context.Context, pageToken int) ([]zendesk.Group, string, error) {
	var nextPageToken string
	groups, page, err := z.client.GetGroups(ctx, &zendesk.GroupListOptions{
		PageOptions: zendesk.PageOptions{
			Page: pageToken,
		},
	})
	if err != nil {
		return nil, "", err
	}

	if page.NextPage != nil {
		nextPageToken, err = parseNextPage(*page.NextPage)
		if err != nil {
			return nil, "", err
		}
	}

	return groups, nextPageToken, err
}

// ListOrganizations fetch organization list.
func (z *ZendeskClient) ListOrganizations(ctx context.Context, opts *zendesk.OrganizationListOptions) ([]zendesk.Organization, string, error) {
	var nextPageToken string
	orgs, page, err := z.client.GetOrganizations(ctx, opts)
	if err != nil {
		return nil, "", fmt.Errorf("zendesk-connector: failed to fetch org: %w", err)
	}

	if page.NextPage != nil {
		nextPageToken, err = parseNextPage(*page.NextPage)
		if err != nil {
			return nil, "", err
		}
	}

	return orgs, nextPageToken, err
}

// GetGroupMemberships get the memberships of the specified group.
func (z *ZendeskClient) GetGroupMemberships(ctx context.Context, groupId int64, pageToken string) ([]zendesk.GroupMembership, string, error) {
	var nextPageToken string

	pageOptions, err := parsePageOptions(pageToken)
	if err != nil {
		return nil, "", err
	}

	groupMemberships, page, err := z.client.GetGroupMemberships(ctx, &zendesk.GroupMembershipListOptions{
		GroupID:     groupId,
		PageOptions: pageOptions,
	})
	if err != nil {
		return nil, "", err
	}

	if page.NextPage != nil {
		nextPageToken, err = parseNextPage(*page.NextPage)
		if err != nil {
			return nil, "", err
		}
	}

	return groupMemberships, nextPageToken, err
}

// GetUser get an existing user.
func (z *ZendeskClient) GetUser(ctx context.Context, userID int64) (zendesk.User, error) {
	user, err := z.client.GetUser(ctx, userID)
	if err != nil {
		return zendesk.User{}, err
	}

	return user, err
}

// GetUsers gets users based on roles.
func (z *ZendeskClient) GetUsers(ctx context.Context, opts *zendesk.UserListOptions) (map[int64]zendesk.User, string, error) {
	var (
		mapUsers      = make(map[int64]zendesk.User)
		nextPageToken string
	)
	users, page, err := z.client.GetUsers(ctx, opts)
	if err != nil {
		return nil, "", err
	}

	if page.NextPage != nil {
		nextPageToken, err = parseNextPage(*page.NextPage)
		if err != nil {
			return nil, "", err
		}
	}

	for _, user := range users {
		mapUsers[user.ID] = user
	}

	return mapUsers, nextPageToken, err
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
func (z *ZendeskClient) GetOrganizationUsers(ctx context.Context, orgID *v2.ResourceId, opts *zendesk.UserListOptions) ([]zendesk.User, string, error) {
	var nextPageToken string
	oID, err := strconv.ParseInt(orgID.Resource, 10, 64)
	if err != nil {
		return nil, "", err
	}

	users, page, err := z.client.GetOrganizationUsers(ctx, oID, opts)

	if err != nil {
		return nil, "", err
	}

	if page.NextPage != nil {
		nextPageToken, err = parseNextPage(*page.NextPage)
		if err != nil {
			return nil, "", err
		}
	}

	return users, nextPageToken, nil
}

// GetOrganizationMemberships fetch organization memberships.
func (z *ZendeskClient) GetOrganizationMemberships(ctx context.Context, opts *zendesk.OrganizationMembershipListOptions) ([]zendesk.OrganizationMembership, zendesk.Page, error) {
	orgMemberships, _, err := z.client.GetOrganizationMemberships(ctx, opts)
	if err != nil {
		return nil, zendesk.Page{}, err
	}

	return orgMemberships, zendesk.Page{}, nil
}

// GetRole get an existing user role.
func (z *ZendeskClient) GetRole(ctx context.Context, membership zendesk.OrganizationMembership) (string, zendesk.Page, error) {
	users, nextPage, err := z.client.GetOrganizationUsers(ctx, membership.OrganizationID, &zendesk.UserListOptions{})
	if err != nil {
		return "", zendesk.Page{}, fmt.Errorf("zendesk-connector: failed to fetch role: %w", err)
	}
	for _, user := range users {
		if user.ID == membership.UserID {
			return user.Role, nextPage, nil
		}
	}

	return "", zendesk.Page{}, err
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

// CreateCustomRoleMembership Assigns an agent to a given group.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/account-configuration/custom_roles/#list-custom-roles
func (z *ZendeskClient) CreateCustomRoleMembership(ctx context.Context, roleMemberships zendesk.CustomRole) (zendesk.CustomRole, error) {
	var data, result struct {
		CustomRoles zendesk.CustomRole `json:"custom_role"`
	}

	data.CustomRoles = roleMemberships
	body, err := z.client.Post(ctx, "/custom_roles.json", data)
	if err != nil {
		return zendesk.CustomRole{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.CustomRole{}, err
	}

	return result.CustomRoles, nil
}

// GetGroupMembershipByGroup gets an existing group membership.
func (z *ZendeskClient) GetGroupMembershipByGroup(ctx context.Context, groupMemberships zendesk.GroupMembership) (string, zendesk.Page, error) {
	groups, nextPage, err := z.client.GetGroupMemberships(ctx, &zendesk.GroupMembershipListOptions{
		UserID:  groupMemberships.UserID,
		GroupID: groupMemberships.GroupID,
	})
	if err != nil {
		return "", zendesk.Page{}, fmt.Errorf("zendesk-connector: failed to fetch groupmembership: %w", err)
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
		return "", zendesk.Page{}, fmt.Errorf("zendesk-connector: failed to fetch organizationmemberships: %w", err)
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

func parseNextPage(u string) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	nextPageToken := q.Get("page")
	if nextPageToken == "" {
		return "", errors.New("invalid page token")
	}
	return nextPageToken, nil
}

func parsePageOptions(pageToken string) (zendesk.PageOptions, error) {
	if pageToken == "" {
		return zendesk.PageOptions{}, nil
	}

	pageInt, err := strconv.Atoi(pageToken)
	if err != nil {
		return zendesk.PageOptions{}, fmt.Errorf("failed to parse page token: %w", err)
	}

	return zendesk.PageOptions{
		Page: pageInt,
	}, nil
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
		return nil, fmt.Errorf("zendesk-connector: failed to fetch customroles: %w", err)
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

// UpdateUser updates an existing user (by queueing a job).
//
// Allowed for: Admins or agents with permission to edit end-user profiles
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#update-user
func (z *ZendeskClient) UpdateUser(ctx context.Context, userID int64, user zendesk.User) (zendesk.User, error) {
	return z.client.UpdateUser(ctx, userID, user)
}

// DeleteUser soft deletes a user.
//
// Allowed for: Admins or agents with access to all tickets
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#delete-user
func (z *ZendeskClient) DeleteUser(ctx context.Context, userID int64) error {
	err := z.client.Delete(ctx, fmt.Sprintf("/users/%d.json", userID))
	if err != nil {
		if zErr, ok := err.(zendesk.Error); ok {
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
	err := z.client.Delete(ctx, fmt.Sprintf("/deleted_users/%d.json", userID))
	if err != nil {
		if zErr, ok := err.(zendesk.Error); ok {
			if zErr.Status() == http.StatusOK {
				return nil
			}
		}
		return err
	}
	return nil
}

// GetZendeskClient returns the underlying zendesk client.
func (z *ZendeskClient) GetZendeskClient() *zendesk.Client {
	return z.client
}
