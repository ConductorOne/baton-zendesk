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
