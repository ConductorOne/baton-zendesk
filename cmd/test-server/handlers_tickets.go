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
