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
	sdkTicket "github.com/conductorone/baton-sdk/pkg/types/ticket"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	var formID int64
	if schemaID != defaultSchemaID {
		var err error
		formID, err = strconv.ParseInt(schemaID, 10, 64)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "baton-zendesk: invalid ticket schema id %q", schemaID)
		}
	}

	fields, err := d.zendeskClient.ListAllTicketFields(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list ticket fields: %w", err)
	}

	if schemaID == defaultSchemaID {
		return defaultTicketSchema(ctx, fields), nil, nil
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
		// date is Zendesk's only temporal custom-field type; format in UTC so the date is deterministic.
		return v.UTC().Format("2006-01-02"), err == nil && !v.IsZero(), nil
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
