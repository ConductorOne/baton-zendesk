# Zendesk Ticket Provisioning (TicketManager) — Spec

## Problem & context

ConductorOne can fulfill access requests by creating and tracking tickets in an external
ticketing system when a connector implements the baton-sdk `TicketManagerLimited` interface
(as baton-jira, baton-servicenow, and baton-freshservice do). baton-zendesk has never had
this capability — C1 customers running Zendesk cannot route access-request fulfillment
through Zendesk tickets. This spec adds ticket provisioning to the official upstream
connector.

Verified context (2026-07-23):

- baton-sdk v0.20.1 (vendored) registers a ticket manager by type assertion on the
  connector struct (`connectorbuilder/tickets.go:254`). The SDK's gRPC builder
  reconstructs the `*v2.Ticket` passed to the connector's `CreateTicket` from the incoming
  `TicketRequest` with **only** DisplayName, Description, Status, Labels, CustomFields,
  and RequestedFor (`connectorbuilder/tickets.go:166-173`) — `Type` is dropped and never
  reaches the connector.
- `connectorbuilder.WithTicketingEnabled()` only flips
  `ConnectorMetadata.ExternalTicketSettings.Enabled`; it does **not** gate capability
  advertisement or RPC availability (see R1).
- The vendored go-zendesk library has bindings for ticket forms and ticket fields that are
  safe to use, but its `CustomField.UnmarshalJSON`
  (`vendor/github.com/nukosuke/go-zendesk/zendesk/ticket.go:17-45`) **rejects JSON
  numbers** (handles only string/nil/bool/[]interface{}), so its typed
  `CreateTicket`/`GetTicket` fail to decode any ticket carrying a numeric custom-field
  value — including values set by agents on fields the connector never touches. The
  ticket read/create path therefore uses raw calls + local types (R12).
- Zendesk ticket forms are plan-gated (Suite Growth+ / Support Enterprise / Productivity
  Pack add-on). The failure mode of `GET /api/v2/ticket_forms` on unsupported plans is not
  documented; the design must degrade gracefully without masking auth errors (R4).

## Goals

- C1 can list Zendesk-backed ticket schemas, create tickets against them, and poll tickets
  to completion, on any Zendesk plan.
- Schemas mirror how a Zendesk admin already models request types: one schema per active
  ticket form, with only that form's fields.
- Upstream-mergeable quality: repo conventions, tests, test-server support, README docs.

## Non-goals

- Syncing tickets as resources into the `.c1z` (ticketing ≠ sync).
- Exposing assignee/group/brand as schema fields (routing belongs to Zendesk
  triggers/automations or form defaults; revisit on customer demand).
- `integer`/`decimal` custom fields in schemas: **skipped in v1** with a debug log.
  Both reference connectors avoid SDK number fields (baton-jira maps numbers to string
  fields with a `TODO use number field type`; baton-freshservice skips them with a
  `TODO add number type to c1`), indicating C1 GUI support is unproven; and mapping them
  as strings without native-type recovery risks 422s on create. Revisit when C1 renders
  number fields. (Read-path robustness for numeric values is still required — R12.)
- Custom ticket statuses (`custom_status_id`, `/custom_statuses`): v1 reports the six
  system statuses only; documented limitation.
- `Idempotency-Key` on create: needs per-request headers; documented follow-up
  (duplicate-create risk on network-level retries is accepted for v1).
- Ticket comments/attachments beyond the initial comment.
- Ticket updates after creation (C1's model is create + poll; no update path).

## Requirements

- **R1 — Capability & wiring.** `*connector.Connector` implements
  `connectorbuilder.TicketManagerLimited` (all six methods), with a compile-time assertion
  `var _ connectorbuilder.TicketManagerLimited = (*Connector)(nil)`. `getConnector` in
  `cmd/baton-zendesk/main.go` appends `connectorbuilder.WithTicketingEnabled()` to the
  returned opts iff `cfg.Ticketing` is true.
  **Semantics (SDK-verified):** once the interface is implemented, the SDK always
  registers the ticket manager, advertises `CAPABILITY_TICKETING`, and serves the
  ticketing RPCs — regardless of the flag. The flag controls only
  `ConnectorMetadata.ExternalTicketSettings.Enabled`, which is what C1 reads to decide
  whether to route tickets to this connector. Sync/provisioning/actions behavior is
  unchanged either way.
  *Accept:* build passes with the assertion; metadata shows
  `ExternalTicketSettings.Enabled == true` iff `--ticketing` is set.
- **R2 — Config.** `pkg/config/config.go` adds
  `field.TicketingField.ExportAs(field.ExportTargetGUI)` to `ConfigurationFields`;
  `conf.gen.go` regenerated via `go generate ./...` (never hand-edited), exposing
  `cfg.Ticketing`.
  *Accept:* generated file contains the `Ticketing` accessor; `--ticketing` flag parses.
- **R3 — ListTicketSchemas (forms mode).** Returns one `v2.TicketSchema` per **active**
  ticket form. Schema `Id` = form ID (decimal string), `DisplayName` = form name. Custom
  fields = the form's `ticket_field_ids`, resolved against the account's ticket fields,
  filtered to **active, custom** (`removable: true`) fields, mapped per R6. The client
  **drains all forms pages internally** (R12) and `ListTicketSchemas` returns the full
  schema list in one call with an empty SDK next-page token — Zendesk caps accounts at
  300 forms, so a single bounded drain is both safe and the only way to evaluate the
  zero-active-forms fallback (R4) correctly across pages.
  *Accept:* (a) fixture of 2 active + 1 inactive form → exactly 2 schemas whose field sets
  match each form's `ticket_field_ids` ∩ active custom fields, next token empty; (b)
  two-page forms fixture where page 1 is all-inactive and page 2 has one active form →
  exactly that one schema and **no** `"default"` schema (proves the drain spans pages
  before the fallback decision).
- **R4 — Default-schema fallback.** `ListTicketSchemas` returns exactly one synthetic
  schema (`Id = "default"`, `DisplayName = "Default"`, all active custom ticket fields)
  when, and only when:
  - the forms fetch returns HTTP **404** (endpoint absent — the presumed plan-gate signal;
    classified by extracting the raw `zendesk.Error` via `errors.As` and branching on
    `.Status()`, never on wrapped gRPC codes), or
  - the forms drain succeeds with **zero active forms across all pages**.
  **All other failures propagate as errors** — explicitly including 401/403 (token/scope
  problems must surface loudly, not be masked by a degraded schema), 429, and 5xx. Fail
  closed: never present wrong schemas.
  **Release-gate contingency (closes A3):** before release, verify the forms endpoint's
  behavior on a live non-forms-plan trial account. If the observed plan-gate signal is a
  status other than 404 (e.g. 402, or a 403 distinguishable by Zendesk error body), add
  that *verified* signal to this fallback branch (with a body-based discriminator if
  needed) before shipping; if such plans return 200 with a default form, no change is
  needed (that form's schema is served). Until verified, the residual risk is a hard
  error on such plans — acceptable (loud, actionable) but not silent misbehavior.
  *Accept:* fixture tests: 404→fallback, zero-active-forms-across-pages→fallback,
  403→error, 429→error, 500→error.
- **R5 — Schema contents (both modes).** Every schema carries:
  - `Statuses`: the six system statuses (`new`, `open`, `pending`, `hold`, `solved`,
    `closed`), `Id` = raw status value, `DisplayName` = title-case label. All six are
    listed because C1 displays observed ticket states; only a subset is creatable (R7).
  - `Types`: the four ticket types (`problem`, `incident`, `question`, `task`) —
    advertisement only; the SDK does not forward `ticket.Type` to `CreateTicket` (see
    context), so the create path uses the synthetic `type` field below.
  - Synthetic pick-string custom fields (extracted in CreateTicket, never sent to Zendesk
    as `custom_fields` entries):
    - `priority` — `PickStringFieldSchema("priority", "Priority", false,
      ["low","normal","high","urgent"])`
    - `type` — `PickStringFieldSchema("type", "Type", false,
      ["problem","incident","question","task"])`
  *Accept:* schema fixture asserts statuses/types/priority/type exactly.
- **R6 — Field type mapping (schema direction).** Zendesk `ticket_fields.type` → SDK
  constructor:
  | Zendesk type | SDK schema field |
  |---|---|
  | `text`, `textarea`, `regexp`, `partialcreditcard`, `lookup` | `StringFieldSchema` |
  | `tagger` | `PickStringFieldSchema` (allowed values = `custom_field_options[].value`) |
  | `multiselect` | `PickMultipleStringsFieldSchema` (allowed values = option values) |
  | `checkbox` | `BoolFieldSchema` |
  | `date` | `TimestampFieldSchema` |
  | `integer`, `decimal` | skipped with debug log (see Non-goals) |
  | any other/unknown type | skipped with debug log (omit, don't error) |
  Field key/id = the Zendesk field ID formatted with `strconv.FormatInt` (never `%v`).
  `Required` = Zendesk `required` (the "required to solve" flag — deliberate choice: a
  ticket that cannot be solved is a worse failure than an extra prompt at request time).
  System fields (`removable: false`) are never schema custom fields.
  *Accept:* table-driven test covering every row incl. integer/decimal and unknown-type
  skips.
- **R7 — CreateTicket.** Given `(*v2.Ticket, *v2.TicketSchema)` (the SDK forwards
  DisplayName, Description, Status, Labels, CustomFields, RequestedFor — nothing else):
  1. Validate with `valid, err := sdkTicket.ValidateTicket(ctx, schema, ticket)`. The
     SDK signals invalidity via the **bool**, not the error (it returns `(false, nil)`
     for nearly all invalid paths); treat the ticket as invalid when `err != nil ||
     !valid` and return `errors.Join(fmt.Errorf(...),
     sdkTicket.ErrTicketValidationError)` — the jira reference pattern.
  2. Build the create payload:
     - `subject` ← `DisplayName`; `comment.body` ← `Description`, falling back to
       `DisplayName` if empty; if both empty → `InvalidArgument` (Zendesk requires a
       comment).
     - `ticket_form_id` ← schema `Id` parsed as int64, omitted for `"default"`.
     - `requester_id` ← `RequestedFor.Id.Resource` parsed as int64 when `RequestedFor` is
       set (native Zendesk user ID per repo convention); parse failure →
       `InvalidArgument`. Unset → field omitted (Zendesk defaults the requester to the
       authenticated API user).
     - `tags` ← `Labels`.
     - `status` ← `ticket.Status.Id` only if ∈ {`new`, `open`, `pending`, `hold`};
       `solved`/`closed` → `InvalidArgument` with a clear message (Zendesk rejects
       creating closed tickets, and solved requires an assignee — fail closed at our
       boundary, not with an opaque 422); any other value → omitted.
     - `priority` / `type` ← the synthetic pick-string fields when present (extracted
       before the R8 loop).
     - `custom_fields` ← reverse mapping per R8.
  3. POST via the client (R12); map the response per R9 and return it.
  *Accept:* mapping unit test (full ticket → exact JSON payload); validation-failure
  test; requester parse-failure test; solved/closed-status rejection test.
- **R8 — Field value mapping (create direction).** Pure type-switch on the SDK value —
  no native-type re-fetch, no proto annotations:
  | SDK value | Zendesk `custom_fields[].value` |
  |---|---|
  | `StringValue` | string |
  | `PickStringValue` | string (option tag value) |
  | `PickMultipleStringValues` | `[]string` |
  | `BoolValue` | bool |
  | `NumberValue` | JSON number (defensive completeness — v1 schemas never produce number fields, but the SDK type must not be dropped silently if present) |
  | `TimestampValue` | `.AsTime().UTC().Format("2006-01-02")` — formatted in UTC explicitly so the date is deterministic (date is Zendesk's only temporal custom-field type) |
  | unset/nil value | field omitted |
  The synthetic `priority` and `type` fields are extracted before this loop and never sent
  as Zendesk custom fields. Custom-field IDs parse back from the schema key via
  `strconv.ParseInt`; parse failure → `InvalidArgument`.
  *Accept:* table-driven test per row + priority/type extraction + omission of nil values.
- **R9 — GetTicket & completion.** `GetTicket(ctx, id)` fetches the ticket and maps:
  `Id` = `strconv.FormatInt(t.ID, 10)`, `DisplayName` = subject, `Description` =
  description, `Status` = `{Id: status, DisplayName: title-case}`, `Labels` = tags,
  `CreatedAt`/`UpdatedAt` from timestamps, `Url` = agent-facing URL
  `https://<subdomain>.zendesk.com/agent/tickets/<id>`; when subdomain is empty
  (`--base-url` test mode) fall back to the API `url` field — an API-shaped URL, not an
  agent URL, so tests assert the API-shaped value (no production impact: subdomain is a
  required config field). When status is `solved` or
  `closed`, `CompletedAt` = `updated_at` — a documented **upper-bound approximation**
  (Zendesk exposes no `closed_at` on the ticket object; `updated_at` drifts forward on
  post-solve edits; the metrics endpoint is out of scope). Completion detection is the
  status check, not the timestamp. Non-numeric ticket ID → `InvalidArgument`; Zendesk 404
  propagates as `NotFound` via `wrapZendeskError`.
  *Accept:* mapping tests for open + solved tickets (CompletedAt set only for the
  latter), URL construction both modes.
- **R10 — GetTicketSchema.** `GetTicketSchema(ctx, "default")` rebuilds the default
  schema (all active custom fields). Numeric ID → fetch that form (`GetTicketForm`) and
  build per R3/R5/R6; **inactive form → `NotFound`** (matches R3's active-only model);
  fetch 404 → `NotFound`. Non-numeric, non-`"default"` ID → `InvalidArgument`.
  *Accept:* tests for all four paths (default, active form, inactive form, invalid ID).
- **R11 — Bulk methods.** `BulkCreateTickets`/`BulkGetTickets` loop per item, calling the
  single-item methods; per-item failures populate the response item's `Error` string
  (batch never fails wholesale); each request's annotations merge into its response item
  (external-ticket-ref round-trip), matching jira/servicenow/freshservice boilerplate.
  *Accept:* test with one good + one bad item → one success, one `Error`, nil top-level
  error.
- **R12 — Client methods.** New `pkg/client/tickets.go`:
  - `CreateTicket(ctx, ticket) (Ticket, error)` and `GetTicket(ctx, int64) (Ticket, error)`
    — implemented as **raw calls** (`z.client.Post` / `z.client.Get`, the established
    pattern in this client) against `POST /api/v2/tickets.json` / `GET
    /api/v2/tickets/{id}.json`, decoding into **local types** in `pkg/client`:
    a `Ticket` struct plus a `CustomField` (ID + value) whose decoder accepts string, bool, JSON number,
    `[]string`, and null. Rationale: go-zendesk's `CustomField.UnmarshalJSON` rejects JSON
    numbers, so its typed ticket methods hard-fail on any ticket carrying a numeric
    custom-field value — including agent-set values on fields the connector never touches.
  - `ListAllTicketForms(ctx) ([]zendesk.TicketForm, error)` — drains go-zendesk
    `GetTicketForms` offset pagination across all pages with a hard cap of 10 pages
    (forms are account-capped at 300 ≈ 3 pages at 100/page); cap trip → error, not
    truncation (bounded traversal, fail closed).
  - `ListAllTicketFields(ctx) ([]zendesk.TicketField, error)` — drains cursor pagination
    (`page[size]=100`) with a hard cap of 50 pages (5,000 fields); cap trip → error, not
    truncation (bounded traversal, fail closed).
  All errors wrapped with the existing `wrapZendeskError` + `baton-zendesk:` prefix.
  Plan-gate classification (R4) inspects the raw `zendesk.Error` HTTP status via
  `errors.As` (the wrapper uses `errors.Join`, so the chain is preserved) — never wrapped
  gRPC codes.
  *Accept:* httptest-based client tests using the existing drain-loop convention,
  including: pagination-cap tests for both drains; a fixture decode of a create/get
  response whose `custom_fields` contain a **JSON number**, a string, a bool, and an
  array (must not error); the two-page forms drain test (R3b).
- **R13 — Test server.** `cmd/test-server` gains `handlers_tickets.go`: `POST
  /api/v2/tickets.json`, `GET /api/v2/tickets/{id}.json`, `GET /api/v2/ticket_fields.json`,
  `GET /api/v2/ticket_forms.json`, with seed data (≥2 forms; ≥1 field of each mapped type
  plus one integer field to exercise the skip and the numeric read path) and stateful
  ticket creation.
  *Accept:* an automated integration test (in `cmd/test-server`, alongside the unexported
  handler/state types it drives — it imports the real `pkg/connector` + `pkg/client`
  stack) spins up the test-server handlers via `httptest`, constructs the `Connector`
  against them, and drives
  ListTicketSchemas → CreateTicket → GetTicket end-to-end under `go test ./...`. The
  manual `--ticketing --list-ticket-schemas` / `--create-ticket` CLI flow against the
  standalone test server is documented in the test-server README as a secondary check.
- **R14 — Docs.** README gains a Ticketing section: enabling `--ticketing`, schema-per-form
  model + default fallback, plan-gating note, token scope requirements, v1 limitations
  (integer/decimal fields, custom statuses, idempotency).
  *Accept:* README renders and states all of the above.

## Existing-code reconciliation

- **Native IDs, no ExternalId:** this repo intentionally uses native Zendesk numeric IDs as
  C1 resource IDs everywhere (no `WithExternalID()` anywhere). Ticketing follows suit:
  `RequestedFor.Id.Resource` and ticket IDs are parsed as native IDs with
  `strconv.ParseInt`. Verified against the SDK: the builder forwards `RequestedFor` to the
  connector, and baton-servicenow consumes it the same way. (baton-freshservice extracts
  the user-trait email instead — a Freshservice-API-shaped choice, not a contract
  requirement.) No deviation.
- **Connector struct gains methods, no new builder:** `ResourceSyncers()` untouched;
  ticketing methods live on `*Connector` in a new `pkg/connector/ticket.go` — same shape
  as `actions.go` extending the connector for the actions capability.
- **Client conventions:** new methods reuse `wrapZendeskError`, the `baton-zendesk:` error
  prefix, the raw `Get`/`Post` + local-struct pattern already used for memberships, cursor
  pagination helpers where the endpoint supports it, and the established test conventions
  (`newTestClient`, drain loops). Named deviations: (a) ticket create/read bypasses
  go-zendesk's typed methods — justified by its number-rejecting `CustomField`
  unmarshaler; (b) `ListTicketForms` uses offset pagination — the endpoint's documented
  mode via go-zendesk; (c) integer/decimal schema fields are skipped — matches
  jira/freshservice precedent on unproven C1 number-field rendering.
- **Config codegen:** field added to `config.go`; `conf.gen.go` regenerated, never edited.
- **`main.go`:** only the opts slice in `getConnector` changes (nil → conditional
  ticketing opt), mirroring baton-jira/freshservice.
- **Behavioral compatibility:** sync, provisioning, and actions are unchanged. Note:
  because capability advertisement keys off interface implementation (not the flag),
  merged-but-unflagged builds advertise `CAPABILITY_TICKETING` with
  `ExternalTicketSettings.Enabled=false`; C1 routes tickets only when enabled. This is
  identical to baton-jira/servicenow/freshservice behavior.

## Data model / API / contracts

- **SDK contract:** `connectorbuilder.TicketManagerLimited` (vendored v0.20.1,
  `pkg/connectorbuilder/tickets.go:27-35`). Effective `CreateTicket` input surface:
  DisplayName, Description, Status, Labels, CustomFields, RequestedFor (builder-verified).
- **Schema ID space:** `"default"` | decimal form ID. Form IDs are numeric, so no
  collision. Synthetic field IDs `priority`/`type` cannot collide with real field keys
  (those are numeric strings).
- **Zendesk endpoints used:** `POST /api/v2/tickets.json`, `GET /api/v2/tickets/{id}.json`
  (raw + local types), `GET /api/v2/ticket_fields` (cursor, go-zendesk types),
  `GET /api/v2/ticket_forms` + `GET /api/v2/ticket_forms/{id}` (go-zendesk types).
- **New files:** `pkg/connector/ticket.go` (six interface methods + mapping),
  `pkg/connector/ticket_schema.go` (schema/field mapping helpers, keeps files small per
  lint), `pkg/client/tickets.go` (raw ticket calls + local `Ticket`/`CustomField`
  types), `cmd/test-server/handlers_tickets.go`, plus tests
  (`pkg/connector/ticket_test.go`, client test additions).
- **Zendesk field-required semantics:** `required` = required-to-solve (agent),
  `required_in_portal` = end-user portal; schema uses `required` only.

## Edge cases & failure modes

- **Forms endpoint failure classification:** 404 → fallback schema; 401/403 → error
  (scope/auth problems surface, never masked by a degraded schema); 429/5xx → error (SDK
  retries). Zero active forms → fallback. Classification on raw HTTP status via
  `errors.As`, not gRPC codes. Behavior on non-forms plans is undocumented upstream —
  see A3.
- **Numeric custom-field values on read:** any ticket may carry agent-set numeric values;
  the local `CustomField` decoder accepts them (blocker fixed by design; regression-
  tested per R12).
- **Form referencing inactive/deleted fields:** `ticket_field_ids` entries missing from the
  fields map are skipped silently (Zendesk allows stale references).
- **Empty description:** fall back to subject; both empty → `InvalidArgument`.
- **Create-time status:** `solved`/`closed` rejected at our boundary with
  `InvalidArgument` (Zendesk 422s on closed-at-create; solved requires an assignee).
- **Unknown custom-field value type in CreateTicket** (SDK adds a new oneof): return
  `InvalidArgument` naming the field — never send a guessed value.
- **Requester not a synced team member:** any numeric ID is passed through; Zendesk
  validates and returns 422 → `InvalidArgument` (mapped by `wrapZendeskError`).
- **Duplicate creates on retry:** no Idempotency-Key in v1; SDK-level retries happen only
  on transport/429 errors before a response is received. Risk documented (Non-goals).
- **Rate limits:** 429 → `codes.Unavailable` (existing mapping) → SDK retryer backs off.
- **Large field counts:** pagination cap (R12) errors rather than returning a silently
  truncated schema.
- **`closed` without `solved`:** completion check is status ∈ {solved, closed}, not a
  timestamp field, so direct-to-closed tickets are still detected.
- **Form deactivated between ListTicketSchemas and CreateTicket:** create proceeds with
  the stale `ticket_form_id`; Zendesk accepts or 422s — either way the error path is
  clean (`InvalidArgument`).
- **Concurrency:** ticketing task invocations are independent one-shots; no shared mutable
  state is introduced (no caching in v1).

## Assumptions & open questions

- **A1 (SDK-verified):** the gRPC builder passes the schema from the request to the
  connector's `CreateTicket` and forwards only the six input fields listed in Data model —
  verified in vendored `connectorbuilder/tickets.go:150-180`, matching how all three
  reference connectors consume it.
- **A2 (verified within repo convention):** `RequestedFor` resources carry the native
  Zendesk user ID in `Id.Resource` — holds for this connector's team_member resources
  (native-ID convention repo-wide) and matches baton-servicenow's consumption pattern.
- **A3 (closed by R4's release-gate contingency):** ticket forms' exact error behavior on
  non-forms plans is undocumented; R4 handles 404 + zero-active-forms and defines the
  concrete pre-release verification step and the action to take for each possible outcome
  (different signal → add verified signal to fallback; 200-with-default-form → no
  change).
- **A4 (replaced by design):** custom-field value robustness is covered per direction by
  the requirement that owns it — encode by R7's exact-JSON-payload test, decode by R12's
  `CustomField` fixture test (JSON number/string/bool/array) — not assumed of
  go-zendesk (whose unmarshaler is known-broken for numbers).
- **Q1 (deferred):** should `priority`/`type` defaults be configurable per connector
  instance? Not in v1; forms can set defaults in Zendesk.
- **Q2 (deferred):** Idempotency-Key support via a raw call with per-request header —
  follow-up issue after v1 (the raw-call plumbing from R12 makes this easy to add).
- **Q3 (deferred):** revisit integer/decimal schema fields when C1 GUI number-field
  rendering is confirmed (tracked alongside the jira/freshservice TODOs).
