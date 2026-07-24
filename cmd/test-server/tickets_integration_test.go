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
