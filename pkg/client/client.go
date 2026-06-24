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

// cbpPageSize must be set on every CBP request — Zendesk treats requests
// without page[size] as offset pagination on endpoints that support both,
// which leaves meta.has_more/after_cursor empty and stops sync after page 1.
const cbpPageSize = 100

// Zendesk API endpoint paths for direct API calls.
const (
	// https://developer.zendesk.com/api-reference/ticketing/users/users/
	// Permissions: Admins or agents with permission to edit end-user profiles.
	pathUser = "/users/%d.json"

	// https://developer.zendesk.com/api-reference/ticketing/users/users/#permanently-delete-user
	// Permissions: Admins or agents with access to all tickets.
	pathDeletedUser = "/deleted_users/%d.json"

	// https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships-by-group-id
	pathGroupMemberships = "/groups/%d/memberships.json" // GET (list by group)

	// https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships
	pathUserGroupMemberships = "/users/%d/group_memberships.json"

	// https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#list-memberships
	pathUserOrganizationMemberships = "/users/%d/organization_memberships.json"
)

type ZendeskClient struct {
	client *zendesk.Client
}

func New(ctx context.Context, httpClient *http.Client, subdomain string, email string, apiToken string, baseURL string) (*ZendeskClient, error) {
	zc := &ZendeskClient{}
	client, err := zendesk.NewClient(httpClient)
	if err != nil {
		return nil, err
	}
	// Custom base URL takes precedence over subdomain-based URL
	if baseURL != "" {
		err = client.SetEndpointURL(baseURL)
		if err != nil {
			return nil, err
		}
	} else {
		err = client.SetSubdomain(subdomain)
		if err != nil {
			return nil, err
		}
	}
	client.SetCredential(zendesk.NewAPITokenCredential(email, apiToken))
	zc.client = client
	return zc, nil
}

// ListUsers returns all ZendeskClient users.
func (z *ZendeskClient) ListUsers(ctx context.Context, pageToken string) ([]zendesk.User, string, error) {
	users, meta, err := z.client.GetUsersCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageSize: cbpPageSize, PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{Roles: []string{teamMembersRoleAdmin, teamMembersRoleAgent}},
	})
	if err != nil {
		return nil, "", wrapZendeskError(err)
	}
	return users, getNextPageToken(meta), nil
}

// ListUsersByRole returns users assigned to a specific custom role, with cursor pagination.
func (z *ZendeskClient) ListUsersByRole(ctx context.Context, roleID int64, pageToken string) ([]zendesk.User, string, error) {
	users, meta, err := z.client.GetUsersCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageSize: cbpPageSize, PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{PermissionSet: roleID},
	})
	if err != nil {
		return nil, "", wrapZendeskError(err)
	}
	return users, getNextPageToken(meta), nil
}

// ListGroups returns all ZendeskClient user groups.
func (z *ZendeskClient) ListGroups(ctx context.Context, pageToken string) ([]zendesk.Group, string, error) {
	groups, meta, err := z.client.GetGroupsCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageSize: cbpPageSize, PageAfter: pageToken},
	})
	if err != nil {
		return nil, "", wrapZendeskError(err)
	}
	return groups, getNextPageToken(meta), nil
}

// ListOrganizations fetch organization list.
func (z *ZendeskClient) ListOrganizations(ctx context.Context, pageToken string) ([]zendesk.Organization, string, error) {
	orgs, meta, err := z.client.GetOrganizationsCBP(ctx, &zendesk.CBPOptions{
		CursorPagination: zendesk.CursorPagination{PageSize: cbpPageSize, PageAfter: pageToken},
	})
	if err != nil {
		return nil, "", wrapZendeskError(err)
	}
	return orgs, getNextPageToken(meta), nil
}

// GetGroupMemberships lists memberships for a single group.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships-by-group-id
func (z *ZendeskClient) GetGroupMemberships(ctx context.Context, groupId int64, pageToken string) ([]zendesk.GroupMembership, string, error) {
	query := url.Values{}
	query.Set("page[size]", strconv.Itoa(cbpPageSize))
	if pageToken != "" {
		query.Set("page[after]", pageToken)
	}

	path := fmt.Sprintf(pathGroupMemberships, groupId)
	body, err := z.client.Get(ctx, path+"?"+query.Encode())
	if err != nil {
		return nil, "", wrapZendeskError(err)
	}

	var result struct {
		GroupMemberships []zendesk.GroupMembership    `json:"group_memberships"`
		Meta             zendesk.CursorPaginationMeta `json:"meta"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", err
	}

	return result.GroupMemberships, getNextPageToken(result.Meta), nil
}

// GetUser get an existing user.
func (z *ZendeskClient) GetUser(ctx context.Context, userID int64) (zendesk.User, error) {
	user, err := z.client.GetUser(ctx, userID)
	if err != nil {
		return zendesk.User{}, wrapZendeskError(err)
	}
	return user, nil
}

// GetGroupDetails get an existing group.
func (z *ZendeskClient) GetGroupDetails(ctx context.Context, groupID int64) (zendesk.Group, error) {
	group, err := z.client.GetGroup(ctx, groupID)
	if err != nil {
		return zendesk.Group{}, wrapZendeskError(err)
	}
	return group, nil
}

// GetOrgName get an existing organization name.
func (z *ZendeskClient) GetOrgName(ctx context.Context, orgID *v2.ResourceId) (string, error) {
	oID, err := strconv.ParseInt(orgID.Resource, 10, 64)
	if err != nil {
		return "", err
	}

	org, err := z.client.GetOrganization(ctx, oID)
	if err != nil {
		return "", wrapZendeskError(err)
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
		CursorPagination: zendesk.CursorPagination{PageSize: cbpPageSize, PageAfter: pageToken},
		CommonOptions:    zendesk.CommonOptions{Id: oID, Roles: []string{teamMembersRoleAdmin, teamMembersRoleAgent}},
	})
	if err != nil {
		return nil, "", wrapZendeskError(err)
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
		return zendesk.GroupMembership{}, wrapZendeskError(err)
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.GroupMembership{}, err
	}

	return result.GroupMemberships, nil
}

// GetGroupMembershipByGroup gets the ID of the user's membership in the given group,
// or "" if the user is not a member of that group.
//
// It lists the user's memberships via /users/{user_id}/group_memberships and matches
// the group ID client-side. The flat /group_memberships.json endpoint ignores
// group_id when user_id is also set, so filtering there can return a membership
// in a different group and a revoke would delete the wrong one (CXH-1734).
func (z *ZendeskClient) GetGroupMembershipByGroup(ctx context.Context, groupMembership zendesk.GroupMembership) (string, error) {
	for cursor := ""; ; {
		var result struct {
			GroupMemberships []zendesk.GroupMembership    `json:"group_memberships"`
			Meta             zendesk.CursorPaginationMeta `json:"meta"`
		}

		query := url.Values{}
		query.Set("page[size]", strconv.Itoa(cbpPageSize))
		if cursor != "" {
			query.Set("page[after]", cursor)
		}
		path := fmt.Sprintf(pathUserGroupMemberships, groupMembership.UserID)
		body, err := z.client.Get(ctx, path+"?"+query.Encode())
		if err != nil {
			return "", wrapZendeskError(err)
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", err
		}

		for _, membership := range result.GroupMemberships {
			if membership.GroupID == groupMembership.GroupID {
				return fmt.Sprintf("%d", membership.ID), nil
			}
		}

		cursor = getNextPageToken(result.Meta)
		if cursor == "" {
			return "", nil
		}
	}
}

// GetOrganizationMembershipByUser gets the ID of the user's membership in the given
// organization, or "" if the user is not a member of that organization.
//
// It lists the user's memberships via /users/{user_id}/organization_memberships and
// matches the organization ID client-side, for the same reason as
// GetGroupMembershipByGroup: the flat endpoint ignores filter query params.
func (z *ZendeskClient) GetOrganizationMembershipByUser(ctx context.Context, organizationMembership zendesk.OrganizationMembershipListOptions) (string, error) {
	for cursor := ""; ; {
		var result struct {
			OrganizationMemberships []zendesk.OrganizationMembership `json:"organization_memberships"`
			Meta                    zendesk.CursorPaginationMeta     `json:"meta"`
		}

		query := url.Values{}
		query.Set("page[size]", strconv.Itoa(cbpPageSize))
		if cursor != "" {
			query.Set("page[after]", cursor)
		}
		path := fmt.Sprintf(pathUserOrganizationMemberships, organizationMembership.UserID)
		body, err := z.client.Get(ctx, path+"?"+query.Encode())
		if err != nil {
			return "", wrapZendeskError(err)
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", err
		}

		for _, membership := range result.OrganizationMemberships {
			if membership.OrganizationID == organizationMembership.OrganizationID {
				return fmt.Sprintf("%d", membership.ID), nil
			}
		}

		cursor = getNextPageToken(result.Meta)
		if cursor == "" {
			return "", nil
		}
	}
}

// RemoveGroupMembershipByID removes a user from a group, given a specified
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships
func (z *ZendeskClient) RemoveGroupMembershipByID(ctx context.Context, groupMemberships zendesk.GroupMembership) (string, error) {
	groupMembershipID, err := z.GetGroupMembershipByGroup(ctx, groupMemberships)
	if err != nil {
		return "", err
	}
	if groupMembershipID == "" {
		return "", ErrMembershipNotFound
	}

	err = z.client.Delete(ctx, fmt.Sprintf("/group_memberships/%s", groupMembershipID))
	if err != nil {
		return "", wrapZendeskError(err)
	}

	return groupMembershipID, nil
}

// RemoveOrganizationMembershipByID removes a user from an organization, given a specified
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#list-memberships
func (z *ZendeskClient) RemoveOrganizationMembershipByID(ctx context.Context, organizationMemberships zendesk.OrganizationMembershipListOptions) (string, error) {
	organizationMembershipID, err := z.GetOrganizationMembershipByUser(ctx, organizationMemberships)
	if err != nil {
		return "", err
	}
	if organizationMembershipID == "" {
		return "", ErrMembershipNotFound
	}

	err = z.client.Delete(ctx, fmt.Sprintf("/organization_memberships/%s", organizationMembershipID))
	if err != nil {
		return "", wrapZendeskError(err)
	}

	return organizationMembershipID, nil
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
		return zendesk.OrganizationMembership{}, wrapZendeskError(err)
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.OrganizationMembership{}, err
	}

	return result.OrganizationMembership, nil
}

// GetCustomRoles fetch CustomRoles list.
func (z *ZendeskClient) GetCustomRoles(ctx context.Context) ([]zendesk.CustomRole, error) {
	customRole, err := z.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, wrapZendeskError(err)
	}

	return customRole, nil
}

// CreateUser creates a new user.
//
// Allowed for: Admins or agents with permission to manage team members
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#create-user
func (z *ZendeskClient) CreateUser(ctx context.Context, user zendesk.User) (zendesk.User, error) {
	created, err := z.client.CreateUser(ctx, user)
	if err != nil {
		return zendesk.User{}, wrapZendeskError(err)
	}
	return created, nil
}

// UpdateUser updates a user via direct HTTP PUT request with raw data.
//
// This function allows updating users with arbitrary fields via direct API call.
func (z *ZendeskClient) UpdateUser(ctx context.Context, userID int64, data map[string]any) (zendesk.User, error) {
	body, err := z.client.Put(ctx, fmt.Sprintf(pathUser, userID), data)
	if err != nil {
		return zendesk.User{}, wrapZendeskError(err)
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
		var zErr zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			if zErr.Status() == http.StatusOK {
				return nil
			}
		}
		return wrapZendeskError(err)
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
		var zErr zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			if zErr.Status() == http.StatusOK {
				return nil
			}
		}
		return wrapZendeskError(err)
	}
	return nil
}

func (z *ZendeskClient) GetZendeskClient() *zendesk.Client {
	return z.client
}
