# Zendesk Ticket Provisioning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `connectorbuilder.TicketManagerLimited` so ConductorOne can create and track Zendesk tickets (access-request fulfillment), per `docs/superpowers/specs/2026-07-23-zendesk-ticketing-design.md`.

**Architecture:** One `v2.TicketSchema` per active Zendesk ticket form (fallback to a single `"default"` schema on 404 / zero active forms). Client layer adds raw ticket create/read calls with local types (go-zendesk's `CustomField` unmarshaler rejects JSON numbers) plus bounded drains for forms (offset) and fields (cursor). Connector layer adds the six interface methods on `*Connector`. Wiring is `field.TicketingField` re-export + conditional `connectorbuilder.WithTicketingEnabled()`.

**Tech Stack:** Go 1.25.2, baton-sdk v0.20.1 (vendored), nukosuke/go-zendesk (vendored, forms/fields types only), stdlib `testing` + `httptest` (no testify in this repo).

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-23-zendesk-ticketing-design.md` — R-numbers below refer to it.
- Error strings: prefix `baton-zendesk: ` + lowercase phrase; wrap with `%w`; HTTP errors through the existing `wrapZendeskError`.
- Lint (`.golangci.yml`, managed — do not edit): `godot` (comments end with a period), `nakedret` max-func-lines 0 (no naked returns anywhere), line length ≤ 200, `errorlint`, initialisms uppercase (ID, URL, API). Run `golangci-lint run` before each commit if available, else rely on CI.
- Tests: stdlib only — `t.Helper()`, `t.Fatalf`; NO testify. Client tests use `newTestClient(t, srv.URL)` + httptest mux; drain helpers loop until empty token.
- Never hand-edit `pkg/config/conf.gen.go` — change `pkg/config/config.go`, run `go generate ./pkg/config`.
- No new module deps (everything needed is vendored). Do not run `go mod tidy/vendor`.
- Commits: one per task, conventional style (`feat:`/`test:`/`docs:`), no AI attribution lines.
- All builds/tests run from repo root `/Users/ali.falahi/Documents/Github/baton-zendesk`.

## Spec coverage

| Spec req | Task(s) |
|---|---|
| R1 capability & wiring | T9 |
| R2 config | T9 |
| R3 ListTicketSchemas (forms mode) | T5, T6 |
| R4 default-schema fallback | T6 |
| R5 schema contents | T5 |
| R6 field type mapping | T5 |
| R7 CreateTicket | T7 |
| R8 value mapping (create) | T7 |
| R9 GetTicket & completion | T7 |
| R10 GetTicketSchema | T6 |
| R11 bulk methods | T8 |
| R12 client methods | T1, T2, T3, T4 |
| R13 test server + integration test | T10 |
| R14 docs | T11 |

## Security / rollout / observability

- **Security:** no new secrets; auth reuses the existing API-token credential. Fail-closed: 403 on forms fetch is an error (never masked by fallback, R4); pagination caps error instead of truncating (R12); solved/closed create requests rejected with `InvalidArgument` (R7). Input validation before API calls (`ValidateTicket`, ID parsing).
- **Rollout/rollback:** additive only — no schema/config migrations; `--ticketing` off leaves C1 routing disabled (`ExternalTicketSettings.Enabled=false`). Revert = revert the commits.
- **Observability:** skipped fields and fallback activation logged at debug/warn via `ctxzap.Extract(ctx)`; API errors carry gRPC codes via `wrapZendeskError`.
- **Release gate (R4/A3):** before release, verify forms-endpoint behavior on a live non-forms-plan trial tenant; if the plan-gate signal is not 404, add the verified signal to `isTicketFormsPlanGate` (T6) before shipping. Tracked in T11's README note.

---

### Task 1: Client local ticket types (`CustomField` decode robustness)

**Files:**
- Create: `pkg/client/tickets.go`
- Test: `pkg/client/tickets_test.go`

**Interfaces:**
- Consumes: nothing (pure types).
- Produces: `client.Ticket`, `client.TicketComment`, `client.CustomField` (with `ID int64`, `Value any`), used by T2/T7. `CustomField.UnmarshalJSON` accepts string, bool, JSON number, array-of-strings, and null.

- [ ] **Step 1: Write the failing test**

Create `pkg/client/tickets_test.go`:

```go
package client

import (
	"encoding/json"
	"testing"
)

// TestCustomFieldDecode covers the go-zendesk gap that motivated local types:
// its CustomField unmarshaler rejects JSON numbers, but Zendesk echoes every
// custom field on a ticket — including agent-set numeric values (spec R12).
func TestCustomFieldDecode(t *testing.T) {
	raw := `{"ticket":{"id":35436,"subject":"subj","description":"desc","status":"open",
		"custom_fields":[
			{"id":111,"value":42},
			{"id":112,"value":"tag_value"},
			{"id":113,"value":true},
			{"id":114,"value":["a","b"]},
			{"id":115,"value":null},
			{"id":116,"value":1.5}
		]}}`

	var data struct {
		Ticket Ticket `json:"ticket"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("decode ticket with numeric custom field: %v", err)
	}
	cfs := data.Ticket.CustomFields
	if len(cfs) != 6 {
		t.Fatalf("expected 6 custom fields, got %d", len(cfs))
	}
	if got := cfs[0].Value; got != float64(42) {
		t.Fatalf("numeric value: expected float64(42), got %#v", got)
	}
	if got := cfs[1].Value; got != "tag_value" {
		t.Fatalf("string value: expected tag_value, got %#v", got)
	}
	if got := cfs[2].Value; got != true {
		t.Fatalf("bool value: expected true, got %#v", got)
	}
	ss, ok := cfs[3].Value.([]string)
	if !ok || len(ss) != 2 || ss[0] != "a" || ss[1] != "b" {
		t.Fatalf("multiselect value: expected [a b], got %#v", cfs[3].Value)
	}
	if cfs[4].Value != nil {
		t.Fatalf("null value: expected nil, got %#v", cfs[4].Value)
	}
	if got := cfs[5].Value; got != 1.5 {
		t.Fatalf("decimal value: expected 1.5, got %#v", got)
	}
}

// TestCustomFieldEncode asserts the create-direction wire shape (spec R8).
func TestCustomFieldEncode(t *testing.T) {
	cf := CustomField{ID: 111, Value: []string{"a", "b"}}
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("marshal custom field: %v", err)
	}
	want := `{"id":111,"value":["a","b"]}`
	if string(b) != want {
		t.Fatalf("expected %s, got %s", want, string(b))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/client/ -run 'TestCustomField' -v`
Expected: FAIL (compile error: `undefined: Ticket`, `undefined: CustomField`)

- [ ] **Step 3: Write minimal implementation**

Create `pkg/client/tickets.go`:

```go
package client

import (
	"encoding/json"
	"fmt"
	"time"
)

// Ticket create/read paths use these local types instead of go-zendesk's:
// its CustomField.UnmarshalJSON rejects JSON numbers, so any ticket carrying a
// numeric custom-field value — including agent-set values on fields this
// connector never touches — fails to decode through the vendored typed methods.
const (
	// https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/#create-ticket
	pathTickets = "/tickets.json"

	// https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/#show-ticket
	pathTicketFmt = "/tickets/%d.json"
)

// TicketComment is the create-only first comment; Zendesk derives the ticket
// description from it.
type TicketComment struct {
	Body string `json:"body,omitempty"`
}

// CustomField is one Zendesk custom-field value. Value holds string, bool,
// float64, []string, or nil after decoding.
type CustomField struct {
	ID    int64 `json:"id"`
	Value any   `json:"value"`
}

// UnmarshalJSON accepts every value shape Zendesk returns for custom fields:
// string, bool, number, array of strings, and null.
func (c *CustomField) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID    int64 `json:"id"`
		Value any   `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.ID = raw.ID
	switch v := raw.Value.(type) {
	case string, bool, float64, nil:
		c.Value = v
	case []any:
		ss := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("baton-zendesk: custom field %d: array value must contain strings, got %T", raw.ID, item)
			}
			ss = append(ss, s)
		}
		c.Value = ss
	default:
		return fmt.Errorf("baton-zendesk: custom field %d: unsupported value type %T", raw.ID, v)
	}
	return nil
}

// Ticket is the subset of Zendesk's ticket object this connector reads and
// writes. Create-only fields (Comment, RequesterID, TicketFormID) are omitted
// from responses by Zendesk and carry omitempty for requests.
type Ticket struct {
	ID           int64          `json:"id,omitempty"`
	URL          string         `json:"url,omitempty"`
	Subject      string         `json:"subject,omitempty"`
	Description  string         `json:"description,omitempty"`
	Status       string         `json:"status,omitempty"`
	Priority     string         `json:"priority,omitempty"`
	Type         string         `json:"type,omitempty"`
	RequesterID  int64          `json:"requester_id,omitempty"`
	TicketFormID int64          `json:"ticket_form_id,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	CustomFields []CustomField  `json:"custom_fields,omitempty"`
	Comment      *TicketComment `json:"comment,omitempty"`
	CreatedAt    *time.Time     `json:"created_at,omitempty"`
	UpdatedAt    *time.Time     `json:"updated_at,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/client/ -run 'TestCustomField' -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/client/tickets.go pkg/client/tickets_test.go
git commit -m "feat: add local ticket types with number-safe custom field decoding"
```

---

### Task 2: Client `CreateTicket` / `GetTicket` (raw calls)

**Files:**
- Modify: `pkg/client/tickets.go`
- Test: `pkg/client/tickets_test.go`

**Interfaces:**
- Consumes: T1 types; existing `z.client.Post/Get` (return `[]byte, error`), `wrapZendeskError`.
- Produces: `func (z *ZendeskClient) CreateTicket(ctx context.Context, ticket Ticket) (Ticket, error)` and `func (z *ZendeskClient) GetTicket(ctx context.Context, ticketID int64) (Ticket, error)` — used by T7.

- [ ] **Step 1: Write the failing test**

Append to `pkg/client/tickets_test.go`:

```go
func ticketMockServer(t *testing.T) (*httptest.Server, *ZendeskClient) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets.json", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ticket Ticket `json:"ticket"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.Ticket.ID = 35436
		req.Ticket.Status = "new"
		// Echo a numeric custom field back even if the request had none —
		// simulates agent-set values on unrelated fields (spec R12).
		req.Ticket.CustomFields = append(req.Ticket.CustomFields, CustomField{ID: 999, Value: float64(7)})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": req.Ticket})
	})
	mux.HandleFunc("GET /tickets/35436.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket":{"id":35436,"subject":"subj","status":"solved",
			"custom_fields":[{"id":999,"value":7}]}}`))
	})
	mux.HandleFunc("GET /tickets/404404.json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"RecordNotFound"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, newTestClient(t, srv.URL)
}

func TestCreateTicket(t *testing.T) {
	_, zc := ticketMockServer(t)
	got, err := zc.CreateTicket(context.Background(), Ticket{
		Subject: "subj",
		Comment: &TicketComment{Body: "desc"},
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if got.ID != 35436 || got.Status != "new" {
		t.Fatalf("expected id=35436 status=new, got %+v", got)
	}
}

func TestGetTicket(t *testing.T) {
	_, zc := ticketMockServer(t)
	got, err := zc.GetTicket(context.Background(), 35436)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Status != "solved" {
		t.Fatalf("expected status=solved, got %+v", got)
	}
}

func TestGetTicketNotFound(t *testing.T) {
	_, zc := ticketMockServer(t)
	_, err := zc.GetTicket(context.Background(), 404404)
	if err == nil {
		t.Fatalf("expected error for missing ticket")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v (%v)", status.Code(err), err)
	}
}
```

Add to the test file's import block: `"context"`, `"net/http"`, `"net/http/httptest"`, `"google.golang.org/grpc/codes"`, `"google.golang.org/grpc/status"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/client/ -run 'TestCreateTicket|TestGetTicket' -v`
Expected: FAIL (compile error: `zc.CreateTicket undefined`)

- [ ] **Step 3: Write minimal implementation**

Append to `pkg/client/tickets.go` (add `"context"` to imports):

```go
// CreateTicket creates a Zendesk ticket.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/#create-ticket
func (z *ZendeskClient) CreateTicket(ctx context.Context, ticket Ticket) (Ticket, error) {
	var data, result struct {
		Ticket Ticket `json:"ticket"`
	}
	data.Ticket = ticket

	body, err := z.client.Post(ctx, pathTickets, data)
	if err != nil {
		return Ticket{}, wrapZendeskError(err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Ticket{}, fmt.Errorf("baton-zendesk: decode create ticket response: %w", err)
	}
	return result.Ticket, nil
}

// GetTicket fetches a single Zendesk ticket by ID.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/#show-ticket
func (z *ZendeskClient) GetTicket(ctx context.Context, ticketID int64) (Ticket, error) {
	body, err := z.client.Get(ctx, fmt.Sprintf(pathTicketFmt, ticketID))
	if err != nil {
		return Ticket{}, wrapZendeskError(err)
	}
	var result struct {
		Ticket Ticket `json:"ticket"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Ticket{}, fmt.Errorf("baton-zendesk: decode ticket response: %w", err)
	}
	return result.Ticket, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/client/ -run 'TestCreateTicket|TestGetTicket' -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/client/tickets.go pkg/client/tickets_test.go
git commit -m "feat: add raw ticket create/get client calls"
```

---

### Task 3: Client `ListAllTicketForms` (offset drain, page cap)

**Files:**
- Modify: `pkg/client/tickets.go`
- Test: `pkg/client/tickets_test.go`

**Interfaces:**
- Consumes: go-zendesk `GetTicketForms(ctx, *zendesk.TicketFormListOptions) ([]zendesk.TicketForm, zendesk.Page, error)`; `Page.HasNext()`; `GetTicketForm(ctx, int64) (zendesk.TicketForm, error)`.
- Produces (used by T6): `func (z *ZendeskClient) ListAllTicketForms(ctx context.Context) ([]zendesk.TicketForm, error)` and `func (z *ZendeskClient) GetTicketForm(ctx context.Context, formID int64) (zendesk.TicketForm, error)` (wraps errors through `wrapZendeskError` so 404→NotFound and 429/5xx get retryable codes, consistent with every other client call).

- [ ] **Step 1: Write the failing test**

Append to `pkg/client/tickets_test.go`:

```go
// ticketFormsMock serves two OBP pages of ticket forms, then a page-cap probe
// mode where next_page never goes empty.
func ticketFormsMock(t *testing.T, endless bool) *ZendeskClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ticket_forms.json", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		next := `"next"`
		if page == "2" && !endless {
			next = "null"
		}
		id := int64(100)
		if page == "2" {
			id = 200
		}
		fmt.Fprintf(w, `{"ticket_forms":[{"id":%d,"name":"form-%d","active":true}],
			"next_page":%s,"count":2}`, id, id, next)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newTestClient(t, srv.URL)
}

func TestListAllTicketForms(t *testing.T) {
	zc := ticketFormsMock(t, false)
	forms, err := zc.ListAllTicketForms(context.Background())
	if err != nil {
		t.Fatalf("ListAllTicketForms: %v", err)
	}
	if len(forms) != 2 || forms[0].ID != 100 || forms[1].ID != 200 {
		t.Fatalf("expected forms [100 200], got %+v", forms)
	}
}

func TestListAllTicketFormsPageCap(t *testing.T) {
	zc := ticketFormsMock(t, true)
	_, err := zc.ListAllTicketForms(context.Background())
	if err == nil {
		t.Fatalf("expected page-cap error, got nil")
	}
}

func TestGetTicketFormNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ticket_forms/9.json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"RecordNotFound"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	zc := newTestClient(t, srv.URL)
	_, err := zc.GetTicketForm(context.Background(), 9)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}
```

Add `"fmt"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/client/ -run 'TestListAllTicketForms' -v`
Expected: FAIL (compile error: `zc.ListAllTicketForms undefined`)

- [ ] **Step 3: Write minimal implementation**

Append to `pkg/client/tickets.go` (add `"github.com/nukosuke/go-zendesk/zendesk"` to imports):

```go
// maxTicketFormPages bounds the offset drain in ListAllTicketForms. Zendesk
// caps accounts at 300 ticket forms (~3 pages at 100/page); tripping the cap
// is an error, never a silent truncation.
const maxTicketFormPages = 10

// ListAllTicketForms fetches every ticket form, draining offset pagination.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/tickets/ticket_forms/#list-ticket-forms
func (z *ZendeskClient) ListAllTicketForms(ctx context.Context) ([]zendesk.TicketForm, error) {
	var all []zendesk.TicketForm
	opts := &zendesk.TicketFormListOptions{
		PageOptions: zendesk.PageOptions{Page: 1, PerPage: cbpPageSize},
	}
	for {
		forms, page, err := z.client.GetTicketForms(ctx, opts)
		if err != nil {
			return nil, wrapZendeskError(err)
		}
		all = append(all, forms...)
		if !page.HasNext() {
			return all, nil
		}
		opts.Page++
		if opts.Page > maxTicketFormPages {
			return nil, fmt.Errorf("baton-zendesk: ticket forms exceeded %d pages, refusing to truncate", maxTicketFormPages)
		}
	}
}

// GetTicketForm fetches a single ticket form by ID.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/tickets/ticket_forms/#show-ticket-form
func (z *ZendeskClient) GetTicketForm(ctx context.Context, formID int64) (zendesk.TicketForm, error) {
	form, err := z.client.GetTicketForm(ctx, formID)
	if err != nil {
		return zendesk.TicketForm{}, wrapZendeskError(err)
	}
	return form, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/client/ -run 'TestListAllTicketForms' -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/client/tickets.go pkg/client/tickets_test.go
git commit -m "feat: add bounded ticket forms drain to client"
```

---

### Task 4: Client `ListAllTicketFields` (cursor drain, page cap)

**Files:**
- Modify: `pkg/client/tickets.go`
- Test: `pkg/client/tickets_test.go`

**Interfaces:**
- Consumes: go-zendesk `GetTicketFieldsCBP(ctx, *zendesk.CBPOptions) ([]zendesk.TicketField, zendesk.CursorPaginationMeta, error)` + `getNextPageToken` — the exact typed-CBP pattern `ListGroups`/`ListOrganizations` already use (`pkg/client/client.go:85-104`). (The non-CBP `GetTicketFields` fetches one page with no options; its generated CBP sibling is the right tool.)
- Produces: `func (z *ZendeskClient) ListAllTicketFields(ctx context.Context) ([]zendesk.TicketField, error)` — used by T6.

- [ ] **Step 1: Write the failing test**

Append to `pkg/client/tickets_test.go`:

```go
func ticketFieldsMock(t *testing.T, total int) *ZendeskClient {
	t.Helper()
	fields := make([]map[string]any, total)
	for i := range fields {
		fields[i] = map[string]any{"id": int64(1000 + i), "type": "text", "title": fmt.Sprintf("f%d", i), "active": true, "removable": true}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ticket_fields.json", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("page[size]") == "" {
			http.Error(w, "missing page[size]", http.StatusBadRequest)
			return
		}
		start := 0
		if after := q.Get("page[after]"); after != "" {
			_, _ = fmt.Sscanf(after, "p%d", &start)
		}
		end := start + 100
		hasMore := true
		if end >= len(fields) {
			end = len(fields)
			hasMore = false
		}
		cursor := ""
		if hasMore {
			cursor = fmt.Sprintf("p%d", end)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ticket_fields": fields[start:end],
			"meta":          map[string]any{"has_more": hasMore, "after_cursor": cursor},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newTestClient(t, srv.URL)
}

func TestListAllTicketFields(t *testing.T) {
	zc := ticketFieldsMock(t, 150)
	fields, err := zc.ListAllTicketFields(context.Background())
	if err != nil {
		t.Fatalf("ListAllTicketFields: %v", err)
	}
	if len(fields) != 150 {
		t.Fatalf("expected 150 fields, got %d", len(fields))
	}
}

func TestListAllTicketFieldsPageCap(t *testing.T) {
	zc := ticketFieldsMock(t, 5100) // 51 pages exceeds the 50-page cap.
	_, err := zc.ListAllTicketFields(context.Background())
	if err == nil {
		t.Fatalf("expected page-cap error, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/client/ -run 'TestListAllTicketFields' -v`
Expected: FAIL (compile error: `zc.ListAllTicketFields undefined`)

- [ ] **Step 3: Write minimal implementation**

Append to `pkg/client/tickets.go`:

```go
// maxTicketFieldPages bounds the cursor drain in ListAllTicketFields
// (50 pages × 100 = 5,000 fields); tripping the cap is an error, never a
// silent truncation.
const maxTicketFieldPages = 50

// ListAllTicketFields fetches every ticket field, draining cursor pagination
// via the same typed-CBP pattern ListGroups and ListOrganizations use.
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/tickets/ticket_fields/#list-ticket-fields
func (z *ZendeskClient) ListAllTicketFields(ctx context.Context) ([]zendesk.TicketField, error) {
	var all []zendesk.TicketField
	token := ""
	for page := 0; ; page++ {
		if page >= maxTicketFieldPages {
			return nil, fmt.Errorf("baton-zendesk: ticket fields exceeded %d pages, refusing to truncate", maxTicketFieldPages)
		}
		fields, meta, err := z.client.GetTicketFieldsCBP(ctx, &zendesk.CBPOptions{
			CursorPagination: zendesk.CursorPagination{PageSize: cbpPageSize, PageAfter: token},
		})
		if err != nil {
			return nil, wrapZendeskError(err)
		}
		all = append(all, fields...)
		token = getNextPageToken(meta)
		if token == "" {
			return all, nil
		}
	}
}
```


- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/client/ -run 'TestListAllTicketFields' -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Run the full client package tests and commit**

Run: `go test ./pkg/client/ -count=1`
Expected: `ok` (all existing + new tests)

```bash
git add pkg/client/tickets.go pkg/client/tickets_test.go
git commit -m "feat: add bounded ticket fields cursor drain to client"
```

---

### Task 5: Schema construction & field mapping (`ticket_schema.go`)

**Files:**
- Create: `pkg/connector/ticket_schema.go`
- Test: `pkg/connector/ticket_schema_test.go`

**Interfaces:**
- Consumes: `zendesk.TicketForm{ID, Name, Active, TicketFieldIDs}`, `zendesk.TicketField{ID, Type, Title, Active, Required, Removable, CustomFieldOptions}`; SDK factories `sdkTicket.StringFieldSchema/BoolFieldSchema/TimestampFieldSchema/PickStringFieldSchema/PickMultipleStringsFieldSchema`.
- Produces (used by T6/T7):
  - `const defaultSchemaID = "default"`, `const syntheticFieldPriority = "priority"`, `const syntheticFieldType = "type"`
  - `func schemaForForm(ctx context.Context, form zendesk.TicketForm, fieldsByID map[int64]zendesk.TicketField) *v2.TicketSchema`
  - `func defaultTicketSchema(ctx context.Context, fields []zendesk.TicketField) *v2.TicketSchema`
  - `func schemaFieldForTicketField(ctx context.Context, f zendesk.TicketField) (*v2.TicketCustomField, bool)`
  - `func zendeskTicketStatuses() []*v2.TicketStatus` (all six), `func zendeskTicketTypes() []*v2.TicketType`
  - `var creatableTicketStatuses = map[string]bool{...}` (new/open/pending/hold)

- [ ] **Step 1: Write the failing test**

Create `pkg/connector/ticket_schema_test.go`:

```go
package connector

import (
	"context"
	"testing"

	"github.com/nukosuke/go-zendesk/zendesk"
)

// TestSchemaFieldForTicketField covers every row of the spec R6 mapping table.
func TestSchemaFieldForTicketField(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		field       zendesk.TicketField
		wantOK      bool
		wantPick    []string
		wantMulti   []string
	}{
		{name: "text", field: zendesk.TicketField{ID: 1, Type: "text", Title: "T", Active: true, Removable: true}, wantOK: true},
		{name: "textarea", field: zendesk.TicketField{ID: 2, Type: "textarea", Title: "T", Active: true, Removable: true}, wantOK: true},
		{name: "regexp", field: zendesk.TicketField{ID: 3, Type: "regexp", Title: "T", Active: true, Removable: true}, wantOK: true},
		{name: "partialcreditcard", field: zendesk.TicketField{ID: 4, Type: "partialcreditcard", Title: "T", Active: true, Removable: true}, wantOK: true},
		{name: "lookup", field: zendesk.TicketField{ID: 5, Type: "lookup", Title: "T", Active: true, Removable: true}, wantOK: true},
		{
			name: "tagger",
			field: zendesk.TicketField{ID: 6, Type: "tagger", Title: "T", Active: true, Removable: true,
				CustomFieldOptions: []zendesk.CustomFieldOption{{Name: "Opt A", Value: "opt_a"}, {Name: "Opt B", Value: "opt_b"}}},
			wantOK: true, wantPick: []string{"opt_a", "opt_b"},
		},
		{
			name: "multiselect",
			field: zendesk.TicketField{ID: 7, Type: "multiselect", Title: "T", Active: true, Removable: true,
				CustomFieldOptions: []zendesk.CustomFieldOption{{Name: "Opt A", Value: "opt_a"}}},
			wantOK: true, wantMulti: []string{"opt_a"},
		},
		{name: "checkbox", field: zendesk.TicketField{ID: 8, Type: "checkbox", Title: "T", Active: true, Removable: true}, wantOK: true},
		{name: "date", field: zendesk.TicketField{ID: 9, Type: "date", Title: "T", Active: true, Removable: true}, wantOK: true},
		{name: "integer skipped", field: zendesk.TicketField{ID: 10, Type: "integer", Title: "T", Active: true, Removable: true}, wantOK: false},
		{name: "decimal skipped", field: zendesk.TicketField{ID: 11, Type: "decimal", Title: "T", Active: true, Removable: true}, wantOK: false},
		{name: "unknown skipped", field: zendesk.TicketField{ID: 12, Type: "surprise", Title: "T", Active: true, Removable: true}, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cf, ok := schemaFieldForTicketField(ctx, tc.field)
			if ok != tc.wantOK {
				t.Fatalf("ok: expected %v, got %v", tc.wantOK, ok)
			}
			if !ok {
				return
			}
			if cf.GetId() == "" || cf.GetDisplayName() != "T" {
				t.Fatalf("expected id set and display name T, got %+v", cf)
			}
			if tc.wantPick != nil {
				vals := cf.GetPickStringValue().GetAllowedValues()
				if len(vals) != len(tc.wantPick) || vals[0] != tc.wantPick[0] {
					t.Fatalf("pick values: expected %v, got %v", tc.wantPick, vals)
				}
			}
			if tc.wantMulti != nil {
				vals := cf.GetPickMultipleStringValues().GetAllowedValues()
				if len(vals) != len(tc.wantMulti) || vals[0] != tc.wantMulti[0] {
					t.Fatalf("multi values: expected %v, got %v", tc.wantMulti, vals)
				}
			}
		})
	}
}

// TestSchemaForForm covers spec R3 field scoping + R5 schema contents.
func TestSchemaForForm(t *testing.T) {
	ctx := context.Background()
	fieldsByID := map[int64]zendesk.TicketField{
		1: {ID: 1, Type: "text", Title: "Custom Text", Active: true, Removable: true},
		2: {ID: 2, Type: "subject", Title: "Subject", Active: true, Removable: false}, // system field: excluded.
		3: {ID: 3, Type: "text", Title: "Inactive", Active: false, Removable: true},   // inactive: excluded.
		4: {ID: 4, Type: "integer", Title: "Number", Active: true, Removable: true},   // integer: skipped.
	}
	form := zendesk.TicketForm{ID: 77, Name: "HW Request", Active: true, TicketFieldIDs: []int64{1, 2, 3, 4, 999}}

	schema := schemaForForm(ctx, form, fieldsByID)
	if schema.GetId() != "77" || schema.GetDisplayName() != "HW Request" {
		t.Fatalf("expected id=77 name='HW Request', got %+v", schema)
	}
	cfs := schema.GetCustomFields()
	// Field 1 + synthetic priority + synthetic type.
	if len(cfs) != 3 {
		t.Fatalf("expected 3 custom fields (1 real + priority + type), got %d: %v", len(cfs), cfs)
	}
	if _, ok := cfs["1"]; !ok {
		t.Fatalf("expected custom field keyed by numeric id string \"1\"")
	}
	if _, ok := cfs[syntheticFieldPriority]; !ok {
		t.Fatalf("expected synthetic priority field")
	}
	if _, ok := cfs[syntheticFieldType]; !ok {
		t.Fatalf("expected synthetic type field")
	}
	wantStatuses := []string{"new", "open", "pending", "hold", "solved", "closed"}
	statuses := schema.GetStatuses()
	if len(statuses) != len(wantStatuses) {
		t.Fatalf("expected %d statuses, got %d", len(wantStatuses), len(statuses))
	}
	for i, want := range wantStatuses {
		if statuses[i].GetId() != want {
			t.Fatalf("status %d: expected %s, got %s", i, want, statuses[i].GetId())
		}
	}
	wantTypes := []string{"problem", "incident", "question", "task"}
	types := schema.GetTypes()
	if len(types) != len(wantTypes) {
		t.Fatalf("expected %d ticket types, got %d", len(wantTypes), len(types))
	}
	for i, want := range wantTypes {
		if types[i].GetId() != want {
			t.Fatalf("type %d: expected %s, got %s", i, want, types[i].GetId())
		}
	}
	prio := cfs[syntheticFieldPriority].GetPickStringValue().GetAllowedValues()
	if len(prio) != 4 || prio[0] != "low" || prio[3] != "urgent" {
		t.Fatalf("priority allowed values: expected [low normal high urgent], got %v", prio)
	}
	tt := cfs[syntheticFieldType].GetPickStringValue().GetAllowedValues()
	if len(tt) != 4 || tt[0] != "problem" || tt[3] != "task" {
		t.Fatalf("type allowed values: expected [problem incident question task], got %v", tt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/connector/ -run 'TestSchemaF' -v`
Expected: FAIL (compile error: `undefined: schemaFieldForTicketField`)

- [ ] **Step 3: Write minimal implementation**

Create `pkg/connector/ticket_schema.go`:

```go
package connector

import (
	"context"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	sdkTicket "github.com/conductorone/baton-sdk/pkg/types/ticket"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
)

const (
	// defaultSchemaID is the synthetic schema served when ticket forms are
	// unavailable (plan-gated 404) or no active form exists. Form IDs are
	// numeric, so this cannot collide.
	defaultSchemaID = "default"

	// Synthetic pick-string schema fields: the SDK builder does not forward
	// ticket.Type to CreateTicket, and priority is a Zendesk system field, so
	// both ride as schema custom fields and are extracted before the
	// custom_fields payload is built (never sent to Zendesk as custom fields).
	syntheticFieldPriority = "priority"
	syntheticFieldType     = "type"
)

// creatableTicketStatuses are the statuses Zendesk accepts at create time:
// closed is rejected outright and solved requires an assignee, which this
// connector never sets.
var creatableTicketStatuses = map[string]bool{
	"new":     true,
	"open":    true,
	"pending": true,
	"hold":    true,
}

// zendeskTicketStatuses returns all six system statuses. All six are listed
// because ConductorOne displays observed states; only creatableTicketStatuses
// may be requested at create time.
func zendeskTicketStatuses() []*v2.TicketStatus {
	return []*v2.TicketStatus{
		{Id: "new", DisplayName: "New"},
		{Id: "open", DisplayName: "Open"},
		{Id: "pending", DisplayName: "Pending"},
		{Id: "hold", DisplayName: "Hold"},
		{Id: "solved", DisplayName: "Solved"},
		{Id: "closed", DisplayName: "Closed"},
	}
}

// zendeskTicketTypes returns the four ticket types. Advertisement only: the
// create path reads the synthetic type custom field instead (see ticket.go).
func zendeskTicketTypes() []*v2.TicketType {
	return []*v2.TicketType{
		{Id: "problem", DisplayName: "Problem"},
		{Id: "incident", DisplayName: "Incident"},
		{Id: "question", DisplayName: "Question"},
		{Id: "task", DisplayName: "Task"},
	}
}

func syntheticSchemaFields() map[string]*v2.TicketCustomField {
	return map[string]*v2.TicketCustomField{
		syntheticFieldPriority: sdkTicket.PickStringFieldSchema(syntheticFieldPriority, "Priority", false,
			[]string{"low", "normal", "high", "urgent"}),
		syntheticFieldType: sdkTicket.PickStringFieldSchema(syntheticFieldType, "Type", false,
			[]string{"problem", "incident", "question", "task"}),
	}
}

// schemaFieldForTicketField maps one Zendesk custom ticket field to an SDK
// schema field per the spec R6 table. Returns false for field types that are
// deliberately skipped (integer/decimal: ConductorOne number-field rendering
// is unproven; unknown types: omit, don't error).
func schemaFieldForTicketField(ctx context.Context, f zendesk.TicketField) (*v2.TicketCustomField, bool) {
	id := strconv.FormatInt(f.ID, 10)
	switch f.Type {
	case "text", "textarea", "regexp", "partialcreditcard", "lookup":
		return sdkTicket.StringFieldSchema(id, f.Title, f.Required), true
	case "tagger":
		return sdkTicket.PickStringFieldSchema(id, f.Title, f.Required, customFieldOptionValues(f)), true
	case "multiselect":
		return sdkTicket.PickMultipleStringsFieldSchema(id, f.Title, f.Required, customFieldOptionValues(f)), true
	case "checkbox":
		return sdkTicket.BoolFieldSchema(id, f.Title, f.Required), true
	case "date":
		return sdkTicket.TimestampFieldSchema(id, f.Title, f.Required), true
	default:
		ctxzap.Extract(ctx).Debug("baton-zendesk: skipping unsupported ticket field type",
			zap.String("field_type", f.Type), zap.Int64("field_id", f.ID))
		return nil, false
	}
}

func customFieldOptionValues(f zendesk.TicketField) []string {
	values := make([]string, 0, len(f.CustomFieldOptions))
	for _, o := range f.CustomFieldOptions {
		values = append(values, o.Value)
	}
	return values
}

// includeTicketField reports whether a ticket field belongs in a schema:
// active custom fields only (system fields are handled first-class).
func includeTicketField(f zendesk.TicketField) bool {
	return f.Active && f.Removable
}

func buildSchema(ctx context.Context, id, displayName string, fields []zendesk.TicketField) *v2.TicketSchema {
	customFields := syntheticSchemaFields()
	for _, f := range fields {
		if !includeTicketField(f) {
			continue
		}
		cf, ok := schemaFieldForTicketField(ctx, f)
		if !ok {
			continue
		}
		customFields[cf.GetId()] = cf
	}
	return &v2.TicketSchema{
		Id:           id,
		DisplayName:  displayName,
		Types:        zendeskTicketTypes(),
		Statuses:     zendeskTicketStatuses(),
		CustomFields: customFields,
	}
}

// schemaForForm builds the schema for one active ticket form: the form's
// ticket_field_ids resolved against fieldsByID. IDs missing from the map are
// skipped silently (Zendesk allows stale references).
func schemaForForm(ctx context.Context, form zendesk.TicketForm, fieldsByID map[int64]zendesk.TicketField) *v2.TicketSchema {
	fields := make([]zendesk.TicketField, 0, len(form.TicketFieldIDs))
	for _, fid := range form.TicketFieldIDs {
		if f, ok := fieldsByID[fid]; ok {
			fields = append(fields, f)
		}
	}
	return buildSchema(ctx, strconv.FormatInt(form.ID, 10), form.Name, fields)
}

// defaultTicketSchema builds the fallback schema from every active custom
// ticket field (spec R4).
func defaultTicketSchema(ctx context.Context, fields []zendesk.TicketField) *v2.TicketSchema {
	return buildSchema(ctx, defaultSchemaID, "Default", fields)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/connector/ -run 'TestSchemaF' -v`
Expected: PASS (all subtests)

- [ ] **Step 5: Commit**

```bash
git add pkg/connector/ticket_schema.go pkg/connector/ticket_schema_test.go
git commit -m "feat: add ticket schema construction and field mapping"
```

---

### Task 6: `ListTicketSchemas` + `GetTicketSchema` + fallback (`ticket.go`, part 1)

**Files:**
- Create: `pkg/connector/ticket.go`
- Test: `pkg/connector/ticket_test.go`

**Interfaces:**
- Consumes: T3/T4 client drains; T5 schema builders; `pagination.Token`; go-zendesk `GetTicketForm(ctx, id)` via `d.zendeskClient.GetZendeskClient()`.
- Produces (SDK interface methods on `*Connector`, used by T7-T9):
  - `func (d *Connector) ListTicketSchemas(ctx context.Context, pToken *pagination.Token) ([]*v2.TicketSchema, string, annotations.Annotations, error)`
  - `func (d *Connector) GetTicketSchema(ctx context.Context, schemaID string) (*v2.TicketSchema, annotations.Annotations, error)`
  - helper `func isTicketFormsPlanGate(err error) bool`

- [ ] **Step 1: Write the failing test**

Create `pkg/connector/ticket_test.go`:

```go
package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ticketTestFixture is one httptest Zendesk hosting forms/fields/tickets
// endpoints, configurable per scenario. formsPages serves real offset
// pagination: element i answers page i+1, next_page set while more remain.
type ticketTestFixture struct {
	formsStatus int      // non-zero: /ticket_forms.json returns this HTTP status.
	formsJSON   string   // single-page body, used when formsStatus == 0 and formsPages is nil.
	formsPages  []string // multi-page bodies WITHOUT next_page/count keys (raw ticket_forms arrays).
	fieldsJSON  string
}

func newTicketTestConnector(t *testing.T, fx ticketTestFixture) *Connector {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ticket_forms.json", func(w http.ResponseWriter, r *http.Request) {
		if fx.formsStatus != 0 {
			http.Error(w, `{"error":"plan"}`, fx.formsStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if fx.formsPages == nil {
			_, _ = w.Write([]byte(fx.formsJSON))
			return
		}
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		next := "null"
		if page < len(fx.formsPages) {
			next = `"next"`
		}
		body := `{"ticket_forms":` + fx.formsPages[page-1] + `,"next_page":` + next + `,"count":2}`
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("GET /ticket_forms/77.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket_form":{"id":77,"name":"HW Request","active":true,"ticket_field_ids":[1]}}`))
	})
	mux.HandleFunc("GET /ticket_forms/88.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket_form":{"id":88,"name":"Retired","active":false,"ticket_field_ids":[1]}}`))
	})
	mux.HandleFunc("GET /ticket_fields.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fx.fieldsJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	zc, err := client.New(context.Background(), nil, "", "test@example.com", "token", srv.URL)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return &Connector{zendeskClient: zc}
}

const testFieldsJSON = `{"ticket_fields":[
	{"id":1,"type":"text","title":"Custom Text","active":true,"removable":true},
	{"id":2,"type":"subject","title":"Subject","active":true,"removable":false},
	{"id":3,"type":"text","title":"Other Text","active":true,"removable":true}
],"meta":{"has_more":false,"after_cursor":""}}`

// TestListTicketSchemasFormsMode covers spec R3 accept (a): two active forms
// with different field sets yield two schemas, each scoped to its own form's
// active custom fields; the inactive form yields none.
func TestListTicketSchemasFormsMode(t *testing.T) {
	c := newTicketTestConnector(t, ticketTestFixture{
		formsJSON: `{"ticket_forms":[
			{"id":77,"name":"HW Request","active":true,"ticket_field_ids":[1,2]},
			{"id":99,"name":"SW Request","active":true,"ticket_field_ids":[3]},
			{"id":88,"name":"Retired","active":false,"ticket_field_ids":[1]}
		],"next_page":null,"count":3}`,
		fieldsJSON: testFieldsJSON,
	})
	schemas, next, _, err := c.ListTicketSchemas(context.Background(), &pagination.Token{})
	if err != nil {
		t.Fatalf("ListTicketSchemas: %v", err)
	}
	if next != "" {
		t.Fatalf("expected empty next token, got %q", next)
	}
	if len(schemas) != 2 || schemas[0].GetId() != "77" || schemas[1].GetId() != "99" {
		t.Fatalf("expected schemas [77 99] for the two active forms, got %+v", schemas)
	}
	// Per-form field scoping: 77 carries field 1 (2 is a system field), 99
	// carries field 3 — plus the two synthetic fields each.
	cf77 := schemas[0].GetCustomFields()
	if _, ok := cf77["1"]; !ok || len(cf77) != 3 {
		t.Fatalf("schema 77: expected fields {1, priority, type}, got %v", cf77)
	}
	cf99 := schemas[1].GetCustomFields()
	if _, ok := cf99["3"]; !ok || len(cf99) != 3 {
		t.Fatalf("schema 99: expected fields {3, priority, type}, got %v", cf99)
	}
	if _, ok := cf99["1"]; ok {
		t.Fatalf("schema 99 must not carry form 77's field 1")
	}
}

func TestListTicketSchemasFallbackOn404(t *testing.T) {
	c := newTicketTestConnector(t, ticketTestFixture{formsStatus: http.StatusNotFound, fieldsJSON: testFieldsJSON})
	schemas, _, _, err := c.ListTicketSchemas(context.Background(), &pagination.Token{})
	if err != nil {
		t.Fatalf("ListTicketSchemas: %v", err)
	}
	if len(schemas) != 1 || schemas[0].GetId() != defaultSchemaID {
		t.Fatalf("expected single default schema, got %+v", schemas)
	}
}

func TestListTicketSchemasFallbackOnZeroActive(t *testing.T) {
	c := newTicketTestConnector(t, ticketTestFixture{
		formsJSON:  `{"ticket_forms":[{"id":88,"name":"Retired","active":false,"ticket_field_ids":[1]}],"next_page":null,"count":1}`,
		fieldsJSON: testFieldsJSON,
	})
	schemas, _, _, err := c.ListTicketSchemas(context.Background(), &pagination.Token{})
	if err != nil {
		t.Fatalf("ListTicketSchemas: %v", err)
	}
	if len(schemas) != 1 || schemas[0].GetId() != defaultSchemaID {
		t.Fatalf("expected single default schema, got %+v", schemas)
	}
}

// TestListTicketSchemasDrainsPagesBeforeFallback proves the zero-active-forms
// fallback decision spans ALL form pages (spec R3 accept b / R4): page 1 is
// all-inactive, page 2 has the only active form — the result must be exactly
// that form's schema and never the default schema.
func TestListTicketSchemasDrainsPagesBeforeFallback(t *testing.T) {
	c := newTicketTestConnector(t, ticketTestFixture{
		formsPages: []string{
			`[{"id":88,"name":"Retired","active":false,"ticket_field_ids":[1]}]`,
			`[{"id":77,"name":"HW Request","active":true,"ticket_field_ids":[1,2]}]`,
		},
		fieldsJSON: testFieldsJSON,
	})
	schemas, _, _, err := c.ListTicketSchemas(context.Background(), &pagination.Token{})
	if err != nil {
		t.Fatalf("ListTicketSchemas: %v", err)
	}
	if len(schemas) != 1 || schemas[0].GetId() != "77" {
		t.Fatalf("expected exactly the page-2 active form schema 77 (no default), got %+v", schemas)
	}
}

func TestListTicketSchemasErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		httpStatus int
	}{
		{"403 propagates", http.StatusForbidden},
		{"429 propagates", http.StatusTooManyRequests},
		{"500 propagates", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTicketTestConnector(t, ticketTestFixture{formsStatus: tc.httpStatus, fieldsJSON: testFieldsJSON})
			_, _, _, err := c.ListTicketSchemas(context.Background(), &pagination.Token{})
			if err == nil {
				t.Fatalf("expected error for HTTP %d, got fallback/nil", tc.httpStatus)
			}
		})
	}
}

func TestGetTicketSchema(t *testing.T) {
	c := newTicketTestConnector(t, ticketTestFixture{formsJSON: `{"ticket_forms":[],"next_page":null,"count":0}`, fieldsJSON: testFieldsJSON})

	schema, _, err := c.GetTicketSchema(context.Background(), defaultSchemaID)
	if err != nil || schema.GetId() != defaultSchemaID {
		t.Fatalf("default schema: expected ok, got schema=%v err=%v", schema, err)
	}

	schema, _, err = c.GetTicketSchema(context.Background(), "77")
	if err != nil || schema.GetDisplayName() != "HW Request" {
		t.Fatalf("form schema: expected HW Request, got schema=%v err=%v", schema, err)
	}

	_, _, err = c.GetTicketSchema(context.Background(), "88")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("inactive form: expected NotFound, got %v", err)
	}

	_, _, err = c.GetTicketSchema(context.Background(), "not-a-number")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad id: expected InvalidArgument, got %v", err)
	}
}
```

(The import block above is exactly what T6's code needs; Task 7 adds `encoding/json` and other imports alongside the code that first uses them.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/connector/ -run 'TestListTicketSchemas|TestGetTicketSchema' -v`
Expected: FAIL (compile error: `c.ListTicketSchemas undefined`)

- [ ] **Step 3: Write minimal implementation**

Create `pkg/connector/ticket.go`:

```go
package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isTicketFormsPlanGate classifies a forms-fetch failure as the plan-gate
// signal (endpoint absent on plans without ticket forms). Classification is on
// the raw HTTP status via errors.As — wrapZendeskError joins the original
// zendesk.Error into the chain — never on wrapped gRPC codes. 401/403 are
// deliberately NOT plan-gate signals: token/scope problems must surface
// loudly, not be masked by a degraded schema (spec R4).
func isTicketFormsPlanGate(err error) bool {
	var zErr zendesk.Error
	return errors.As(err, &zErr) && zErr.Status() == http.StatusNotFound
}

// ListTicketSchemas returns one schema per active ticket form, or the single
// default schema when forms are unavailable (spec R3/R4). The client drains
// all form pages internally (Zendesk caps accounts at 300 forms), so the
// whole list is returned in one call with an empty next-page token.
func (d *Connector) ListTicketSchemas(ctx context.Context, _ *pagination.Token) ([]*v2.TicketSchema, string, annotations.Annotations, error) {
	fields, err := d.zendeskClient.ListAllTicketFields(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-zendesk: failed to list ticket fields: %w", err)
	}

	forms, err := d.zendeskClient.ListAllTicketForms(ctx)
	if err != nil {
		if !isTicketFormsPlanGate(err) {
			return nil, "", nil, fmt.Errorf("baton-zendesk: failed to list ticket forms: %w", err)
		}
		ctxzap.Extract(ctx).Warn("baton-zendesk: ticket forms endpoint unavailable (plan-gated), serving default schema", zap.Error(err))
		return []*v2.TicketSchema{defaultTicketSchema(ctx, fields)}, "", nil, nil
	}

	fieldsByID := ticketFieldsByID(fields)
	schemas := make([]*v2.TicketSchema, 0, len(forms))
	for _, form := range forms {
		if !form.Active {
			continue
		}
		schemas = append(schemas, schemaForForm(ctx, form, fieldsByID))
	}
	if len(schemas) == 0 {
		ctxzap.Extract(ctx).Warn("baton-zendesk: no active ticket forms, serving default schema")
		return []*v2.TicketSchema{defaultTicketSchema(ctx, fields)}, "", nil, nil
	}
	return schemas, "", nil, nil
}

// GetTicketSchema rebuilds one schema: the default schema by its sentinel ID,
// or a single active form by numeric ID (spec R10).
func (d *Connector) GetTicketSchema(ctx context.Context, schemaID string) (*v2.TicketSchema, annotations.Annotations, error) {
	fields, err := d.zendeskClient.ListAllTicketFields(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list ticket fields: %w", err)
	}

	if schemaID == defaultSchemaID {
		return defaultTicketSchema(ctx, fields), nil, nil
	}

	formID, err := strconv.ParseInt(schemaID, 10, 64)
	if err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-zendesk: invalid ticket schema id %q", schemaID)
	}
	form, err := d.zendeskClient.GetTicketForm(ctx, formID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, nil, status.Errorf(codes.NotFound, "baton-zendesk: ticket form %d not found", formID)
		}
		return nil, nil, fmt.Errorf("baton-zendesk: failed to get ticket form %d: %w", formID, err)
	}
	if !form.Active {
		return nil, nil, status.Errorf(codes.NotFound, "baton-zendesk: ticket form %d is inactive", formID)
	}
	return schemaForForm(ctx, form, ticketFieldsByID(fields)), nil, nil
}

func ticketFieldsByID(fields []zendesk.TicketField) map[int64]zendesk.TicketField {
	byID := make(map[int64]zendesk.TicketField, len(fields))
	for _, f := range fields {
		byID[f.ID] = f
	}
	return byID
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/connector/ -run 'TestListTicketSchemas|TestGetTicketSchema' -v`
Expected: PASS (all subtests, including 403/429/500 propagation)

- [ ] **Step 5: Commit**

```bash
git add pkg/connector/ticket.go pkg/connector/ticket_test.go
git commit -m "feat: add ticket schema listing with plan-gate fallback"
```

---

### Task 7: `CreateTicket` + `GetTicket` (`ticket.go`, part 2)

**Files:**
- Modify: `pkg/connector/ticket.go`
- Modify: `pkg/connector/ticket_test.go`

**Interfaces:**
- Consumes: T2 client `CreateTicket/GetTicket` + T1 types; `sdkTicket.ValidateTicket` (invalid signaled via the **bool**: `(false, nil)` for nearly all invalid paths), `sdkTicket.Get*Value` getters, `sdkTicket.ErrTicketValidationError`.
- Produces (used by T8/T9):
  - `func (d *Connector) CreateTicket(ctx context.Context, ticket *v2.Ticket, schema *v2.TicketSchema) (*v2.Ticket, annotations.Annotations, error)`
  - `func (d *Connector) GetTicket(ctx context.Context, ticketID string) (*v2.Ticket, annotations.Annotations, error)`

- [ ] **Step 1: Write the failing test**

Append to `pkg/connector/ticket_test.go`:

```go
func createTicketFixtureConnector(t *testing.T, capture *client.Ticket) *Connector {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets.json", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ticket client.Ticket `json:"ticket"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*capture = req.Ticket
		req.Ticket.ID = 555
		req.Ticket.Status = "new"
		req.Ticket.URL = "http://api/tickets/555.json"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": req.Ticket})
	})
	mux.HandleFunc("GET /tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket":{"id":555,"subject":"Access to prod DB","status":"solved",
			"url":"http://api/tickets/555.json",
			"created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-02T11:30:00Z"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	zc, err := client.New(context.Background(), nil, "", "test@example.com", "token", srv.URL)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return &Connector{zendeskClient: zc}
}

func testSchema(t *testing.T) *v2.TicketSchema {
	t.Helper()
	fieldsByID := map[int64]zendesk.TicketField{
		1: {ID: 1, Type: "tagger", Title: "Env", Active: true, Removable: true,
			CustomFieldOptions: []zendesk.CustomFieldOption{{Name: "Prod", Value: "prod"}}},
		2: {ID: 2, Type: "checkbox", Title: "Urgent", Active: true, Removable: true},
		3: {ID: 3, Type: "date", Title: "Due", Active: true, Removable: true},
	}
	form := zendesk.TicketForm{ID: 77, Name: "HW Request", Active: true, TicketFieldIDs: []int64{1, 2, 3}}
	return schemaForForm(context.Background(), form, fieldsByID)
}

func TestCreateTicketMapping(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	schema := testSchema(t)

	due := time.Date(2026, 8, 1, 23, 30, 0, 0, time.FixedZone("UTC+9", 9*3600))
	ticket := &v2.Ticket{
		DisplayName: "Access to prod DB",
		Description: "please grant",
		Labels:      []string{"c1", "access-request"},
		RequestedFor: &v2.Resource{
			Id: &v2.ResourceId{ResourceType: "team_member", Resource: "101"},
		},
		Status: &v2.TicketStatus{Id: "open"},
		CustomFields: map[string]*v2.TicketCustomField{
			"1":                    sdkTicket.PickStringField("1", "prod"),
			"2":                    sdkTicket.BoolField("2", true),
			"3":                    sdkTicket.TimestampField("3", due),
			syntheticFieldPriority: sdkTicket.PickStringField(syntheticFieldPriority, "high"),
			syntheticFieldType:     sdkTicket.PickStringField(syntheticFieldType, "task"),
		},
	}

	created, _, err := c.CreateTicket(context.Background(), ticket, schema)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.GetId() != "555" {
		t.Fatalf("expected created id 555, got %q", created.GetId())
	}

	// Payload assertions (spec R7/R8).
	if captured.Subject != "Access to prod DB" || captured.Comment == nil || captured.Comment.Body != "please grant" {
		t.Fatalf("subject/comment: got %+v", captured)
	}
	if captured.TicketFormID != 77 || captured.RequesterID != 101 || captured.Status != "open" {
		t.Fatalf("form/requester/status: got %+v", captured)
	}
	if captured.Priority != "high" || captured.Type != "task" {
		t.Fatalf("synthetic fields: expected priority=high type=task, got %+v", captured)
	}
	if len(captured.Tags) != 2 {
		t.Fatalf("tags: got %+v", captured.Tags)
	}
	if len(captured.CustomFields) != 3 {
		t.Fatalf("expected 3 custom fields (priority/type extracted), got %+v", captured.CustomFields)
	}
	values := map[int64]any{}
	for _, cf := range captured.CustomFields {
		values[cf.ID] = cf.Value
	}
	if values[1] != "prod" || values[2] != true {
		t.Fatalf("custom field values: got %+v", values)
	}
	// Date formatted in UTC: 23:30 UTC+9 is 14:30 UTC on the same day.
	if values[3] != "2026-08-01" {
		t.Fatalf("date value: expected 2026-08-01, got %v", values[3])
	}
}

func TestCreateTicketRejectsSolvedClosed(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	for _, badStatus := range []string{"solved", "closed"} {
		ticket := &v2.Ticket{
			DisplayName: "t", Description: "d",
			Status: &v2.TicketStatus{Id: badStatus},
		}
		_, _, err := c.CreateTicket(context.Background(), ticket, testSchema(t))
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("status %s: expected InvalidArgument, got %v", badStatus, err)
		}
	}
}

func TestCreateTicketValidationFailure(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	schema := testSchema(t)
	// Pick value outside allowedValues: ValidateTicket returns (false, nil).
	ticket := &v2.Ticket{
		DisplayName: "t", Description: "d",
		CustomFields: map[string]*v2.TicketCustomField{
			"1": sdkTicket.PickStringField("1", "not-an-option"),
		},
	}
	_, _, err := c.CreateTicket(context.Background(), ticket, schema)
	if err == nil || !errors.Is(err, sdkTicket.ErrTicketValidationError) {
		t.Fatalf("expected ErrTicketValidationError, got %v", err)
	}
}

func TestCreateTicketRequesterParseFailure(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	ticket := &v2.Ticket{
		DisplayName: "t", Description: "d",
		RequestedFor: &v2.Resource{Id: &v2.ResourceId{ResourceType: "team_member", Resource: "not-numeric"}},
	}
	_, _, err := c.CreateTicket(context.Background(), ticket, testSchema(t))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestCreateTicketEmptySubjectAndDescription(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	_, _, err := c.CreateTicket(context.Background(), &v2.Ticket{}, testSchema(t))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty subject+description, got %v", err)
	}
}

func TestGetTicketCompletion(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	got, _, err := c.GetTicket(context.Background(), "555")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.GetStatus().GetId() != "solved" {
		t.Fatalf("expected solved, got %+v", got.GetStatus())
	}
	if got.GetCompletedAt() == nil {
		t.Fatalf("expected CompletedAt set for solved ticket")
	}
	if got.GetCompletedAt().AsTime() != got.GetUpdatedAt().AsTime() {
		t.Fatalf("CompletedAt should equal UpdatedAt")
	}
	// subdomain empty (base-url test mode): URL falls back to the API url.
	if got.GetUrl() != "http://api/tickets/555.json" {
		t.Fatalf("expected API url fallback, got %q", got.GetUrl())
	}

	_, _, err = c.GetTicket(context.Background(), "not-numeric")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad id, got %v", err)
	}
}

// TestCustomFieldWireValue covers every row of the spec R8 mapping table,
// including omission of empty/unset values.
func TestCustomFieldWireValue(t *testing.T) {
	due := time.Date(2026, 8, 1, 23, 30, 0, 0, time.FixedZone("UTC+9", 9*3600))
	tests := []struct {
		name    string
		field   *v2.TicketCustomField
		want    any
		wantOK  bool
		wantErr bool
	}{
		{name: "string", field: sdkTicket.StringField("1", "hello"), want: "hello", wantOK: true},
		{name: "empty string omitted", field: sdkTicket.StringField("1", ""), wantOK: false},
		{name: "pick string", field: sdkTicket.PickStringField("1", "prod"), want: "prod", wantOK: true},
		{name: "pick multiple", field: sdkTicket.PickMultipleStringsField("1", []string{"a", "b"}), wantOK: true},
		{name: "strings", field: sdkTicket.StringsField("1", []string{"x"}), wantOK: true},
		{name: "bool", field: sdkTicket.BoolField("1", false), want: false, wantOK: true},
		{name: "number", field: sdkTicket.NumberField("1", 42), want: float64(42), wantOK: true},
		{name: "timestamp utc date", field: sdkTicket.TimestampField("1", due), want: "2026-08-01", wantOK: true},
		{name: "unset value omitted", field: &v2.TicketCustomField{Id: "1"}, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := customFieldWireValue(tc.field)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err: expected wantErr=%v, got %v", tc.wantErr, err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok: expected %v, got %v (value %#v)", tc.wantOK, ok, got)
			}
			if tc.want != nil && got != tc.want {
				t.Fatalf("value: expected %#v, got %#v", tc.want, got)
			}
		})
	}
}

func TestAgentTicketURL(t *testing.T) {
	c := &Connector{subdomain: "acme"}
	if got := c.agentTicketURL(555, "http://api/x.json"); got != "https://acme.zendesk.com/agent/tickets/555" {
		t.Fatalf("expected agent URL, got %q", got)
	}
	c = &Connector{}
	if got := c.agentTicketURL(555, "http://api/x.json"); got != "http://api/x.json" {
		t.Fatalf("expected API url fallback, got %q", got)
	}
}
```

Add to the test file's imports: `"encoding/json"` (used by `createTicketFixtureConnector`), `"errors"`, `"time"`, `v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"`, `sdkTicket "github.com/conductorone/baton-sdk/pkg/types/ticket"`, `"github.com/nukosuke/go-zendesk/zendesk"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/connector/ -run 'TestCreateTicket|TestGetTicketCompletion|TestAgentTicketURL' -v`
Expected: FAIL (compile error: `c.CreateTicket undefined`)

- [ ] **Step 3: Write minimal implementation**

Append to `pkg/connector/ticket.go` (add imports: `sdkTicket "github.com/conductorone/baton-sdk/pkg/types/ticket"`, `"github.com/conductorone/baton-zendesk/pkg/client"`, `"google.golang.org/protobuf/types/known/timestamppb"` — no `time` import is needed; the timestamp value's `UTC()/Format/IsZero` are methods on the value):

```go
// CreateTicket validates the request against the schema and creates the
// Zendesk ticket (spec R7/R8). The SDK forwards only DisplayName, Description,
// Status, Labels, CustomFields, and RequestedFor — ticket type rides in the
// synthetic type custom field.
func (d *Connector) CreateTicket(ctx context.Context, ticket *v2.Ticket, schema *v2.TicketSchema) (*v2.Ticket, annotations.Annotations, error) {
	valid, err := sdkTicket.ValidateTicket(ctx, schema, ticket)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: ticket validation failed: %w", err)
	}
	if !valid {
		return nil, nil, errors.Join(errors.New("baton-zendesk: ticket is invalid for schema"), sdkTicket.ErrTicketValidationError)
	}

	payload, err := d.buildCreatePayload(ticket, schema)
	if err != nil {
		return nil, nil, err
	}

	created, err := d.zendeskClient.CreateTicket(ctx, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to create ticket: %w", err)
	}
	return d.zendeskTicketToSDK(created), nil, nil
}

// GetTicket fetches one ticket for status polling (spec R9).
func (d *Connector) GetTicket(ctx context.Context, ticketID string) (*v2.Ticket, annotations.Annotations, error) {
	id, err := strconv.ParseInt(ticketID, 10, 64)
	if err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-zendesk: invalid ticket id %q", ticketID)
	}
	t, err := d.zendeskClient.GetTicket(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to get ticket %d: %w", id, err)
	}
	return d.zendeskTicketToSDK(t), nil, nil
}

func (d *Connector) buildCreatePayload(ticket *v2.Ticket, schema *v2.TicketSchema) (client.Ticket, error) {
	subject := ticket.GetDisplayName()
	body := ticket.GetDescription()
	if body == "" {
		body = subject
	}
	if body == "" {
		return client.Ticket{}, status.Error(codes.InvalidArgument, "baton-zendesk: ticket requires a subject or description")
	}

	payload := client.Ticket{
		Subject: subject,
		Comment: &client.TicketComment{Body: body},
		Tags:    ticket.GetLabels(),
	}

	if schema.GetId() != defaultSchemaID {
		formID, err := strconv.ParseInt(schema.GetId(), 10, 64)
		if err != nil {
			return client.Ticket{}, status.Errorf(codes.InvalidArgument, "baton-zendesk: invalid schema id %q", schema.GetId())
		}
		payload.TicketFormID = formID
	}

	if rf := ticket.GetRequestedFor(); rf != nil {
		requesterID, err := strconv.ParseInt(rf.GetId().GetResource(), 10, 64)
		if err != nil {
			return client.Ticket{}, status.Errorf(codes.InvalidArgument,
				"baton-zendesk: requested_for resource id %q is not a zendesk user id", rf.GetId().GetResource())
		}
		payload.RequesterID = requesterID
	}

	if s := ticket.GetStatus().GetId(); s != "" {
		if !creatableTicketStatuses[s] {
			return client.Ticket{}, status.Errorf(codes.InvalidArgument,
				"baton-zendesk: status %q cannot be set at create time (closed is rejected by zendesk; solved requires an assignee)", s)
		}
		payload.Status = s
	}

	customFields, priority, ticketType, err := mapCustomFieldValues(ticket.GetCustomFields())
	if err != nil {
		return client.Ticket{}, err
	}
	payload.CustomFields = customFields
	payload.Priority = priority
	payload.Type = ticketType
	return payload, nil
}

// mapCustomFieldValues reverse-maps SDK custom-field values to the Zendesk
// wire shape by a pure type-switch (spec R8) and extracts the synthetic
// priority/type fields.
func mapCustomFieldValues(fields map[string]*v2.TicketCustomField) ([]client.CustomField, string, string, error) {
	var (
		out      []client.CustomField
		priority string
		tType    string
	)
	for key, cf := range fields {
		switch key {
		case syntheticFieldPriority:
			v, err := sdkTicket.GetPickStringValue(cf)
			if err == nil && v != "" {
				priority = v
			}
			continue
		case syntheticFieldType:
			v, err := sdkTicket.GetPickStringValue(cf)
			if err == nil && v != "" {
				tType = v
			}
			continue
		}

		fieldID, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return nil, "", "", status.Errorf(codes.InvalidArgument, "baton-zendesk: custom field key %q is not a zendesk field id", key)
		}

		value, ok, err := customFieldWireValue(cf)
		if err != nil {
			return nil, "", "", err
		}
		if !ok {
			continue
		}
		out = append(out, client.CustomField{ID: fieldID, Value: value})
	}
	return out, priority, tType, nil
}

func customFieldWireValue(cf *v2.TicketCustomField) (any, bool, error) {
	switch cf.GetValue().(type) {
	case *v2.TicketCustomField_StringValue:
		v, err := sdkTicket.GetStringValue(cf)
		return v, err == nil && v != "", nil
	case *v2.TicketCustomField_PickStringValue:
		v, err := sdkTicket.GetPickStringValue(cf)
		return v, err == nil && v != "", nil
	case *v2.TicketCustomField_PickMultipleStringValues:
		v, err := sdkTicket.GetPickMultipleStringValues(cf)
		return v, err == nil && len(v) > 0, nil
	case *v2.TicketCustomField_StringValues:
		v, err := sdkTicket.GetStringsValue(cf)
		return v, err == nil && len(v) > 0, nil
	case *v2.TicketCustomField_BoolValue:
		v, err := sdkTicket.GetBoolValue(cf)
		return v, err == nil, nil
	case *v2.TicketCustomField_NumberValue:
		// Defensive completeness: v1 schemas never produce number fields.
		v, err := sdkTicket.GetNumberValue(cf)
		return float64(v), err == nil, nil
	case *v2.TicketCustomField_TimestampValue:
		v, err := sdkTicket.GetTimestampValue(cf)
		if err != nil || v.IsZero() {
			return nil, false, nil
		}
		// date is Zendesk's only temporal custom-field type; format in UTC so
		// the date is deterministic.
		return v.UTC().Format("2006-01-02"), true, nil
	case nil:
		return nil, false, nil
	default:
		return nil, false, status.Errorf(codes.InvalidArgument,
			"baton-zendesk: unsupported custom field value type for field %q", cf.GetId())
	}
}

// completedTicketStatuses drive CompletedAt (spec R9): status is the
// completion signal, updated_at the (upper-bound) completion timestamp.
var completedTicketStatuses = map[string]bool{"solved": true, "closed": true}

func (d *Connector) zendeskTicketToSDK(t client.Ticket) *v2.Ticket {
	ret := &v2.Ticket{
		Id:          strconv.FormatInt(t.ID, 10),
		DisplayName: t.Subject,
		Description: t.Description,
		Labels:      t.Tags,
		Url:         d.agentTicketURL(t.ID, t.URL),
	}
	if t.Status != "" {
		ret.Status = &v2.TicketStatus{Id: t.Status, DisplayName: titleCase(t.Status)}
	}
	if t.CreatedAt != nil {
		ret.CreatedAt = timestamppb.New(*t.CreatedAt)
	}
	if t.UpdatedAt != nil {
		ret.UpdatedAt = timestamppb.New(*t.UpdatedAt)
		if completedTicketStatuses[t.Status] {
			ret.CompletedAt = timestamppb.New(*t.UpdatedAt)
		}
	}
	return ret
}

// agentTicketURL prefers the human-facing agent URL; with no subdomain
// (base-url test mode) it falls back to the API url field.
func (d *Connector) agentTicketURL(ticketID int64, apiURL string) string {
	if d.subdomain == "" {
		return apiURL
	}
	return fmt.Sprintf("https://%s.zendesk.com/agent/tickets/%d", d.subdomain, ticketID)
}

```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/connector/ -count=1`
Expected: PASS (all connector tests, old and new)

- [ ] **Step 5: Commit**

```bash
git add pkg/connector/ticket.go pkg/connector/ticket_test.go
git commit -m "feat: add ticket create and get with completion mapping"
```

---

### Task 8: Bulk methods

**Files:**
- Modify: `pkg/connector/ticket.go`
- Modify: `pkg/connector/ticket_test.go`

**Interfaces:**
- Consumes: T7 `CreateTicket`/`GetTicket`; `annotations.Annotations.Merge` (nil-receiver-safe).
- Produces: `BulkCreateTickets`, `BulkGetTickets` on `*Connector` — completes `TicketManagerLimited` (asserted in T9).

- [ ] **Step 1: Write the failing test**

Append to `pkg/connector/ticket_test.go`:

```go
func TestBulkCreateTickets(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	resp, err := c.BulkCreateTickets(context.Background(), &v2.TicketsServiceBulkCreateTicketsRequest{
		TicketRequests: []*v2.TicketsServiceCreateTicketRequest{
			{
				Request: &v2.TicketRequest{DisplayName: "good", Description: "d"},
				Schema:  testSchema(t),
			},
			{
				// Empty subject+description fails buildCreatePayload per item.
				Request: &v2.TicketRequest{},
				Schema:  testSchema(t),
			},
		},
	})
	if err != nil {
		t.Fatalf("BulkCreateTickets: per-item failures must not fail the batch: %v", err)
	}
	items := resp.GetTickets()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].GetError() != "" || items[0].GetTicket().GetId() != "555" {
		t.Fatalf("item 0: expected success, got %+v", items[0])
	}
	if items[1].GetError() == "" {
		t.Fatalf("item 1: expected per-item error, got %+v", items[1])
	}
}

func TestBulkGetTickets(t *testing.T) {
	var captured client.Ticket
	c := createTicketFixtureConnector(t, &captured)
	resp, err := c.BulkGetTickets(context.Background(), &v2.TicketsServiceBulkGetTicketsRequest{
		TicketRequests: []*v2.TicketsServiceGetTicketRequest{
			{Id: "555"},
			{Id: "not-numeric"},
		},
	})
	if err != nil {
		t.Fatalf("BulkGetTickets: per-item failures must not fail the batch: %v", err)
	}
	items := resp.GetTickets()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].GetError() != "" || items[0].GetTicket().GetId() != "555" {
		t.Fatalf("item 0: expected success for 555, got %+v", items[0])
	}
	if items[1].GetError() == "" {
		t.Fatalf("item 1: expected per-item error, got %+v", items[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/connector/ -run 'TestBulk' -v`
Expected: FAIL (compile error: `c.BulkGetTickets undefined`)

- [ ] **Step 3: Write minimal implementation**

Append to `pkg/connector/ticket.go` — this is the cross-connector boilerplate (jira/servicenow/freshservice), kept identical on purpose:

```go
// BulkCreateTickets creates tickets one by one; per-item failures land in the
// item's Error field so one bad ticket never fails the batch (spec R11).
func (d *Connector) BulkCreateTickets(ctx context.Context, request *v2.TicketsServiceBulkCreateTicketsRequest) (*v2.TicketsServiceBulkCreateTicketsResponse, error) {
	tickets := make([]*v2.TicketsServiceCreateTicketResponse, 0, len(request.GetTicketRequests()))
	for _, ticketReq := range request.GetTicketRequests() {
		reqBody := ticketReq.GetRequest()
		ticketBody := &v2.Ticket{
			DisplayName:  reqBody.GetDisplayName(),
			Description:  reqBody.GetDescription(),
			Status:       reqBody.GetStatus(),
			Labels:       reqBody.GetLabels(),
			CustomFields: reqBody.GetCustomFields(),
			RequestedFor: reqBody.GetRequestedFor(),
		}
		ticket, annos, err := d.CreateTicket(ctx, ticketBody, ticketReq.GetSchema())
		// Merge the request annotations so the external ticket ref round-trips.
		annos.Merge(ticketReq.GetAnnotations()...)
		resp := &v2.TicketsServiceCreateTicketResponse{Ticket: ticket, Annotations: annos}
		if err != nil {
			resp.Error = err.Error()
		}
		tickets = append(tickets, resp)
	}
	return &v2.TicketsServiceBulkCreateTicketsResponse{Tickets: tickets}, nil
}

// BulkGetTickets fetches tickets one by one with the same per-item error
// semantics as BulkCreateTickets (spec R11).
func (d *Connector) BulkGetTickets(ctx context.Context, request *v2.TicketsServiceBulkGetTicketsRequest) (*v2.TicketsServiceBulkGetTicketsResponse, error) {
	tickets := make([]*v2.TicketsServiceGetTicketResponse, 0, len(request.GetTicketRequests()))
	for _, ticketReq := range request.GetTicketRequests() {
		ticket, annos, err := d.GetTicket(ctx, ticketReq.GetId())
		// Merge the request annotations so the external ticket ref round-trips.
		annos.Merge(ticketReq.GetAnnotations()...)
		resp := &v2.TicketsServiceGetTicketResponse{Ticket: ticket, Annotations: annos}
		if err != nil {
			resp.Error = err.Error()
		}
		tickets = append(tickets, resp)
	}
	return &v2.TicketsServiceBulkGetTicketsResponse{Tickets: tickets}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/connector/ -run 'TestBulk' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/connector/ticket.go pkg/connector/ticket_test.go
git commit -m "feat: add bulk ticket methods with per-item error semantics"
```

---

### Task 9: Wiring — config field, codegen, interface assertion, `main.go`

**Files:**
- Modify: `pkg/config/config.go`
- Regenerate: `pkg/config/conf.gen.go` (via `go generate`, never by hand)
- Modify: `cmd/baton-zendesk/main.go`
- Modify: `pkg/connector/ticket.go` (compile-time assertion)

**Interfaces:**
- Consumes: T6-T8 methods (assertion requires all six); SDK `field.TicketingField`, `connectorbuilder.WithTicketingEnabled()`.
- Produces: `cfg.Ticketing bool` on the generated config; ticketing opt wired.

- [ ] **Step 1: Add the compile-time assertion (the failing "test" is the build)**

At the top of `pkg/connector/ticket.go`, after imports, add:

```go
// Compile-time check: ticketing capability requires all six methods.
var _ connectorbuilder.TicketManagerLimited = (*Connector)(nil)
```

Add `"github.com/conductorone/baton-sdk/pkg/connectorbuilder"` to that file's imports.

Run: `go build ./...`
Expected: PASS (all six methods exist after T6-T8). If it fails, a signature drifted — fix before proceeding.

- [ ] **Step 2: Add the config field and regenerate**

In `pkg/config/config.go`, add to the `var (...)` block:

```go
	// TicketingGUIField re-exports the SDK's shared --ticketing flag so the
	// ConductorOne GUI shows the toggle (same pattern as baton-jira and
	// baton-freshservice).
	TicketingGUIField = field.TicketingField.ExportAs(field.ExportTargetGUI)
```

And append it to `ConfigurationFields`:

```go
	ConfigurationFields = []field.SchemaField{
		SubdomainField,
		ApiTokenField,
		EmailField,
		OrgsField,
		BaseURLField,
		TicketingGUIField,
	}
```

Run: `go generate ./pkg/config`
Expected: `pkg/config/conf.gen.go` regenerates and now contains a `Ticketing bool` field (mapstructure tag `ticketing`). Verify: `grep -i ticketing pkg/config/conf.gen.go` shows the field.

- [ ] **Step 3: Wire the opt in `cmd/baton-zendesk/main.go`**

Replace the `getConnector` function body:

```go
func getConnector(ctx context.Context, cfg *config.Zendesk, _ *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	cb, err := connector.New(ctx, cfg.Orgs, cfg.Subdomain, cfg.Email, cfg.ApiToken, cfg.BaseUrl)
	if err != nil {
		return nil, nil, err
	}
	var opts []connectorbuilder.Opt
	if cfg.Ticketing {
		opts = append(opts, connectorbuilder.WithTicketingEnabled())
	}
	return cb, opts, nil
}
```

- [ ] **Step 4: Add the R1 accept test — `ExternalTicketSettings.Enabled` toggles with the opt**

Append to `pkg/connector/ticket_test.go` (add imports: `"github.com/conductorone/baton-sdk/pkg/annotations"`, `"github.com/conductorone/baton-sdk/pkg/connectorbuilder"`):

```go
// TestTicketingEnabledToggle asserts spec R1: implementing the interface
// always advertises ticketing; the WithTicketingEnabled opt only flips
// ExternalTicketSettings.Enabled — which is what C1 reads for routing.
func TestTicketingEnabledToggle(t *testing.T) {
	fixture := ticketTestFixture{formsJSON: `{"ticket_forms":[],"next_page":null,"count":0}`, fieldsJSON: testFieldsJSON}
	for _, tc := range []struct {
		name string
		opts []connectorbuilder.Opt
		want bool
	}{
		{name: "enabled", opts: []connectorbuilder.Opt{connectorbuilder.WithTicketingEnabled()}, want: true},
		{name: "disabled", opts: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			srv, err := connectorbuilder.NewConnector(ctx, newTicketTestConnector(t, fixture), tc.opts...)
			if err != nil {
				t.Fatalf("NewConnector: %v", err)
			}
			resp, err := srv.GetMetadata(ctx, &v2.ConnectorServiceGetMetadataRequest{})
			if err != nil {
				t.Fatalf("GetMetadata: %v", err)
			}
			settings := &v2.ExternalTicketSettings{}
			annos := annotations.Annotations(resp.GetMetadata().GetAnnotations())
			ok, err := annos.Pick(settings)
			if err != nil || !ok {
				t.Fatalf("expected ExternalTicketSettings annotation (ok=%v err=%v)", ok, err)
			}
			if settings.GetEnabled() != tc.want {
				t.Fatalf("expected Enabled=%v, got %v", tc.want, settings.GetEnabled())
			}
		})
	}
}
```

Run: `go test ./pkg/connector/ -run 'TestTicketingEnabledToggle' -v`
Expected: PASS both subtests. (If `connectorbuilder.NewConnector` requires additional setup in this SDK version, check its usage in the SDK's own tests under `vendor/.../connectorbuilder/` and adjust construction — the assertion target stays `ExternalTicketSettings.Enabled`.)

Run: `go build ./... && go test ./... -count=1`
Expected: all packages pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/config/config.go pkg/config/conf.gen.go cmd/baton-zendesk/main.go pkg/connector/ticket.go pkg/connector/ticket_test.go
git commit -m "feat: wire ticketing capability behind --ticketing flag"
```

---

### Task 10: Test-server ticket handlers + end-to-end integration test

**Files:**
- Create: `cmd/test-server/handlers_tickets.go`
- Modify: `cmd/test-server/main.go` (extract `newMux`, register routes)
- Modify: `cmd/test-server/state.go` (ticket storage + seed types)
- Modify: `cmd/test-server/seeds.go` (forms/fields/seed data)
- Create: `cmd/test-server/tickets_integration_test.go`

**Interfaces:**
- Consumes: existing `writeJSON`/`writeJSONStatus`/`cbpMeta`/`cbpPage`/`requireAuth`/`recordingMiddleware`; `connector.New` + T6/T7 methods.
- Produces: mock endpoints `POST /tickets.json`, `GET /tickets/{idWithExt}`, `GET /ticket_fields.json`, `GET /ticket_forms.json`; `newMux(srv *server) *http.ServeMux` reused by `run()` and tests.

- [ ] **Step 1: Extract `newMux` in `cmd/test-server/main.go`**

Move the route-registration block out of `run()` into:

```go
func newMux(srv *server) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /organizations.json", requireAuth(srv.handleListOrganizations))
	mux.HandleFunc("GET /users.json", requireAuth(srv.handleListUsers))
	// Go's ServeMux requires a wildcard to occupy the whole path segment, so
	// the ".json" suffix can't sit in the same {id} wildcard — capture it and
	// strip the suffix in the handler instead.
	mux.HandleFunc("GET /users/{idWithExt}", requireAuth(srv.handleGetUser))
	mux.HandleFunc("GET /organization_memberships.json", requireAuth(srv.handleOrgMemberships))
	mux.HandleFunc("POST /organization_memberships.json", requireAuth(srv.handleOrgMemberships))
	mux.HandleFunc("DELETE /organization_memberships/{id}", requireAuth(srv.handleDeleteOrgMembership))
	mux.HandleFunc("GET /groups.json", requireAuth(srv.handleListGroups))
	mux.HandleFunc("GET /group_memberships.json", requireAuth(srv.handleListGroupMemberships))
	mux.HandleFunc("GET /custom_roles.json", requireAuth(srv.handleListCustomRoles))

	mux.HandleFunc("POST /tickets.json", requireAuth(srv.handleCreateTicket))
	mux.HandleFunc("GET /tickets/{idWithExt}", requireAuth(srv.handleGetTicket))
	mux.HandleFunc("GET /ticket_fields.json", requireAuth(srv.handleListTicketFields))
	mux.HandleFunc("GET /ticket_forms.json", requireAuth(srv.handleListTicketForms))

	// Debug-only, not part of the Zendesk API: exposes the per-path call
	// counters so a validation script can assert the old per-org
	// GetOrganizationUsers endpoint (/organizations/{id}/users.json) is never
	// hit after the org.Grants -> team_member.Grants inversion.
	mux.HandleFunc("GET /__debug/calls", srv.handleDebugCalls)

	return mux
}
```

and in `run()` replace the old block with `mux := newMux(srv)`.

- [ ] **Step 2: Add ticket state to `cmd/test-server/state.go`**

Add types + fields (mirroring the existing style):

```go
// TicketField mirrors the fields baton-zendesk reads off zendesk.TicketField.
type TicketField struct {
	ID        int64
	Type      string
	Title     string
	Active    bool
	Removable bool
	Required  bool
	Options   []map[string]any // custom_field_options entries: {"name","value"}.
}

// TicketForm mirrors zendesk.TicketForm.
type TicketForm struct {
	ID             int64
	Name           string
	Active         bool
	TicketFieldIDs []int64
}

// Ticket is a created mock ticket, echoed back by GET /tickets/{id}.
type Ticket struct {
	ID           int64
	Subject      string
	Description  string
	Status       string
	Priority     string
	Type         string
	RequesterID  int64
	TicketFormID int64
	Tags         []string
	CustomFields []map[string]any
}
```

Add to `State`: `ticketFields []*TicketField`, `ticketForms []*TicketForm`, `tickets map[int64]*Ticket`, `nextTicketID int64`. Add accessor methods following the existing one-lock-per-method convention:

```go
// Copy-on-read like the sibling accessors (ListOrganizations et al.) so
// callers never alias shared state.
func (s *State) ListTicketFields() []*TicketField {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TicketField, len(s.ticketFields))
	copy(out, s.ticketFields)
	return out
}

func (s *State) ListTicketForms() []*TicketForm {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TicketForm, len(s.ticketForms))
	copy(out, s.ticketForms)
	return out
}

func (s *State) CreateTicket(t *Ticket) *Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextTicketID
	s.nextTicketID++
	if t.Status == "" {
		t.Status = "new"
	}
	s.tickets[t.ID] = t
	return t
}

func (s *State) GetTicket(id int64) (*Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	return t, ok
}
```

(Adjust formatting to match the file; initialize `tickets` map and `nextTicketID: 9001` where `NewState()` builds the struct. Seed fields/forms in `seeds.go` — **all seeded fields `Required: false`** (the e2e test creates a ticket supplying only field 502, which passes `ValidateTicket` only if nothing else is required):
- form `{ID: 401, Name: "Access Request", Active: true, TicketFieldIDs: [501, 502, 503, 504, 505, 506, 507]}` — 504/505 are deliberately ON the form so the schema build genuinely traverses the integer-skip and system-field-exclusion paths;
- one inactive form `{ID: 402, Name: "Retired", Active: false, TicketFieldIDs: [501]}`;
- fields (one of each mapped type per spec R13, plus the skip/exclusion cases): 501 `text` (Active, Removable), 502 `tagger` (Active, Removable, Options: `[{"name":"Option A","value":"opt_a"},{"name":"Option B","value":"opt_b"}]`), 503 `date` (Active, Removable), 504 `integer` (Active, Removable — skipped by the schema mapper), 505 `subject` (Active, `Removable: false` — excluded as a system field), 506 `checkbox` (Active, Removable), 507 `multiselect` (Active, Removable, Options: `[{"name":"Tag One","value":"tag_one"}]`).)

- [ ] **Step 3: Write `cmd/test-server/handlers_tickets.go`**

```go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func ticketFieldJSON(f *TicketField) map[string]any {
	return map[string]any{
		"id": f.ID, "type": f.Type, "title": f.Title,
		"active": f.Active, "removable": f.Removable, "required": f.Required,
		"custom_field_options": f.Options,
	}
}

func ticketJSON(t *Ticket) map[string]any {
	return map[string]any{
		"id": t.ID, "subject": t.Subject, "description": t.Description,
		"status": t.Status, "priority": t.Priority, "type": t.Type,
		"requester_id": t.RequesterID, "ticket_form_id": t.TicketFormID,
		"tags": t.Tags, "custom_fields": t.CustomFields,
		"url":        "http://test/api/v2/tickets/" + strconv.FormatInt(t.ID, 10) + ".json",
		"created_at": "2026-07-23T10:00:00Z", "updated_at": "2026-07-23T10:00:00Z",
	}
}

// handleListTicketFields serves cursor-paginated ticket fields, the CBP shape
// ListAllTicketFields drains.
func (s *server) handleListTicketFields(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.URL.Query().Get("page[size]"))
	page, hasMore, next := cbpPage(s.state.ListTicketFields(), size, r.URL.Query().Get("page[after]"))
	out := make([]map[string]any, 0, len(page))
	for _, f := range page {
		out = append(out, ticketFieldJSON(f))
	}
	writeJSON(w, map[string]any{"ticket_fields": out, keyMeta: cbpMeta(hasMore, next)})
}

// handleListTicketForms serves offset-paginated ticket forms (legacy OBP shape
// with next_page URL), the shape ListAllTicketForms drains.
func (s *server) handleListTicketForms(w http.ResponseWriter, r *http.Request) {
	forms := s.state.ListTicketForms()
	out := make([]map[string]any, 0, len(forms))
	for _, f := range forms {
		out = append(out, map[string]any{
			"id": f.ID, keyName: f.Name, "active": f.Active, "ticket_field_ids": f.TicketFieldIDs,
		})
	}
	// Single page: the real cap is 300 forms; the mock never needs page 2,
	// multi-page drain behavior is covered by pkg/client unit tests.
	writeJSON(w, map[string]any{"ticket_forms": out, "next_page": nil, "count": len(out)})
}

func (s *server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ticket struct {
			Subject string `json:"subject"`
			Comment struct {
				Body string `json:"body"`
			} `json:"comment"`
			Status       string           `json:"status"`
			Priority     string           `json:"priority"`
			Type         string           `json:"type"`
			RequesterID  int64            `json:"requester_id"`
			TicketFormID int64            `json:"ticket_form_id"`
			Tags         []string         `json:"tags"`
			CustomFields []map[string]any `json:"custom_fields"`
		} `json:"ticket"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "MalformedRequest", keyDescription: err.Error()})
		return
	}
	if req.Ticket.Comment.Body == "" {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]any{keyError: "RecordInvalid", keyDescription: "comment is required"})
		return
	}
	t := s.state.CreateTicket(&Ticket{
		Subject: req.Ticket.Subject, Description: req.Ticket.Comment.Body,
		Status: req.Ticket.Status, Priority: req.Ticket.Priority, Type: req.Ticket.Type,
		RequesterID: req.Ticket.RequesterID, TicketFormID: req.Ticket.TicketFormID,
		Tags: req.Ticket.Tags, CustomFields: req.Ticket.CustomFields,
	})
	writeJSONStatus(w, http.StatusCreated, map[string]any{"ticket": ticketJSON(t)})
}

func (s *server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSuffix(r.PathValue("idWithExt"), ".json")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{keyError: "InvalidValue"})
		return
	}
	t, ok := s.state.GetTicket(id)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{keyError: "RecordNotFound"})
		return
	}
	writeJSON(w, map[string]any{"ticket": ticketJSON(t)})
}
```

- [ ] **Step 4: Write the integration test (CI-run end-to-end, spec R13)**

Create `cmd/test-server/tickets_integration_test.go`:

```go
package main

import (
	"context"
	"net/http/httptest"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	sdkTicket "github.com/conductorone/baton-sdk/pkg/types/ticket"
	"github.com/conductorone/baton-zendesk/pkg/connector"
)

// TestTicketingEndToEnd drives ListTicketSchemas -> CreateTicket -> GetTicket
// against the mock server through the real connector + client stack (spec R13).
func TestTicketingEndToEnd(t *testing.T) {
	srv := &server{state: NewState()}
	ts := httptest.NewServer(recordingMiddleware(srv.state, newMux(srv)))
	t.Cleanup(ts.Close)

	ctx := context.Background()
	c, err := connector.New(ctx, nil, "", testEmail, testAPIToken, ts.URL)
	if err != nil {
		t.Fatalf("connector.New: %v", err)
	}

	schemas, next, _, err := c.ListTicketSchemas(ctx, &pagination.Token{})
	if err != nil {
		t.Fatalf("ListTicketSchemas: %v", err)
	}
	if next != "" || len(schemas) != 1 {
		t.Fatalf("expected 1 schema (one active seeded form), next empty; got %d, %q", len(schemas), next)
	}
	schema := schemas[0]
	if schema.GetId() != "401" {
		t.Fatalf("expected schema for form 401, got %s", schema.GetId())
	}
	// Form 401 lists fields 501-507: text 501 + tagger 502 + date 503 +
	// checkbox 506 + multiselect 507 map, integer 504 is skipped, system
	// subject field 505 is excluded, and the synthetic priority/type fields
	// are added — exactly 7.
	if got := len(schema.GetCustomFields()); got != 7 {
		t.Fatalf("expected 7 schema custom fields, got %d", got)
	}
	if _, ok := schema.GetCustomFields()["504"]; ok {
		t.Fatalf("integer field 504 must be skipped")
	}
	if _, ok := schema.GetCustomFields()["505"]; ok {
		t.Fatalf("system field 505 must be excluded")
	}

	created, _, err := c.CreateTicket(ctx, &v2.Ticket{
		DisplayName: "Access to prod",
		Description: "please",
		CustomFields: map[string]*v2.TicketCustomField{
			"502": sdkTicket.PickStringField("502", "opt_a"),
		},
	}, schema)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.GetId() == "" {
		t.Fatalf("expected created ticket id, got %+v", created)
	}

	got, _, err := c.GetTicket(ctx, created.GetId())
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.GetStatus().GetId() != "new" {
		t.Fatalf("expected status new, got %+v", got.GetStatus())
	}
	if got.GetCompletedAt() != nil {
		t.Fatalf("new ticket must not be completed")
	}
}
```

Note: the seeded tagger field 502 must include an option with value `opt_a` (Step 2 seeds).

- [ ] **Step 5: Run, then commit**

Run: `go test ./cmd/test-server/ ./pkg/... -count=1 && go build ./...`
Expected: all pass, including `TestTicketingEndToEnd`.

```bash
git add cmd/test-server/
git commit -m "feat: add ticket endpoints to test server with e2e integration test"
```

---

### Task 11: Documentation

**Files:**
- Modify: `README.md`
- Modify: `cmd/test-server/README.md` (manual ticketing flow)

**Interfaces:** none (docs only).

- [ ] **Step 1: Add a Ticketing section to `README.md`**

Insert after the existing usage/flags documentation (match surrounding tone/format):

```markdown
## Ticketing

baton-zendesk can create and track Zendesk tickets for ConductorOne access-request
fulfillment (external ticket provisioning). Enable it with `--ticketing`.

- **Schemas:** one ticket schema per active Zendesk ticket form, containing that form's
  active custom fields plus `priority` and `type` pick fields. On accounts without ticket
  forms (forms require Suite Growth+ / Support Enterprise or the Productivity Pack
  add-on), a single "Default" schema with all active custom fields is served instead.
- **Completion:** a ticket is considered done when its status is `solved` or `closed`
  (`completed_at` approximates via the ticket's `updated_at`).
- **Token scopes:** the API token must be able to read ticket fields and ticket forms and
  create/read tickets.
- **v1 limitations:** integer/decimal custom fields are not exposed as schema fields;
  Zendesk custom ticket statuses are not supported (system statuses only); ticket creation
  does not yet send an `Idempotency-Key` header.
```

Also extend the "app scopes"/requirements list mentioning tickets read/write if the README enumerates scopes.

- [ ] **Step 2: Document the manual flow in `cmd/test-server/README.md`**

Append:

```markdown
## Ticketing flow

With the server running, exercise ticketing end to end:

    go run ./cmd/baton-zendesk --base-url http://127.0.0.1:8765 \
      --email agent@example.com --api-token test-token --subdomain unused \
      --ticketing --list-ticket-schemas

    go run ./cmd/baton-zendesk --base-url http://127.0.0.1:8765 \
      --email agent@example.com --api-token test-token --subdomain unused \
      --ticketing --create-ticket --bulk-ticket-template-path ./ticket.json

(The automated equivalent runs in CI as TestTicketingEndToEnd.)
```

- [ ] **Step 3: Release-gate note**

Add to the README Ticketing section (or CONTRIBUTING notes if more appropriate):

```markdown
> **Release note:** before releasing ticketing, verify the ticket-forms endpoint's
> behavior on a live non-forms-plan trial account; if the plan-gate signal is not
> HTTP 404, update `isTicketFormsPlanGate` accordingly (see spec R4).
```

- [ ] **Step 4: Verify + commit**

Run: `go build ./... && go test ./... -count=1`
Expected: green.

```bash
git add README.md cmd/test-server/README.md
git commit -m "docs: document ticketing support and test-server flow"
```

---

## Risks & open questions

- **`connectorbuilder.NewConnector` in a unit test (T9 Step 4):** the builder may pull in
  otel/metrics defaults; if construction needs extra options in this SDK version, mirror
  its usage from the SDK's own tests — the assertion target (`ExternalTicketSettings.Enabled`
  toggling with the opt) is the invariant, not the construction recipe.

- **`go generate ./pkg/config` output drift:** the generated struct's exact field set depends on the SDK generator; if `Ticketing` doesn't appear after regen, check that `ExportAs(ExportTargetGUI)` returns a field the generator includes (baton-jira's generated config has it) — T9 Step 2's grep gates this.
- **`v2.TicketSchema`/`v2.Ticket` struct literals:** vendored SDK is hybrid-API protobuf (exported fields + builders); plain literals compile. If a future SDK bump moves to opaque-only, switch to `_builder{}.Build()`.
- **Forms endpoint behavior on non-forms plans** is unverified upstream — release gate in T11/spec R4.
- **`zendesk.CustomFieldOption.Value`** is assumed the tag string (per API docs); T10's integration test exercises it against the mock only — live verification happens alongside the release gate.
