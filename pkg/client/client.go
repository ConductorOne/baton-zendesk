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
func (c *ZendeskClient) ListUsers(ctx context.Context, pageToken int) ([]zendesk.User, string, error) {
	var nextPageToken string
	users, page, err := c.client.GetUsers(ctx, &zendesk.UserListOptions{
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
func (c *ZendeskClient) ListGroups(ctx context.Context, pageToken int) ([]zendesk.Group, string, error) {
	var nextPageToken string
	groups, page, err := c.client.GetGroups(ctx, &zendesk.GroupListOptions{
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
func (c *ZendeskClient) ListOrganizations(ctx context.Context, opts *zendesk.OrganizationListOptions) ([]zendesk.Organization, string, error) {
	var nextPageToken string
	orgs, page, err := c.client.GetOrganizations(ctx, &zendesk.OrganizationListOptions{})
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
func (c *ZendeskClient) GetGroupMemberships(ctx context.Context, groupId int64) ([]zendesk.GroupMembership, string, error) {
	var nextPageToken string
	groupMemberships, page, err := c.client.GetGroupMemberships(ctx, &zendesk.GroupMembershipListOptions{
		GroupID: groupId,
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
func (c *ZendeskClient) GetUser(ctx context.Context, userID int64) (zendesk.User, error) {
	user, err := c.client.GetUser(ctx, userID)
	if err != nil {
		return zendesk.User{}, err
	}

	return user, err
}

// GetGroupDetails get an existing group.
func (c *ZendeskClient) GetGroupDetails(ctx context.Context, groupID int64) (zendesk.Group, error) {
	group, err := c.client.GetGroup(ctx, groupID)
	if err != nil {
		return zendesk.Group{}, err
	}

	return group, err
}

// GetOrgName get an existing organization name.
func (c *ZendeskClient) GetOrgName(ctx context.Context, orgID *v2.ResourceId) (string, error) {
	oID, err := strconv.ParseInt(orgID.Resource, 10, 64)
	if err != nil {
		return "", err
	}

	org, err := c.client.GetOrganization(ctx, oID)
	if err != nil {
		return "", err
	}

	return org.Name, nil
}

// GetOrganizationUsers fetch organization users list.
func (c *ZendeskClient) GetOrganizationUsers(ctx context.Context, orgID *v2.ResourceId, opts *zendesk.UserListOptions) ([]zendesk.User, string, error) {
	var nextPageToken string
	oID, err := strconv.ParseInt(orgID.Resource, 10, 64)
	if err != nil {
		return nil, "", err
	}

	users, page, err := c.client.GetOrganizationUsers(ctx, oID, nil)

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
func (c *ZendeskClient) GetOrganizationMemberships(ctx context.Context, opts *zendesk.OrganizationMembershipListOptions) ([]zendesk.OrganizationMembership, zendesk.Page, error) {
	orgMemberships, _, err := c.client.GetOrganizationMemberships(ctx, &zendesk.OrganizationMembershipListOptions{
		PageOptions:    zendesk.PageOptions{},
		OrganizationID: opts.OrganizationID,
	})
	if err != nil {
		return nil, zendesk.Page{}, err
	}

	return orgMemberships, zendesk.Page{}, nil
}

// GetRole get an existing user role.
func (c *ZendeskClient) GetRole(ctx context.Context, membership zendesk.OrganizationMembership) (string, zendesk.Page, error) {
	users, nextPage, err := c.client.GetOrganizationUsers(ctx, membership.OrganizationID, &zendesk.UserListOptions{})
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

// GetCustomRoles fetch CustomRoles list.
func (c *ZendeskClient) GetCustomRoles(ctx context.Context) ([]zendesk.CustomRole, error) {
	customRole, err := c.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("zendesk-connector: failed to fetch customroles: %w", err)
	}

	return customRole, nil
}

// GetUserResource gets a new connector resource for a Zenddesk group.
func (c *ZendeskClient) GetUserResource(user zendesk.User, resourceTypeUser *v2.ResourceType) (*v2.Resource, error) {
	resource, err := rs.NewUserResource(user.Name, resourceTypeUser, user.ID, nil)
	if err != nil {
		return nil, err
	}

	return resource, nil
}

// GetUserAccountResource create a new connector resource for a Jamf user account.
func (c *ZendeskClient) GetUserAccountResource(account *zendesk.User, resourceTypeUser *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
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

// GetGroupResource gets a new connector resource for a Zenddesk group.
func (c *ZendeskClient) GetGroupResource(group zendesk.Group, resourceTypeGroup *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_id":   group.ID,
		"group_name": group.Name,
	}
	groupTraitOptions := []rs.GroupTraitOption{rs.WithGroupProfile(profile)}
	ret, err := rs.NewGroupResource(
		group.Name,
		resourceTypeGroup,
		group.ID,
		groupTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// GetRoleResource create a new connector resource for a Zendesk role.
func (c *ZendeskClient) GetRoleResource(ctx context.Context, resourceTypeRole *v2.ResourceType, role string, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"role_id":   role,
		"role_name": role,
	}

	roleTraitOptions := []rs.RoleTraitOption{
		rs.WithRoleProfile(profile),
	}

	ret, err := rs.NewRoleResource(
		role,
		resourceTypeRole,
		role,
		roleTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// teamResource creates a new connector resource for a GitHub Team. It is possible that the team has a parent resource.
func (c *ZendeskClient) GetTeamResource(team *zendesk.User, resourceTypeTeam *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		// // Store the org ID in the profile so that we can reference it when calculating grants
		// "orgID": team.GetOrganization().GetID(),
		"user_id":   team.ID,
		"user_name": team.Name,
	}

	ret, err := rs.NewGroupResource(
		team.Name,
		resourceTypeTeam,
		team.ID,
		[]rs.GroupTraitOption{rs.WithGroupProfile(profile)},
		rs.WithAnnotation(
			&v2.V1Identifier{Id: fmt.Sprintf("team:%d", team.ID)},
		),
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (c *ZendeskClient) CreateGroupMemberchip(ctx context.Context, groupMemberships zendesk.GroupMembership) (zendesk.GroupMembership, error) {
	var data, result struct {
		GroupMemberships zendesk.GroupMembership `json:"group_membership"`
	}

	data.GroupMemberships = groupMemberships
	body, err := c.client.Post(ctx, "/group_memberships.json", data)
	if err != nil {
		return zendesk.GroupMembership{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return zendesk.GroupMembership{}, err
	}

	return result.GroupMemberships, nil
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
