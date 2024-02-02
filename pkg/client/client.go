package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nukosuke/go-zendesk/zendesk"
)

type ZendeskClient struct {
	client *zendesk.Client
}

func New(ctx context.Context, httpClient *http.Client, subdomain string, email string, apiToken string) (*ZendeskClient, error) {
	zc := &ZendeskClient{}
	client, err := zendesk.NewClient(httpClient)
	if err != nil {
		return nil, err
	}
	client.SetSubdomain(subdomain)
	client.SetCredential(zendesk.NewAPITokenCredential(email, apiToken))
	zc.client = client
	return zc, nil
}

func (c *ZendeskClient) ListUsers(ctx context.Context) error {
	users, page, err := c.client.GetUsers(ctx, nil)
	if err != nil {
		return err
	}
	for _, user := range users {
		fmt.Println(user.Name)
		fmt.Println(user.Email)
	}
	fmt.Println(page.Count)
	return nil
}
