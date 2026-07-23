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
