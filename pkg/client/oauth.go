package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// https://developer.zendesk.com/api-reference/ticketing/oauth/grant_type_tokens/
	pathOAuthTokens = "/oauth/tokens" //nolint:gosec // URL path, not a credential.

	// Requested explicitly because the per-client default can be as low as 1,800 s (Zendesk allows 300–172,800 s).
	oauthTokenLifetimeSeconds = 7200

	maxOAuthErrorBodyBytes = 4096
)

type oauthErrorResponse struct {
	Error string `json:"error"`
}

// clientCredentialsTokenSource caches the token itself, instead of using oauth2.ReuseTokenSource,
// so bearerTransport can drop a token Zendesk rejects before it expires.
type clientCredentialsTokenSource struct {
	httpClient *http.Client
	config     *clientcredentials.Config

	mu    sync.Mutex
	token *oauth2.Token
}

func newClientCredentialsTokenSource(httpClient *http.Client, tokenURL, clientID, clientSecret string, scopes []string) *clientCredentialsTokenSource {
	return &clientCredentialsTokenSource{
		httpClient: httpClient,
		config: &clientcredentials.Config{
			ClientID:       clientID,
			ClientSecret:   clientSecret,
			TokenURL:       tokenURL,
			Scopes:         scopes,
			EndpointParams: url.Values{"expires_in": {strconv.Itoa(oauthTokenLifetimeSeconds)}},
			AuthStyle:      oauth2.AuthStyleInParams,
		},
	}
}

// Token returns the cached token, fetching a new one when it is missing or about to expire.
func (s *clientCredentialsTokenSource) Token(ctx context.Context) (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token.Valid() {
		return s.token, nil
	}
	token, err := s.config.Token(context.WithValue(ctx, oauth2.HTTPClient, s.httpClient))
	if err != nil {
		// Errors go through the SDK classifier so their gRPC codes match the SDK's own OAuth token sources.
		return nil, s.withSetupHint(uhttp.ClassifyOAuth2TokenError(fmt.Errorf("baton-zendesk: OAuth token request failed: %w", err)))
	}
	ctxzap.Extract(ctx).Debug("baton-zendesk: obtained OAuth access token", zap.Time("expiry", token.Expiry))
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

// withSetupHint prefixes rejections caused by connector or OAuth client settings with an actionable message,
// keeping the SDK's gRPC code. Retryable errors are left as-is.
func (s *clientCredentialsTokenSource) withSetupHint(err error) error {
	code := status.Code(err)
	if code == codes.Unavailable || code == codes.DeadlineExceeded {
		return err
	}
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		return err
	}

	var hint string
	switch retrieveErr.ErrorCode {
	case "invalid_scope":
		hint = fmt.Sprintf("Zendesk rejected the requested OAuth scopes %q; set OAuth Scopes to scopes the Zendesk OAuth client allows",
			strings.Join(s.config.Scopes, " "))
	case "invalid_client":
		hint = "Zendesk rejected the OAuth client; check the client ID and secret, and that the client is confidential and active"
	case "unauthorized_client":
		hint = "the Zendesk OAuth client is not allowed to use the client credentials grant"
	default:
		return err
	}
	return uhttp.WrapErrors(code, "baton-zendesk: "+hint, err)
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
