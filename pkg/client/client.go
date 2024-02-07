package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	_ "github.com/conductorone/baton-sdk/pkg/types/resource"
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

// GetGroupMemberships gets the memberships of the specified group.
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
