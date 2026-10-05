package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"sync"
	"time"
)

const (
	minTokenLifetimeSeconds = 300
	maxTokenLifetimeSeconds = 172800
)

type issuedToken struct {
	userID int64
	expiry time.Time
}

// oauthState mocks Zendesk's client credentials grant. lifetime and failStatus
// are test knobs: they shorten issued tokens and force token endpoint errors.
type oauthState struct {
	mu         sync.Mutex
	clients    map[string]int64
	tokens     map[string]issuedToken
	lifetime   time.Duration
	failStatus int
	issued     int
}

func newOAuthState() *oauthState {
	return &oauthState{
		clients: map[string]int64{
			testOAuthClientID:      oauthAdminOwnerID,
			testOAuthAgentClientID: oauthAgentOwnerID,
		},
		tokens: make(map[string]issuedToken),
	}
}

func (o *oauthState) authenticate(token string) (int64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.tokens[token]
	if !ok || time.Now().After(t.expiry) {
		return 0, false
	}
	return t.userID, true
}

func (o *oauthState) issue(userID int64, requested time.Duration) (string, time.Duration, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, err
	}
	token := hex.EncodeToString(buf)

	o.mu.Lock()
	defer o.mu.Unlock()
	lifetime := requested
	if o.lifetime != 0 {
		lifetime = o.lifetime
	}
	o.tokens[token] = issuedToken{userID: userID, expiry: time.Now().Add(lifetime)}
	o.issued++
	return token, lifetime, nil
}

func (o *oauthState) revokeAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tokens = make(map[string]issuedToken)
}

func (o *oauthState) setLifetime(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lifetime = d
}

func (o *oauthState) setFailStatus(status int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failStatus = status
}

func (o *oauthState) currentFailStatus() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.failStatus
}

func (o *oauthState) issuedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.issued
}

func (o *oauthState) clientOwner(clientID string) (int64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id, ok := o.clients[clientID]
	return id, ok
}

// handleOAuthTokens mocks the client credentials grant, which takes a JSON body.
// Doc URL: https://developer.zendesk.com/api-reference/ticketing/oauth/grant_type_tokens/
func (srv *server) handleOAuthTokens(w http.ResponseWriter, r *http.Request) {
	if status := srv.oauth.currentFailStatus(); status != 0 {
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "1")
		}
		writeJSONStatus(w, status, map[string]any{keyError: "server_error"})
		return
	}

	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "invalid_request"})
		return
	}
	var req struct {
		GrantType    string `json:"grant_type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "invalid_request"})
		return
	}
	if req.GrantType != "client_credentials" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "unsupported_grant_type"})
		return
	}
	if req.ExpiresIn != 0 && (req.ExpiresIn < minTokenLifetimeSeconds || req.ExpiresIn > maxTokenLifetimeSeconds) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "invalid_request"})
		return
	}
	ownerID, ok := srv.oauth.clientOwner(req.ClientID)
	if !ok || req.ClientSecret != testOAuthClientSecret {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{
			keyError:            "invalid_client",
			"error_description": "Client authentication failed due to unknown client, no client authentication included, or unsupported authentication method.",
		})
		return
	}

	requested := time.Duration(req.ExpiresIn) * time.Second
	if requested == 0 {
		requested = 1800 * time.Second
	}
	token, lifetime, err := srv.oauth.issue(ownerID, requested)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{keyError: "server_error"})
		return
	}
	writeJSON(w, map[string]any{
		"access_token": token,
		"token_type":   "bearer",
		"scope":        req.Scope,
		"expires_in":   int64(lifetime / time.Second),
	})
}
