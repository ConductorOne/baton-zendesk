package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const duplicateEmailBody = `{"error":"RecordInvalid","description":"Record validation errors",` +
	`"details":{"email":[{"description":"Email: person@example.com is already being used by another user","error":"DuplicateValue"}]}}`

type createAccountFake struct {
	mu           sync.Mutex
	calls        []string
	createStatus int
	createBody   string
	searchStatus int
	searchUsers  []map[string]any
	searchNext   string
	listUsers    map[string][]map[string]any
}

func (f *createAccountFake) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
}

func (f *createAccountFake) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Ratelimit-Limit", "700")
		w.Header().Set("Ratelimit-Remaining", "0")
		w.Header().Set("Ratelimit-Reset", "1")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/users.json":
			w.WriteHeader(f.createStatus)
			_, _ = w.Write([]byte(f.createBody))
		case r.Method == http.MethodGet && r.URL.Path == "/users/search.json":
			requireEqual(t, "email:person@example.com", r.URL.Query().Get("query"))
			if f.searchStatus != 0 && f.searchStatus != http.StatusOK {
				w.WriteHeader(f.searchStatus)
				_, _ = w.Write([]byte(`{"error":"failed"}`))
				return
			}
			var next any
			if f.searchNext != "" {
				next = f.searchNext
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"users": f.searchUsers, "next_page": next, "count": len(f.searchUsers)})
		case r.Method == http.MethodGet && r.URL.Path == "/users.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"users": f.listUsers[r.URL.Query().Get("role")], "meta": map[string]any{"has_more": false}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func zendeskUser(id int64, email, role string, active, suspended bool) map[string]any {
	return map[string]any{
		"id": id, "name": "Existing Person", "email": email, "role": role,
		"active": active, "suspended": suspended,
		"created_at": "2024-01-02T03:04:05Z", "last_login_at": "2024-02-03T04:05:06Z",
	}
}

func createAccountRequest(t *testing.T) *v2.AccountInfo {
	t.Helper()
	profile, err := structpb.NewStruct(map[string]any{"name": "Requested Person", "email": "person@example.com", "role": "agent"})
	requireNoError(t, err)
	return &v2.AccountInfo{Profile: profile}
}

// syncedTeamMember returns the resource team_member.List emits for id.
func syncedTeamMember(t *testing.T, tm *teamMemberResourceType, id int64) *v2.Resource {
	t.Helper()
	token := ""
	for {
		resources, results, err := tm.List(context.Background(), nil, rs.SyncOpAttrs{PageToken: pagination.Token{Token: token}})
		requireNoError(t, err)
		for _, r := range resources {
			if r.GetId().GetResource() == strconv.FormatInt(id, 10) {
				return r
			}
		}
		if results.NextPageToken == "" {
			t.Fatalf("team member %d not emitted by sync", id)
		}
		token = results.NextPageToken
	}
}

func TestCreateAccountDuplicateEmailReturnsExistingTeamMember(t *testing.T) {
	for _, tc := range []struct {
		name      string
		email     string
		role      string
		suspended bool
	}{
		{name: "agent", email: "person@example.com", role: "agent"},
		{name: "admin with different case", email: "Person@Example.com", role: "admin"},
		{name: "suspended agent stays suspended", email: "person@example.com", role: "agent", suspended: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := zendeskUser(4242, tc.email, tc.role, true, tc.suspended)
			fake := &createAccountFake{
				createStatus: http.StatusUnprocessableEntity,
				createBody:   duplicateEmailBody,
				searchUsers:  []map[string]any{zendeskUser(77, "otherperson@example.com", "agent", true, false), existing},
				listUsers:    map[string][]map[string]any{tc.role: {existing}},
			}
			srv := fake.server(t)
			tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, false)

			resp, plaintext, _, err := tm.CreateAccount(context.Background(), createAccountRequest(t), nil)
			requireNoError(t, err)
			requireEmpty(t, plaintext, "no credentials for an account this connector did not create")
			result, ok := resp.(*v2.CreateAccountResponse_AlreadyExistsResult)
			requireTrue(t, ok, "expected already-exists result, got %T", resp)
			requireFalse(t, result.GetIsCreateAccountResult())
			requireEqual(t, []string{"POST /users.json", "GET /users/search.json"}, fake.calls, "existing account must not be modified")

			want := syncedTeamMember(t, tm, 4242)
			requireTrue(t, proto.Equal(want, result.GetResource()), "resource must match what sync emits:\nwant %v\ngot  %v", want, result.GetResource())
			if tc.suspended {
				requireEqual(t, v2.Status_RESOURCE_STATUS_DISABLED, result.GetResource().GetStatus().GetStatus())
			}
		})
	}
}

func TestCreateAccountDuplicateEmailWithoutUnambiguousMatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		users []map[string]any
		next  string
		want  string
	}{
		{name: "no match", users: []map[string]any{zendeskUser(77, "otherperson@example.com", "agent", true, false)}, want: "no team member"},
		{name: "secondary identity only", users: nil, want: "no team member"},
		{name: "end-user is not a team member", users: []map[string]any{zendeskUser(5, "person@example.com", "end-user", true, false)}, want: "not a team member"},
		{name: "deleted user", users: []map[string]any{zendeskUser(5, "person@example.com", "agent", false, false)}, want: "not active"},
		{name: "multiple matches", users: []map[string]any{
			zendeskUser(5, "person@example.com", "agent", true, false),
			zendeskUser(6, "PERSON@example.com", "admin", true, false),
		}, want: "multiple users"},
		{name: "more results than one page", users: []map[string]any{zendeskUser(5, "person@example.com", "agent", true, false)}, next: "https://example.zendesk.com/next", want: "too many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &createAccountFake{
				createStatus: http.StatusUnprocessableEntity,
				createBody:   duplicateEmailBody,
				searchUsers:  tc.users,
				searchNext:   tc.next,
			}
			srv := fake.server(t)
			tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, false)

			resp, plaintext, _, err := tm.CreateAccount(context.Background(), createAccountRequest(t), nil)
			requireError(t, err)
			requireEqual(t, codes.AlreadyExists, status.Code(err), "err: %v", err)
			requireErrorContains(t, err, tc.want)
			requireNil(t, resp)
			requireEmpty(t, plaintext)
			requireEqual(t, []string{"POST /users.json", "GET /users/search.json"}, fake.calls)
		})
	}
}

func TestCreateAccountDuplicateEmailLookupFailurePreservesStatus(t *testing.T) {
	fake := &createAccountFake{
		createStatus: http.StatusUnprocessableEntity,
		createBody:   duplicateEmailBody,
		searchStatus: http.StatusForbidden,
	}
	srv := fake.server(t)
	tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, false)

	resp, _, _, err := tm.CreateAccount(context.Background(), createAccountRequest(t), nil)
	requireError(t, err)
	requireEqual(t, codes.PermissionDenied, status.Code(err), "err: %v", err)
	requireNil(t, resp)
	requireRateLimitDetail(t, err)
}

func requireRateLimitDetail(t *testing.T, err error) {
	t.Helper()
	for _, d := range status.Convert(err).Details() {
		if rl, ok := d.(*v2.RateLimitDescription); ok {
			requireEqual(t, int64(700), rl.GetLimit())
			return
		}
	}
	t.Fatalf("rate-limit details missing from %v", err)
}

func TestCreateAccountOtherErrorsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantCode codes.Code
	}{
		{name: "other 422 validation error", status: http.StatusUnprocessableEntity,
			body:     `{"error":"RecordInvalid","details":{"email":[{"description":"Email is invalid","error":"InvalidFormat"}]}}`,
			wantCode: codes.InvalidArgument},
		{name: "duplicate on another field", status: http.StatusUnprocessableEntity,
			body:     `{"error":"RecordInvalid","details":{"external_id":[{"description":"External has already been taken","error":"DuplicateValue"}]}}`,
			wantCode: codes.InvalidArgument},
		{name: "duplicate wording without error code", status: http.StatusUnprocessableEntity,
			body:     `{"error":"RecordInvalid","description":"Email is already being used by another user DuplicateValue"}`,
			wantCode: codes.InvalidArgument},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"Forbidden"}`, wantCode: codes.PermissionDenied},
		{name: "not implemented", status: http.StatusNotImplemented, body: duplicateEmailBody, wantCode: codes.Unimplemented},
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":"InternalError"}`, wantCode: codes.Unavailable},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":"TooManyRequests"}`, wantCode: codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &createAccountFake{createStatus: tc.status, createBody: tc.body}
			srv := fake.server(t)
			tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, false)

			resp, plaintext, _, err := tm.CreateAccount(context.Background(), createAccountRequest(t), nil)
			requireError(t, err)
			requireEqual(t, tc.wantCode, status.Code(err), "err: %v", err)
			requireNil(t, resp)
			requireEmpty(t, plaintext)
			requireEqual(t, []string{"POST /users.json"}, fake.calls, "no lookup for non-conflict errors")
			requireRateLimitDetail(t, err)
		})
	}
}

func TestCreateAccountSuccessUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireEqual(t, http.MethodPost, r.Method)
		requireEqual(t, "/users.json", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"user": zendeskUser(99, "person@example.com", "agent", true, false)})
	}))
	t.Cleanup(srv.Close)
	tm := teamMemberBuilder(newTeamMemberTestClient(t, srv.URL), nil, false)

	resp, plaintext, _, err := tm.CreateAccount(context.Background(), createAccountRequest(t), nil)
	requireNoError(t, err)
	requireEmpty(t, plaintext)
	result, ok := resp.(*v2.CreateAccountResponse_SuccessResult)
	requireTrue(t, ok, "expected success result, got %T", resp)
	requireTrue(t, result.GetIsCreateAccountResult())
	requireEqual(t, "99", result.GetResource().GetId().GetResource())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

func requireEqual(t *testing.T, want, got any, msgAndArgs ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %v, got %v%s", want, got, message(msgAndArgs))
	}
}

func requireTrue(t *testing.T, ok bool, msgAndArgs ...any) {
	t.Helper()
	if !ok {
		t.Fatalf("expected true%s", message(msgAndArgs))
	}
}

func requireFalse(t *testing.T, ok bool, msgAndArgs ...any) {
	t.Helper()
	if ok {
		t.Fatalf("expected false%s", message(msgAndArgs))
	}
}

func requireEmpty[T any](t *testing.T, v []T, msgAndArgs ...any) {
	t.Helper()
	if len(v) != 0 {
		t.Fatalf("expected empty, got %v%s", v, message(msgAndArgs))
	}
}

func requireNil(t *testing.T, v any) {
	t.Helper()
	if v != nil && !reflect.ValueOf(v).IsNil() {
		t.Fatalf("expected nil, got %v", v)
	}
}

func message(msgAndArgs []any) string {
	if len(msgAndArgs) == 0 {
		return ""
	}
	if format, ok := msgAndArgs[0].(string); ok {
		return ": " + fmt.Sprintf(format, msgAndArgs[1:]...)
	}
	return fmt.Sprint(msgAndArgs...)
}
