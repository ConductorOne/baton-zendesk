package client

import (
	"errors"
	"net/http"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/nukosuke/go-zendesk/zendesk"
	"google.golang.org/grpc/codes"
)

// ErrMembershipNotFound is returned when a membership lookup finds no matching record.
var ErrMembershipNotFound = errors.New("membership not found")

func getNextPageToken(meta zendesk.CursorPaginationMeta) string {
	if meta.HasMore {
		return meta.AfterCursor
	}
	return ""
}

// wrapZendeskError maps a zendesk HTTP error to a gRPC-coded error.
// Non-zendesk errors are returned as-is.
func wrapZendeskError(err error) error {
	if err == nil {
		return nil
	}
	var zErr zendesk.Error
	if !errors.As(err, &zErr) {
		return err
	}
	switch zErr.Status() {
	case http.StatusUnauthorized:
		return uhttp.WrapErrors(codes.Unauthenticated, "baton-zendesk: unauthorized", err)
	case http.StatusForbidden:
		return uhttp.WrapErrors(codes.PermissionDenied, "baton-zendesk: permission denied", err)
	case http.StatusNotFound:
		return uhttp.WrapErrors(codes.NotFound, "baton-zendesk: not found", err)
	case http.StatusUnprocessableEntity:
		return uhttp.WrapErrors(codes.InvalidArgument, "baton-zendesk: invalid argument", err)
	case http.StatusTooManyRequests:
		return uhttp.WrapErrors(codes.Unavailable, "baton-zendesk: rate limit exceeded", err)
	case http.StatusInternalServerError:
		return uhttp.WrapErrors(codes.Internal, "baton-zendesk: internal server error", err)
	case http.StatusServiceUnavailable:
		return uhttp.WrapErrors(codes.Unavailable, "baton-zendesk: service unavailable", err)
	default:
		return uhttp.WrapErrors(codes.Unknown, "baton-zendesk: unexpected API error", err)
	}
}
