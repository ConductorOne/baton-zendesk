# baton-zendesk
`baton-zendesk` is a connector for Zendesk built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the Zendesk API to sync data about users, groups and enterprise.

Check out [Baton](https://github.com/conductorone/baton) to learn more the project in general.

# Getting Started
You can try out the Zendesk platform with a free, 14-day trial account. If you're interested in becoming a Zendesk developer partner, you can convert your trial account into a sponsored Zendesk Support account.

As part of becoming a Zendesk developer partner, Zendesk sponsors an instance for up to 5 agents that you can use for developing, demoing, and troubleshooting your app or integration.

Unlike a trial account, a sponsored account does not expire after 14 days.
## Prerequisites

1. Zendesk `trial account` sign up for a free Zendesk Support trial  [developer site](https://www.zendesk.com/register/)
2. Authentication method set to `OAuth 2.0 with Client Credentials Grant (Server Authentication)`
3. App access level set to: `App + Enterprise Access`
4. Application Scopes: 
  - manage users
  - manage groups
  - manage organizations
  - grant read resource
5. App must be approved by your Zendesk admin. More info [here](https://developer.box.com/guides/authorization/custom-app-approval/)
6. Enterprise ID can be found in `Developer console -> Your App -> General settings`
7. Client ID and Client Secret can be found in `Developer console -> Your App -> Configuration`

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-box
baton-zendesk
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_ZENDESK_CLIENT_ID=clientId BATON_ZENDESK_CLIENT_SECRET=clientSecret BATON_ENTERPRISE_ID=enterpriseId ghcr.io/conductorone/baton-zendesk:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-zendesk/cmd/baton-zendesk@main

BATON_CLIENT_ID=clientId BATON_CLIENT_SECRET=clientSecret BATON_ENTERPRISE_ID=enterpriseId 
baton resources
```

# Data Model

`baton-zendesk` pulls down information about the following Zendesk resources:
- Users
- Groups
- Organizations

# Contributing, Support, and Issues

We started Baton because we were tired of taking screenshots and manually building spreadsheets. We welcome contributions, and ideas, no matter how small -- our goal is to make identity and permissions sprawl less painful for everyone. If you have questions, concerns, or ideas: Please open a Github Issue!

See [CONTRIBUTING.md](https://github.com/ConductorOne/baton/blob/main/CONTRIBUTING.md) for more details.

# `baton-zendesk` Command Line Usage

```
baton-zendesk

Usage:
  baton-zendesk [flags]
  baton-zendesk [command]

Available Commands:
  capabilities       Get connector capabilities
  completion         Generate the autocompletion script for the specified shell
  help               Help about any command

Flags:
      --api-token string       The Zendesk apitoken. ($BATON_API_TOKEN)
      --client-id string       The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string   The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
      --email string           The Zendesk email. ($BATON_EMAIL)
  -f, --file string            The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                   help for baton-zendesk
      --log-format string      The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string       The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -p, --provisioning           This must be set in order for provisioning actions to be enabled. ($BATON_PROVISIONING)
      --subdomain string       The Zendesk subdomain. ($BATON_SUBDOMAIN)
  -v, --version                version for baton-zendesk

Use "baton-zendesk [command] --help" for more information about a command.
```
