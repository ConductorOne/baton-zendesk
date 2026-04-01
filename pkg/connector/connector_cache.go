package connector

import (
	"context"
	"sort"
	"strconv"

	"github.com/conductorone/baton-sdk/pkg/session"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
	"github.com/nukosuke/go-zendesk/zendesk"
)

var usersNamespace = sessions.WithPrefix("zendesk:users")

func (c *Connector) cacheUsers(ctx context.Context, ss sessions.SessionStore) ([]zendesk.User, error) {
	if ss != nil {
		cached, err := session.GetAllJSON[zendesk.User](ctx, ss, usersNamespace)
		if err == nil && len(cached) > 0 {
			indices := make([]int, 0, len(cached))
			for k := range cached {
				i, err := strconv.Atoi(k)
				if err != nil {
					users := make([]zendesk.User, 0, len(cached))
					for _, u := range cached {
						users = append(users, u)
					}
					return users, nil
				}
				indices = append(indices, i)
			}
			sort.Ints(indices)
			users := make([]zendesk.User, 0, len(indices))
			for _, i := range indices {
				if u, ok := cached[strconv.Itoa(i)]; ok {
					users = append(users, u)
				}
			}
			return users, nil
		}
	}

	var usersToCache []zendesk.User
	pageToken := ""
	for {
		users, nextPageToken, err := c.zendeskClient.ListUsers(ctx, pageToken)
		if err != nil {
			return nil, err
		}
		usersToCache = append(usersToCache, users...)
		if nextPageToken == "" {
			break
		}
		pageToken = nextPageToken
	}

	if ss != nil && len(usersToCache) > 0 {
		userMap := make(map[string]zendesk.User, len(usersToCache))
		for i, user := range usersToCache {
			userMap[strconv.Itoa(i)] = user
		}
		if err := session.SetManyJSON(ctx, ss, userMap, usersNamespace); err != nil {
			return nil, err
		}
	}

	return usersToCache, nil
}
