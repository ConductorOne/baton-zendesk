# `baton-zendesk` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-zendesk.svg)](https://pkg.go.dev/github.com/conductorone/baton-zendesk) ![verify](https://github.com/conductorone/baton-zendesk/actions/workflows/verify.yaml/badge.svg)
`baton-zendesk` is a connector for Zendesk built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the Zendesk API to sync data about users, groups and enterprise.

Check out [Baton](https://github.com/conductorone/baton) to learn more about the project in general.

# Getting Started
You can try out the Zendesk platform with a free, 14-day trial account. If you're interested in becoming a Zendesk developer partner, you can convert your trial account into a sponsored Zendesk Support account.

As part of becoming a Zendesk developer partner, Zendesk sponsors an instance for up to 5 agents that you can use for developing, and troubleshooting your app or integration.

Unlike a trial account, a sponsored account does not expire after 14 days.
## Prerequisites

1. Zendesk `trial account` sign up for a free Zendesk Support trial  [developer site](https://www.zendesk.com/register/)
2. Authentication method set to `Token access`
3. Application Scopes:
  - manage team members
  - manage groups
  - manage organizations
  - grant resources
  - revoke resources
  - read/write tickets (required for ticketing feature)
4. **Permissions for Provisioning Actions**: To use account provisioning features (create, delete, enable, disable users), the API token must belong to an account with one of the following permissions:
  - **Admin** role, OR
  - **Agent** role with permission to edit end-user profiles

## Requesting a sponsored test account
For a trial Support account, see
https://developer.zendesk.com/documentation/api-basics/getting-started/getting-a-trial-or-sponsored-account-for-development/#requesting-a-sponsored-test-account

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-zendesk
baton-zendesk
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_SUBDOMAIN=clientSubdomain BATON_EMAIL=clientEmail BATON_API_TOKEN=apiToken public.ecr.aws/conductorone/baton-zendesk:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-zendesk/cmd/baton-zendesk@main

BATON_SUBDOMAIN=clientSubdomain BATON_EMAIL=clientEmail BATON_API_TOKEN=apiToken baton-zendesk
baton resources
```

# Data Model

`baton-zendesk` pulls down information about the following Zendesk resources:
- Team Members
- Groups
- Organizations
- Roles

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
      --orgs strings           Limit syncing to specific organizations. ($BATON_ORGS)
  -p, --provisioning           This must be set in order for provisioning actions to be enabled. ($BATON_PROVISIONING)
      --subdomain string       The Zendesk subdomain. ($BATON_SUBDOMAIN)
  -v, --version                version for baton-zendesk

Use "baton-zendesk [command] --help" for more information about a command.
```

## Ticketing

baton-zendesk can create and track Zendesk tickets for ConductorOne access-request
fulfillment (external ticket provisioning). Enable it with `--ticketing`.

- **Schemas:** one ticket schema per active Zendesk ticket form, containing that form's
  active custom fields plus `priority` and `type` pick fields. On accounts without ticket
  forms (forms require Suite Growth+ / Support Enterprise or the Productivity Pack
  add-on), a single "Default" schema with all active custom fields is served instead.
- **Completion:** a ticket is considered done when its status is `solved` or `closed`
  (`completed_at` approximates via the ticket's `updated_at`).
- **Token scopes:** the API token must be able to read ticket fields and ticket forms and
  create/read tickets.
- **v1 limitations:** integer/decimal custom fields are not exposed as schema fields;
  Zendesk custom ticket statuses are not supported (system statuses only); ticket creation
  does not yet send an `Idempotency-Key` header.

> **Release note:** before releasing ticketing, verify the ticket-forms endpoint's
> behavior on a live non-forms-plan trial account; if the plan-gate signal is not
> HTTP 404, update `isTicketFormsPlanGate` accordingly (see spec R4).
