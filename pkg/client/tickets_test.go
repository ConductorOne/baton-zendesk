package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
