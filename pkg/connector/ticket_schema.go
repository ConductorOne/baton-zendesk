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
