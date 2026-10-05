package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
)

const (
	// https://developer.zendesk.com/api-reference/ticketing/oauth/grant_type_tokens/
	pathOAuthTokens = "/oauth/tokens" //nolint:gosec // URL path, not a credential.

	// Requested explicitly because the per-client default can be as low as 1,800 s (Zendesk allows 300–172,800 s).
	oauthTokenLifetimeSeconds = 7200

	maxOAuthErrorBodyBytes = 4096
)

type clientCredentialsRequest struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Scope        string `json:"scope,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
}

type clientCredentialsResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// clientCredentialsTokenSource requests tokens with a JSON body, which is what Zendesk documents
// (golang.org/x/oauth2/clientcredentials sends a form body).
type clientCredentialsTokenSource struct {
	httpClient   *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string

	mu    sync.Mutex
	token *oauth2.Token
}

func newClientCredentialsTokenSource(httpClient *http.Client, tokenURL, clientID, clientSecret string, scopes []string) *clientCredentialsTokenSource {
	return &clientCredentialsTokenSource{
		httpClient:   httpClient,
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		scope:        strings.Join(scopes, " "),
	}
}

// Token returns the cached token, fetching a new one when it is missing or about to expire.
func (s *clientCredentialsTokenSource) Token(ctx context.Context) (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token.Valid() {
		return s.token, nil
	}
	token, err := s.fetch(ctx)
	if err != nil {
		return nil, err
	}
	s.token = token
	return token, nil
}

// Invalidate drops the cached token only if it is still the rejected one, so concurrent callers renew once.
func (s *clientCredentialsTokenSource) Invalidate(rejected *oauth2.Token) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token == rejected {
		s.token = nil
	}
}

func (s *clientCredentialsTokenSource) fetch(ctx context.Context) (*oauth2.Token, error) {
	payload, err := json.Marshal(clientCredentialsRequest{ //nolint:gosec // Zendesk requires the secret in the token request body.
		GrantType:    "client_credentials",
		ClientID:     s.clientID,
		ClientSecret: s.clientSecret,
		Scope:        s.scope,
		ExpiresIn:    oauthTokenLifetimeSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("baton-zendesk: encode OAuth token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("baton-zendesk: build OAuth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, uhttp.WrapErrors(codes.Unavailable, "baton-zendesk: OAuth token request failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, oauthTokenError(resp)
	}

	var body clientCredentialsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("baton-zendesk: decode OAuth token response: %w", err)
	}
	if body.AccessToken == "" {
		return nil, uhttp.WrapErrors(codes.Unauthenticated, "baton-zendesk: OAuth token response has no access token")
	}

	token := &oauth2.Token{
		AccessToken: body.AccessToken,
		TokenType:   body.TokenType,
	}
	if body.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}

	ctxzap.Extract(ctx).Debug("baton-zendesk: obtained OAuth access token", zap.Time("expiry", token.Expiry))
	return token, nil
}

func oauthTokenError(resp *http.Response) error {
	var body oauthErrorResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxOAuthErrorBodyBytes)).Decode(&body)
	cause := fmt.Errorf("token endpoint returned %s: %s %s", resp.Status, body.Error, body.ErrorDescription)

	switch {
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		return uhttp.WrapErrors(codes.Unauthenticated,
			"baton-zendesk: OAuth client credentials rejected; check the client ID and secret, and that the client is confidential and active", cause)
	case resp.StatusCode == http.StatusForbidden:
		return uhttp.WrapErrors(codes.PermissionDenied,
			"baton-zendesk: OAuth token request denied; the requested scopes may exceed the client's allowed scopes", cause)
	case resp.StatusCode == http.StatusTooManyRequests:
		return uhttp.WrapErrorsWithRateLimitInfo(codes.ResourceExhausted, resp, cause)
	case resp.StatusCode >= http.StatusInternalServerError:
		return uhttp.WrapErrors(codes.Unavailable, "baton-zendesk: OAuth token endpoint unavailable", cause)
	default:
		return uhttp.WrapErrors(codes.Unknown, "baton-zendesk: unexpected OAuth token endpoint response", cause)
	}
}

// bearerTransport authenticates requests with the cached token and renews it once when Zendesk rejects it.
type bearerTransport struct {
	base   http.RoundTripper
	tokens *clientCredentialsTokenSource
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	token, err := t.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := t.send(req, token)
	if err != nil || !isInvalidTokenResponse(resp) {
		return resp, err
	}

	retry, ok := rewindRequest(req)
	if !ok {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	t.tokens.Invalidate(token)
	token, err = t.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	ctxzap.Extract(ctx).Debug("baton-zendesk: OAuth access token rejected, retrying with a new token")
	return t.send(retry, token)
}

func (t *bearerTransport) send(req *http.Request, token *oauth2.Token) (*http.Response, error) {
	out := req.Clone(req.Context())
	token.SetAuthHeader(out)
	return t.base.RoundTrip(out)
}

// rewindRequest returns a copy of req that can be sent again, or false if its body cannot be replayed.
func rewindRequest(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	out := req.Clone(req.Context())
	out.Body = body
	return out, true
}

// isInvalidTokenResponse detects Zendesk's 401 {"error":"invalid_token"}, leaving the body readable for the caller.
func isInvalidTokenResponse(resp *http.Response) bool {
	if resp.StatusCode != http.StatusUnauthorized {
		return false
	}
	if strings.Contains(resp.Header.Get("WWW-Authenticate"), "invalid_token") {
		return true
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthErrorBodyBytes))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	var body oauthErrorResponse
	return json.Unmarshal(raw, &body) == nil && body.Error == "invalid_token"
}

// oauthTokenURL resolves the token endpoint, which lives at the origin root rather than under /api/v2.
func oauthTokenURL(subdomain, baseURL string) (string, error) {
	origin := fmt.Sprintf("https://%s.zendesk.com", subdomain)
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil {
			return "", fmt.Errorf("baton-zendesk: parse base URL: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return "", fmt.Errorf("baton-zendesk: base URL must include a scheme and host")
		}
		origin = u.Scheme + "://" + u.Host
	}
	return origin + pathOAuthTokens, nil
}
