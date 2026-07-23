package client

import (
	"encoding/json"
	"testing"
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
