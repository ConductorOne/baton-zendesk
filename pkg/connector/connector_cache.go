package connector

import (
	"context"
	"strconv"

	"github.com/conductorone/baton-sdk/pkg/session"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
	"github.com/nukosuke/go-zendesk/zendesk"
)

var usersNamespace = sessions.WithPrefix("zendesk:users")

// populateCache stores a page of users into the session store, keyed by user ID.
// Called from team_member.List on each page so the cache is built incrementally during resource sync.
func populateCache(ctx context.Context, ss sessions.SessionStore, users []zendesk.User) error {
	if ss == nil || len(users) == 0 {
		return nil
	}
	userMap := make(map[string]zendesk.User, len(users))
	for _, user := range users {
		userMap[strconv.FormatInt(user.ID, 10)] = user
	}
	return session.SetManyJSON(ctx, ss, userMap, usersNamespace)
}

// getCachedUsersByIDs fetches only the specified users from the session store.
func getCachedUsersByIDs(ctx context.Context, ss sessions.SessionStore, userIDs []int64) (map[int64]zendesk.User, error) {
	if ss == nil {
		return nil, nil
	}
	keyToID := make(map[string]int64, len(userIDs))
	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		k := strconv.FormatInt(id, 10)
		keys[i] = k
		keyToID[k] = id
	}
	cached, err := session.GetManyJSON[zendesk.User](ctx, ss, keys, usersNamespace)
	if err != nil {
		return nil, err
	}
	users := make(map[int64]zendesk.User, len(cached))
	for k, u := range cached {
		users[keyToID[k]] = u
	}
	return users, nil
}
