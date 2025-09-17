package main

import (
	"github.com/conductorone/baton-sdk/pkg/config"
	cfg "github.com/conductorone/baton-zendesk/pkg/config"
)

func main() {
	config.Generate("zendesk", cfg.Config)
}
