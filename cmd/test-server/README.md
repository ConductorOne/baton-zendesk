# Test server

Mock Zendesk API for baton-zendesk, used to validate the org/team_member
grant-inversion fix (CXH-1955/CXH-29) locally, without a real Support-enabled
tenant. Not wired into CI — this was built to validate one specific fix on
`felipelucero/cxh-29-invert-org-grants-fixes`, see `/create-ci-tests` if it
should become a permanent CI fixture.

## Auth

| Real API | Test server |
|---|---|
| HTTP Basic, username `{email}/token`, password = API token | Same flow: `agent@example.com/token` / `test-token` |

## Endpoints

| Path | Method | Doc URL |
|---|---|---|
| `/organizations.json` | GET (CBP) | https://developer.zendesk.com/api-reference/ticketing/organizations/organizations/#list-organizations |
| `/users.json` | GET (CBP, `role=admin\|agent`) | https://developer.zendesk.com/api-reference/ticketing/users/users/#list-users |
| `/users/{id}.json` | GET | https://developer.zendesk.com/api-reference/ticketing/users/users/#show-user |
| `/organization_memberships.json` | GET (CBP or OBP, dispatches on `page[size]`/`page[after]` vs `page`), POST | https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/ |
| `/organization_memberships/{id}` | DELETE (no `.json` — matches `pkg/client/client.go`) | https://developer.zendesk.com/api-reference/ticketing/organizations/organization_memberships/#delete-membership |
| `/groups.json` | GET (CBP) | https://developer.zendesk.com/api-reference/ticketing/groups/groups/#list-groups |
| `/group_memberships.json` | GET (CBP, `group_id=`) | https://developer.zendesk.com/api-reference/ticketing/groups/group_memberships/#list-memberships |
| `/custom_roles.json` | GET — always returns `[]` | https://developer.zendesk.com/api-reference/ticketing/users/custom_roles/#list-custom-roles |
| `/__debug/calls` | GET — **not a Zendesk endpoint** | per-path request counters, see "Validating the fix" below |

Group/role provisioning (`POST`/`DELETE` on groups or custom roles) is out of
scope — this server only mocks what's needed to exercise the org/team_member
sync and org membership provisioning paths.

## Seed data

8 orgs, 4 team members (2 admin, 2 agent), 8 org memberships, 2 groups. See
`seeds.go` for the canonical list. Expected org grants after a full sync:

| Org | Grant | From membership |
|---|---|---|
| 1 (Acme Corp) | `org:1:admin` | Alice (101) |
| 1 (Acme Corp) | `org:1:agent` | Bob (102) |
| 2 (Globex) | `org:2:admin` | Alice (101) |
| 3 (Initech) | `org:3:admin` | Alice (101) |
| 4 (Umbrella Corp) | `org:4:agent` | Carol (103) |
| 5 (Wayne Enterprises) | *(none — zero team members, tests the empty side)* | — |
| 6 (Stark Industries) | `org:6:agent` | Carol (103) |
| 7 (Wonka Inc) | `org:7:agent` | Carol (103) |
| 8 (Hooli) | `org:8:agent` | Carol (103) |

Dave (104, admin) has no org memberships — exercises team_member.Grants'
empty-grants path.

## Running locally

```bash
# From the repo root, in one terminal:
go run ./cmd/test-server/

# In another terminal, point the connector at it:
export BATON_SUBDOMAIN=unused        # ignored when BATON_BASE_URL is set
export BATON_EMAIL=agent@example.com
export BATON_API_TOKEN=test-token
export BATON_BASE_URL=http://127.0.0.1:8765
go run ./cmd/baton-zendesk --file /tmp/sync-test.c1z --log-level info
```

## Validating the fix

```bash
# Confirm the expected 8 org grants (1 per seeded membership) exist, no
# duplicates or misses:
baton grants --file /tmp/sync-test.c1z --output-format json | \
  jq '[.grants[] | select(.grant.entitlement.resource.id.resourceType=="org")] | length'
# -> 8

# Confirm team_member count matches the seeded roster (no orgs×team_members
# fanout — this is the bug CXH-29/CXH-1955 exists to prevent):
baton resources --file /tmp/sync-test.c1z --resource-type=team_member --output-format json | \
  jq '.resources | length'
# -> 4

# Confirm the old per-org endpoint (GetOrganizationUsers) was never called —
# org.Grants is a no-op now, but this proves it, straight from the request log:
curl -s http://127.0.0.1:8765/__debug/calls | jq '.calls | to_entries | map(select(.key | test("organizations/.*/users.json")))'
# -> []
```

## Curl examples

```bash
curl -u 'agent@example.com/token:test-token' \
  'http://127.0.0.1:8765/organizations.json?page[size]=5'

curl -u 'agent@example.com/token:test-token' \
  'http://127.0.0.1:8765/organization_memberships.json?user_id=101&page[size]=100'

## Ticketing flow

With the server running, exercise ticketing end to end:

    go run ./cmd/baton-zendesk --base-url http://127.0.0.1:8765 \
      --email agent@example.com --api-token test-token --subdomain unused \
      --ticketing --list-ticket-schemas

    go run ./cmd/baton-zendesk --base-url http://127.0.0.1:8765 \
      --email agent@example.com --api-token test-token --subdomain unused \
      --ticketing --create-ticket --bulk-ticket-template-path ./ticket.json

(The automated equivalent runs in CI as TestTicketingEndToEnd.)
```
