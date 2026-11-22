package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/nukosuke/go-zendesk/zendesk"
)

// ErrMembershipNotFound is returned when a membership lookup finds no matching record.
var ErrMembershipNotFound = errors.New("membership not found")

func getNextPageToken(meta zendesk.CursorPaginationMeta) string {
	if meta.HasMore {
		return meta.AfterCursor
	}
	return ""
}

// wrapZendeskError maps a zendesk HTTP error to the gRPC code uhttp.BaseHttpClient returns for the same
// status, including rate-limit details. Non-zendesk errors are returned as-is.
func wrapZendeskError(err error) error {
	if err == nil {
		return nil
	}
	var zErr zendesk.Error
	if !errors.As(err, &zErr) {
		return err
	}
	resp := &http.Response{
		StatusCode: zErr.Status(),
		Status:     fmt.Sprintf("baton-zendesk: %d %s", zErr.Status(), http.StatusText(zErr.Status())),
		Header:     zErr.Headers(),
	}
	return uhttp.WrapErrorsWithRateLimitInfo(uhttp.GrpcCodeFromHTTPStatus(zErr.Status()), resp, err)
}

// IsDuplicateEmailError reports whether err is Zendesk's response to creating a
// user whose email another user already has: a 422 RecordInvalid whose details
// carry a DuplicateValue error on the email field.
func IsDuplicateEmailError(err error) bool {
	var zErr zendesk.Error
	if !errors.As(err, &zErr) || zErr.Status() != http.StatusUnprocessableEntity {
		return false
	}
	body, readErr := io.ReadAll(zErr.Body())
	if readErr != nil {
		return false
	}
	var payload struct {
		Error   string `json:"error"`
		Details struct {
			Email []struct {
				Error string `json:"error"`
			} `json:"email"`
		} `json:"details"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error != "RecordInvalid" {
		return false
	}
	for _, detail := range payload.Details.Email {
		if detail.Error == "DuplicateValue" {
			return true
		}
	}
	return false
}
