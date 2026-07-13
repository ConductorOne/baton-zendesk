package main

import "net/http"

// Hardcoded test credentials only — never real credentials in a test server.
const (
	testEmail    = "agent@example.com"
	testAPIToken = "test-token"
)

// requireAuth validates HTTP Basic Auth the way Zendesk's API token scheme
// does: username is "{email}/token", password is the API token.
// https://developer.zendesk.com/api-reference/introduction/security-and-auth/#api-token
// (matches zendesk.APITokenCredential.Email(), which appends "/token" —
// see vendor/github.com/nukosuke/go-zendesk/zendesk/credential.go).
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testEmail+"/token" || pass != testAPIToken {
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{
				keyError:       "Couldn't authenticate you",
				keyDescription: "Couldn't authenticate you",
			})
			return
		}
		next(w, r)
	}
}
