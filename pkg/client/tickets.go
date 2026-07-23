package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nukosuke/go-zendesk/zendesk"
)

// Ticket create/read paths use these local types instead of go-zendesk's: its
// CustomField.UnmarshalJSON rejects JSON numbers, so any ticket carrying a
// numeric custom-field value — including agent-set values on fields this
// connector never touches — fails to decode through the vendored typed methods.
// https://developer.zendesk.com/api-reference/ticketing/tickets/tickets/#create-ticket
const (
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
		return fmt.Errorf("baton-zendesk: decode custom field: %w", err)
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
