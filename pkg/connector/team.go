package connector

import (
	"context"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"

	"github.com/conductorone/baton-zendesk/pkg/client"
)

type teamResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	connector    *Connector
}

func (t *teamResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return t.resourceType
}

func (t *teamResourceType) Entitlements(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (t *teamResourceType) List(ctx context.Context, parentID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var (
		err error
		ret []*v2.Resource
	)

	users, err := t.connector.cacheUsers(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, user := range users {
		userCopy := user
		res, err := getTeamResource(&userCopy, resourceTypeTeam)
		if err != nil {
			return nil, "", nil, err
		}

		ret = append(ret, res)
	}

	return ret, "", nil, nil
}

// Grants always returns an empty slice for teams since they don't have any entitlements.
func (o *teamResourceType) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func teamBuilder(cli *client.ZendeskClient, con *Connector) *teamResourceType {
	return &teamResourceType{
		resourceType: resourceTypeTeam,
		client:       cli,
		connector:    con,
	}
}
