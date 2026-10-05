package main

import (
	"context"
	"net/http"
	"strings"
)

// Hardcoded test credentials only — never real credentials in a test server.
const (
	testEmail    = "agent@example.com"
	testAPIToken = "test-token"

	testOAuthClientID      = "test-oauth-client"
	testOAuthAgentClientID = "test-oauth-agent-client"
	testOAuthClientSecret  = "test-oauth-secret" //nolint:gosec // Test-only credential.

	// Users that /users/me.json resolves to for each credential.
	apiTokenUserID    int64 = 101
	oauthAdminOwnerID int64 = 101
	oauthAgentOwnerID int64 = 102
)

type authUserKey struct{}

// requireAuth accepts either Zendesk's API token Basic scheme or a Bearer token
// issued by /oauth/tokens.
// https://developer.zendesk.com/api-reference/introduction/security-and-auth/#api-token
// (matches zendesk.APITokenCredential.Email(), which appends "/token" —
// see vendor/github.com/nukosuke/go-zendesk/zendesk/credential.go).
func (srv *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			userID, valid := srv.oauth.authenticate(bearer)
			if !valid {
				writeJSONStatus(w, http.StatusUnauthorized, map[string]any{
					keyError:       "invalid_token",
					keyDescription: "The access token provided is expired, revoked, malformed or invalid for other reasons.",
				})
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), authUserKey{}, userID)))
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != testEmail+"/token" || pass != testAPIToken {
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{
				keyError:       "Couldn't authenticate you",
				keyDescription: "Couldn't authenticate you",
			})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authUserKey{}, apiTokenUserID)))
	}
}

func authenticatedUserID(r *http.Request) int64 {
	id, _ := r.Context().Value(authUserKey{}).(int64)
	return id
}
