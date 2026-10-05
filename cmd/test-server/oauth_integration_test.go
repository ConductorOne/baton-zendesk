package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/conductorone/baton-zendesk/pkg/connector"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	srv := newServer()
	ts := httptest.NewServer(recordingMiddleware(srv.state, newMux(srv)))
	t.Cleanup(ts.Close)
	return srv, ts
}

func oauthAuth(clientID string) client.AuthConfig {
	return client.AuthConfig{
		OAuth:             true,
		OAuthClientID:     clientID,
		OAuthClientSecret: testOAuthClientSecret,
		OAuthScopes:       []string{"read", "write"},
	}
}

func newTestConnector(t *testing.T, baseURL string, auth client.AuthConfig) *connector.Connector {
	t.Helper()
	c, err := connector.New(context.Background(), nil, "", baseURL, auth, nil)
	if err != nil {
		t.Fatalf("connector.New: %v", err)
	}
	return c
}

// fullSync drives every syncer the way the SDK does and returns the sorted resource and grant IDs.
func fullSync(ctx context.Context, c *connector.Connector) ([]string, []string, error) {
	attrs := rs.SyncOpAttrs{Session: newMemorySessionStore()}
	var resourceIDs, grantIDs []string

	for _, syncer := range c.ResourceSyncers(ctx) {
		var resources []*v2.Resource
		token := ""
		for {
			attrs.PageToken = pagination.Token{Token: token}
			page, res, err := syncer.List(ctx, nil, attrs)
			if err != nil {
				return nil, nil, err
			}
			resources = append(resources, page...)
			if token = nextPageToken(res); token == "" {
				break
			}
		}

		for _, r := range resources {
			resourceIDs = append(resourceIDs, r.GetId().GetResourceType()+":"+r.GetId().GetResource())
			token = ""
			for {
				attrs.PageToken = pagination.Token{Token: token}
				grants, res, err := syncer.Grants(ctx, r, attrs)
				if err != nil {
					return nil, nil, err
				}
				for _, g := range grants {
					grantIDs = append(grantIDs, g.GetId())
				}
				if token = nextPageToken(res); token == "" {
					break
				}
			}
		}
	}

	sort.Strings(resourceIDs)
	sort.Strings(grantIDs)
	return resourceIDs, grantIDs, nil
}

func TestOAuthFullSyncMatchesAPIToken(t *testing.T) {
	srv, ts := newTestServer(t)
	ctx := context.Background()

	tokenRes, tokenGrants, err := fullSync(ctx, newTestConnector(t, ts.URL, client.AuthConfig{Email: testEmail, APIToken: testAPIToken}))
	if err != nil {
		t.Fatalf("api-token sync: %v", err)
	}
	if srv.oauth.issuedCount() != 0 {
		t.Fatalf("api-token sync must not request OAuth tokens")
	}

	c := newTestConnector(t, ts.URL, oauthAuth(testOAuthClientID))
	if _, err := c.Validate(ctx); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	oauthRes, oauthGrants, err := fullSync(ctx, c)
	if err != nil {
		t.Fatalf("oauth sync: %v", err)
	}

	if len(tokenRes) == 0 || len(tokenGrants) == 0 {
		t.Fatalf("expected resources and grants, got %d and %d", len(tokenRes), len(tokenGrants))
	}
	if !equalStrings(tokenRes, oauthRes) || !equalStrings(tokenGrants, oauthGrants) {
		t.Fatalf("oauth sync differs from api-token sync:\nresources %v\nvs %v\ngrants %v\nvs %v", tokenRes, oauthRes, tokenGrants, oauthGrants)
	}
	if got := srv.oauth.issuedCount(); got != 1 {
		t.Fatalf("expected a single cached token for the whole sync, got %d token requests", got)
	}
}

func TestOAuthTokenRenewsOnExpiry(t *testing.T) {
	srv, ts := newTestServer(t)
	ctx := context.Background()
	// Shorter than the client's early-expiry window, so every request needs a renewed token.
	srv.oauth.setLifetime(time.Second)

	if _, _, err := fullSync(ctx, newTestConnector(t, ts.URL, oauthAuth(testOAuthClientID))); err != nil {
		t.Fatalf("sync with short-lived tokens: %v", err)
	}
	if got := srv.oauth.issuedCount(); got < 2 {
		t.Fatalf("expected tokens to be renewed during the sync, got %d token requests", got)
	}
}

func TestOAuthRevokedTokenIsRenewedOnce(t *testing.T) {
	srv, ts := newTestServer(t)
	ctx := context.Background()
	c := newTestConnector(t, ts.URL, oauthAuth(testOAuthClientID))

	if _, err := c.Validate(ctx); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	srv.oauth.revokeAll()

	if _, _, err := fullSync(ctx, c); err != nil {
		t.Fatalf("sync after revocation: %v", err)
	}
	if got := srv.oauth.issuedCount(); got != 2 {
		t.Fatalf("expected exactly one renewal after revocation, got %d token requests", got)
	}
}

func TestOAuthPersistentRejectionReturnsUnauthenticated(t *testing.T) {
	srv, ts := newTestServer(t)
	ctx := context.Background()
	// Tokens are rejected by the API immediately while the client believes they are valid.
	srv.oauth.setLifetime(-time.Second)
	c := newTestConnector(t, ts.URL, oauthAuth(testOAuthClientID))

	resources, _, err := fullSync(ctx, c)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
	if resources != nil {
		t.Fatalf("expected no resources on failure, got %v", resources)
	}
	if got := srv.oauth.issuedCount(); got != 2 {
		t.Fatalf("expected the original token plus one retry, got %d token requests", got)
	}
}

func TestOAuthValidateFailures(t *testing.T) {
	_, ts := newTestServer(t)
	ctx := context.Background()

	badSecret := oauthAuth(testOAuthClientID)
	badSecret.OAuthClientSecret = "wrong"
	_, err := newTestConnector(t, ts.URL, badSecret).Validate(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bad secret: expected Unauthenticated, got %v", err)
	}

	_, err = newTestConnector(t, ts.URL, oauthAuth(testOAuthAgentClientID)).Validate(ctx)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-admin client owner: expected PermissionDenied, got %v", err)
	}

	_, err = newTestConnector(t, ts.URL, client.AuthConfig{Email: testEmail, APIToken: testAPIToken}).Validate(ctx)
	if err != nil {
		t.Fatalf("api-token Validate: %v", err)
	}
}

func TestOAuthTokenEndpointErrors(t *testing.T) {
	tests := []struct {
		status int
		want   codes.Code
	}{
		{http.StatusInternalServerError, codes.Unavailable},
		{http.StatusServiceUnavailable, codes.Unavailable},
		{http.StatusTooManyRequests, codes.ResourceExhausted},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv, ts := newTestServer(t)
			srv.oauth.setFailStatus(tt.status)

			resources, _, err := fullSync(context.Background(), newTestConnector(t, ts.URL, oauthAuth(testOAuthClientID)))
			if status.Code(err) != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
			if resources != nil {
				t.Fatalf("expected no resources on failure, got %v", resources)
			}
		})
	}
}

func TestOAuthConcurrentRequestsShareOneToken(t *testing.T) {
	srv, ts := newTestServer(t)
	ctx := context.Background()
	c := newTestConnector(t, ts.URL, oauthAuth(testOAuthClientID))

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Validate(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
	}
	if got := srv.oauth.issuedCount(); got != 1 {
		t.Fatalf("expected 1 token request under concurrency, got %d", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type memorySessionStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{values: make(map[string][]byte)}
}

func (m *memorySessionStore) key(ctx context.Context, key string, opts []sessions.SessionStoreOption) string {
	bag := &sessions.SessionStoreBag{}
	for _, opt := range opts {
		_ = opt(ctx, bag)
	}
	return bag.Prefix + key
}

func (m *memorySessionStore) Get(ctx context.Context, key string, opt ...sessions.SessionStoreOption) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[m.key(ctx, key, opt)]
	return v, ok, nil
}

func (m *memorySessionStore) GetMany(ctx context.Context, keys []string, opt ...sessions.SessionStoreOption) (map[string][]byte, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := make(map[string][]byte)
	for _, k := range keys {
		if v, ok := m.values[m.key(ctx, k, opt)]; ok {
			found[k] = v
		}
	}
	return found, nil, nil
}

func (m *memorySessionStore) Set(ctx context.Context, key string, value []byte, opt ...sessions.SessionStoreOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[m.key(ctx, key, opt)] = value
	return nil
}

func (m *memorySessionStore) SetMany(ctx context.Context, values map[string][]byte, opt ...sessions.SessionStoreOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range values {
		m.values[m.key(ctx, k, opt)] = v
	}
	return nil
}

func (m *memorySessionStore) Delete(ctx context.Context, key string, opt ...sessions.SessionStoreOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, m.key(ctx, key, opt))
	return nil
}

func (m *memorySessionStore) Clear(ctx context.Context, opt ...sessions.SessionStoreOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values = make(map[string][]byte)
	return nil
}

func (m *memorySessionStore) GetAll(ctx context.Context, pageToken string, opt ...sessions.SessionStoreOption) (map[string][]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.values))
	for k, v := range m.values {
		out[k] = v
	}
	return out, "", nil
}

func nextPageToken(res *rs.SyncOpResults) string {
	if res == nil {
		return ""
	}
	return res.NextPageToken
}
