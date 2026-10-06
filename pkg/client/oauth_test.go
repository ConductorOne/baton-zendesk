package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nukosuke/go-zendesk/zendesk"
)

func TestOAuthTokenURL(t *testing.T) {
	tests := []struct {
		name, subdomain, baseURL, want string
		wantErr                        bool
	}{
		{name: "subdomain", subdomain: "acme", want: "https://acme.zendesk.com/oauth/tokens"},
		{name: "base url origin only", subdomain: "acme", baseURL: "http://127.0.0.1:8765/api/v2", want: "http://127.0.0.1:8765/oauth/tokens"},
		{name: "base url without host", baseURL: "/api/v2", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := oauthTokenURL(tt.subdomain, tt.baseURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOAuthClientSendsFormTokenRequestAndBearer(t *testing.T) {
	var tokenRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathOAuthTokens:
			tokenRequests.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token request: %v", err)
			}
			want := url.Values{
				"grant_type": {"client_credentials"}, "client_id": {"id"}, "client_secret": {"secret"},
				"scope": {"read write"}, "expires_in": {strconv.Itoa(oauthTokenLifetimeSeconds)},
			}
			if !reflect.DeepEqual(r.PostForm, want) {
				t.Errorf("token request = %v, want %v", r.PostForm, want)
			}
			writeToken(w, "abc")
		case pathCurrentUser:
			if got := r.Header.Get("Authorization"); got != "Bearer abc" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{"user":{"id":1,"role":"admin"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(context.Background(), nil, "", srv.URL, AuthConfig{
		OAuth: true, OAuthClientID: "id", OAuthClientSecret: "secret", OAuthScopes: []string{"read", "write"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for range 3 {
		if _, err := c.GetCurrentUser(context.Background()); err != nil {
			t.Fatalf("GetCurrentUser: %v", err)
		}
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("expected the token to be cached, got %d token requests", got)
	}
}

func TestOAuthRetryReplaysRequestBody(t *testing.T) {
	var tokenRequests, posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathOAuthTokens {
			n := tokenRequests.Add(1)
			writeToken(w, fmt.Sprintf("token-%d", n))
			return
		}
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"group_id":7`) {
			t.Errorf("request body was not replayed: %q", body)
		}
		if r.Header.Get("Authorization") == "Bearer token-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"group_membership":{"id":1,"user_id":2,"group_id":7}}`))
	}))
	defer srv.Close()

	c, err := New(context.Background(), nil, "", srv.URL, AuthConfig{OAuth: true, OAuthClientID: "id", OAuthClientSecret: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.CreateGroupMembership(context.Background(), zendesk.GroupMembership{UserID: 2, GroupID: 7}); err != nil {
		t.Fatalf("CreateGroupMembership: %v", err)
	}
	if posts.Load() != 2 || tokenRequests.Load() != 2 {
		t.Fatalf("expected one retry with a renewed token, got %d posts and %d token requests", posts.Load(), tokenRequests.Load())
	}
}

func TestOAuthDoesNotRetryOtherUnauthorized(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathOAuthTokens {
			writeToken(w, "abc")
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Couldn't authenticate you"}`))
	}))
	defer srv.Close()

	c, err := New(context.Background(), nil, "", srv.URL, AuthConfig{OAuth: true, OAuthClientID: "id", OAuthClientSecret: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetCurrentUser(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected no retry, got %d calls", got)
	}
}

func TestAPITokenClientUsesBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "agent@example.com/token" || pass != "token" {
			t.Errorf("unexpected credentials %q %q %v", user, pass, ok)
		}
		_, _ = w.Write([]byte(`{"user":{"id":1,"role":"admin"}}`))
	}))
	defer srv.Close()

	c, err := New(context.Background(), nil, "", srv.URL, AuthConfig{Email: "agent@example.com", APIToken: "token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetCurrentUser(context.Background()); err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
}

func TestOAuthRequiresClientCredentials(t *testing.T) {
	if _, err := New(context.Background(), nil, "acme", "", AuthConfig{OAuth: true}); err == nil {
		t.Fatal("expected an error for missing client credentials")
	}
}

// writeToken answers a token request the way Zendesk does; the oauth2 library needs the JSON content type.
func writeToken(w http.ResponseWriter, accessToken string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"bearer","expires_in":7200}`, accessToken)
}
