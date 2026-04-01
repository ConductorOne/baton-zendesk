package client

import "github.com/nukosuke/go-zendesk/zendesk"

func getNextPageToken(meta zendesk.CursorPaginationMeta) string {
	if meta.HasMore {
		return meta.AfterCursor
	}
	return ""
}
