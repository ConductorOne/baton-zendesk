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
