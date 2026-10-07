package client

import (
	"errors"
	"fmt"
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
