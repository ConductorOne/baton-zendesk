package connector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	sdkTicket "github.com/conductorone/baton-sdk/pkg/types/ticket"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/nukosuke/go-zendesk/zendesk"
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
		{name: "pick string empty omitted", field: sdkTicket.PickStringField("1", ""), wantOK: false},
		{name: "pick multiple", field: sdkTicket.PickMultipleStringsField("1", []string{"a", "b"}), wantOK: true},
		{name: "pick multiple nil omitted", field: sdkTicket.PickMultipleStringsField("1", nil), wantOK: false},
		{name: "strings", field: sdkTicket.StringsField("1", []string{"x"}), wantOK: true},
		{name: "strings nil omitted", field: sdkTicket.StringsField("1", nil), wantOK: false},
		{name: "bool", field: sdkTicket.BoolField("1", false), want: false, wantOK: true},
		{name: "number", field: sdkTicket.NumberField("1", 42), want: float64(42), wantOK: true},
		{name: "timestamp utc date", field: sdkTicket.TimestampField("1", due), want: "2026-08-01", wantOK: true},
		{name: "timestamp crosses day boundary in utc", field: sdkTicket.TimestampField("1", time.Date(2026, 8, 1, 0, 30, 0, 0, time.FixedZone("UTC+9", 9*3600))), want: "2026-07-31", wantOK: true},
		{name: "timestamp zero omitted", field: sdkTicket.TimestampField("1", time.Time{}), wantOK: false},
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
