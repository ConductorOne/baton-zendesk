package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/conductorone/baton-zendesk/pkg/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newValidateTestServer(t *testing.T, currentUser string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/tokens":
			_, _ = w.Write([]byte(`{"access_token":"abc","token_type":"bearer","expires_in":7200}`))
		case "/users/me.json":
			_, _ = w.Write([]byte(currentUser))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestValidate(t *testing.T) {
	const (
		admin     = `{"user":{"id":1,"role":"admin"}}`
		agent     = `{"user":{"id":2,"role":"agent"}}`
		anonymous = `{"user":{"id":null,"name":"Anonymous user","role":"end-user"}}`
	)
	apiToken := client.AuthConfig{Email: "agent@example.com", APIToken: "token"}
	oauth := client.AuthConfig{OAuth: true, OAuthClientID: "id", OAuthClientSecret: "secret"}

	tests := []struct {
		name        string
		currentUser string
		auth        client.AuthConfig
		want        codes.Code
	}{
		{name: "api-token agent is not checked", currentUser: agent, auth: apiToken, want: codes.OK},
		{name: "api-token anonymous is not checked", currentUser: anonymous, auth: apiToken, want: codes.OK},
		{name: "oauth admin", currentUser: admin, auth: oauth, want: codes.OK},
		{name: "oauth agent", currentUser: agent, auth: oauth, want: codes.PermissionDenied},
		{name: "oauth anonymous", currentUser: anonymous, auth: oauth, want: codes.Unauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newValidateTestServer(t, tt.currentUser)
			c, err := New(context.Background(), nil, "", srv.URL, tt.auth, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = c.Validate(context.Background())
			if got := status.Code(err); got != tt.want {
				t.Fatalf("Validate() code = %v, want %v (err: %v)", got, tt.want, err)
			}
		})
	}
}
