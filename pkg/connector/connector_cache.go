package connector

import (
	"context"
	"fmt"
	"strconv"

	"github.com/conductorone/baton-sdk/pkg/session"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
	"github.com/nukosuke/go-zendesk/zendesk"
)

var usersNamespace = sessions.WithPrefix("zendesk:users")

// populateCache stores a page of users into the session store, keyed by user ID.
// Called from team_member.List on each page so the cache is built incrementally during resource sync.
func (c *Connector) populateCache(ctx context.Context, ss sessions.SessionStore, users []zendesk.User) error {
	if ss == nil || len(users) == 0 {
		return nil
	}
	userMap := make(map[string]zendesk.User, len(users))
	for _, user := range users {
		userMap[fmt.Sprintf("%d", user.ID)] = user
	}
	return session.SetManyJSON(ctx, ss, userMap, usersNamespace)
}

// getCachedUsersByIDs fetches only the specified users from the session store.
func (c *Connector) getCachedUsersByIDs(ctx context.Context, ss sessions.SessionStore, userIDs []int64) (map[int64]zendesk.User, error) {
	if ss == nil {
		return nil, nil
	}
	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = fmt.Sprintf("%d", id)
	}
	cached, err := session.GetManyJSON[zendesk.User](ctx, ss, keys, usersNamespace)
	if err != nil {
		return nil, err
	}
	users := make(map[int64]zendesk.User, len(cached))
	for k, u := range cached {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid user ID key in cache %q: %w", k, err)
		}
		users[id] = u
	}
	return users, nil
}
