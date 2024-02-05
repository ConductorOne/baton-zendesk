package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"

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
