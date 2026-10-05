package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The expected codes are the ones uhttp.BaseHttpClient and uhttp.ClassifyOAuth2TokenError return;
// only Unavailable and DeadlineExceeded are retried by the SDK.

func hasRateLimitDetails(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	for _, d := range st.Details() {
		if _, ok := d.(*v2.RateLimitDescription); ok {
			return true
		}
	}
	return false
}

func TestAPIErrorCodesMatchUHTTP(t *testing.T) {
	tests := []struct {
		status        int
		want          codes.Code
		wantRateLimit bool
	}{
		{status: http.StatusBadRequest, want: codes.InvalidArgument},
		{status: http.StatusUnauthorized, want: codes.Unauthenticated},
		{status: http.StatusForbidden, want: codes.PermissionDenied},
		{status: http.StatusNotFound, want: codes.NotFound},
		{status: http.StatusRequestTimeout, want: codes.DeadlineExceeded},
		{status: http.StatusConflict, want: codes.AlreadyExists},
		{status: http.StatusUnprocessableEntity, want: codes.InvalidArgument},
		{status: http.StatusTooManyRequests, want: codes.Unavailable, wantRateLimit: true},
		{status: http.StatusInternalServerError, want: codes.Unavailable},
		{status: http.StatusNotImplemented, want: codes.Unimplemented},
		{status: http.StatusBadGateway, want: codes.Unavailable},
		{status: http.StatusServiceUnavailable, want: codes.Unavailable},
		{status: http.StatusGatewayTimeout, want: codes.Unavailable},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv.URL).GetCurrentUser(context.Background())
			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %v, want %v (err: %v)", got, tt.want, err)
			}
			if tt.wantRateLimit && !hasRateLimitDetails(err) {
				t.Fatalf("expected rate limit details on %v", err)
			}
		})
	}
}

func TestOAuthTokenErrorCodesMatchUHTTP(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		want          codes.Code
		wantRateLimit bool
		wantHint      string
	}{
		{name: "400 invalid_client", status: http.StatusBadRequest, body: `{"error":"invalid_client"}`, want: codes.Unauthenticated, wantHint: "check the client ID and secret"},
		{name: "401 invalid_client", status: http.StatusUnauthorized, body: `{"error":"invalid_client"}`, want: codes.Unauthenticated, wantHint: "check the client ID and secret"},
		{name: "400 invalid_scope", status: http.StatusBadRequest, body: `{"error":"invalid_scope"}`, want: codes.InvalidArgument, wantHint: `requested OAuth scopes "read write"`},
		{name: "400 unsupported_grant_type", status: http.StatusBadRequest, body: `{"error":"unsupported_grant_type"}`, want: codes.InvalidArgument},
		{name: "403 unauthorized_client", status: http.StatusForbidden, body: `{"error":"unauthorized_client"}`, want: codes.PermissionDenied, wantHint: "client credentials grant"},
		{name: "403 without error param", status: http.StatusForbidden, want: codes.PermissionDenied},
		{name: "429", status: http.StatusTooManyRequests, want: codes.Unavailable, wantRateLimit: true},
		{name: "500", status: http.StatusInternalServerError, want: codes.Unavailable},
		{name: "503 with invalid_client stays retryable", status: http.StatusServiceUnavailable, body: `{"error":"invalid_client"}`, want: codes.Unavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c, err := New(context.Background(), nil, "", srv.URL, AuthConfig{
				OAuth: true, OAuthClientID: "id", OAuthClientSecret: "secret", OAuthScopes: []string{"read", "write"},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = c.GetCurrentUser(context.Background())
			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %v, want %v (err: %v)", got, tt.want, err)
			}
			if tt.wantRateLimit && !hasRateLimitDetails(err) {
				t.Fatalf("expected rate limit details on %v", err)
			}
			if tt.wantHint != "" && !strings.Contains(err.Error(), tt.wantHint) {
				t.Fatalf("expected hint %q in %v", tt.wantHint, err)
			}
			if tt.wantHint == "" && strings.Contains(err.Error(), "baton-zendesk: Zendesk rejected") {
				t.Fatalf("unexpected hint in %v", err)
			}
		})
	}
}

func TestNetworkErrorsAreRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	for name, auth := range map[string]AuthConfig{
		"api-token": {Email: "agent@example.com", APIToken: "token"},
		"oauth":     {OAuth: true, OAuthClientID: "id", OAuthClientSecret: "secret"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := New(context.Background(), nil, "", url, auth)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = c.GetCurrentUser(context.Background())
			if got := status.Code(err); got != codes.Unavailable {
				t.Fatalf("code = %v, want Unavailable (err: %v)", got, err)
			}
		})
	}
}
