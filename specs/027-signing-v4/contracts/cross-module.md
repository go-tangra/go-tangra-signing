# Contract: Cross-module changes

## auth — `Profiles.Contacts` (research D1)

```proto
// Contacts returns the e-mail address of active members of a tenant. Policy:
// services allowed by policy.yaml only (svc/signing). The phone number is never
// returned.
rpc Contacts(LookupContactsRequest) returns (LookupContactsResponse);
message LookupContactsRequest { string tenant_id = 1; repeated string user_ids = 2; } // 1..100
message Contact { string user_id = 1; string display_name = 2; string email = 3; }
message LookupContactsResponse { repeated Contact contacts = 1; } // inactive/foreign/unknown omitted
```

- Tenant must match the caller's grant exactly like `Lookup`; users without an e-mail
  are omitted; audited (`profiles.contacts`, count only).
- auth `deploy/policy.yaml`: rule `signing-profiles` from `svc/signing` to
  `/auth.v1.Profiles/Lookup`, `/auth.v1.Profiles/ListMembers`, `/auth.v1.Profiles/Contacts`.
- Released as auth SDK `sdk/v4.x` + auth minor.

## notification — system templates (research D13)

Added to `internal/notify/systemtemplates.go` (English, with Bulgarian wording in the
body as v3), key prefix `signing.`:

| key | required variables |
|---|---|
| signing.invitation | document, sender, signer, link |
| signing.next_signer | document, sender, signer, link |
| signing.certificate_setup | signer, link |
| signing.reminder | document, sender, signer, link, reminder_no |
| signing.completed | document, recipient, link |
| signing.declined | document, signer, reason, link |
| signing.cancelled | document, reason, link |
| signing.expired | document, link |
| signing.certificate_locked | signer, locked_until, link |

notification `deploy/policy.yaml`: `svc/signing` added to the `Notifier/Send` rule.
Caller: `notifyclient.SendKey(ctx, tenant, key, email, vars, correlationID)`.

## scheduler — task types (research D12)

| type | scope | default cron | retries | payload schema |
|---|---|---|---|---|
| `signing:expire-submissions` | platform | `*/15 * * * *` | 1 | `{}` (no properties) |
| `signing:send-reminders` | platform | `0 * * * *` | 1 | `{"batch": integer 1..1000, default 200}` |

Results: `OK("expired N")`, `OK("reminded N, crl M, swept K")`; DB outages → `Retry`.
Scheduler `deploy/policy.yaml` `modules-register` gains `svc/signing`; scheduler
`discovery.static.signing: ["signing:9915"]`; signing policy `scheduler-execute`.

## Events (Valkey stream `platform:events:<tenant>`)

| type | data |
|---|---|
| `signing.submission.completed` | `{submission_id, template_id, final_version, audit_trail: bool}` |
| `signing.submission.cancelled` | `{submission_id, template_id, reason_code: cancelled\|declined}` |
| `signing.submission.expired` | `{submission_id, template_id}` |
| `signing.inbox` (to signer user ids) | `{signer_id, submission_id, state}` — live "To sign" refresh |

## Framework edge — `ConnectSources` (research D6)

`edge.Config.ConnectSources []string` validated like `FrameSources` (https origins,
port allowed, no path); `headersFilter()` emits `connect-src 'self' <origins>`; empty =
header byte-for-byte unchanged. Portal: `edge.connect_sources` (config), passed through
`edgeConfig`. Production: `https://localhost:53952 https://localhost:53953
https://localhost:53954 https://localhost:53955`.

## Warden (administrator document signing, FR-038)

The TSA credentials are a Warden secret id; signing fetches it on behalf of the
signed-in user (pattern of ipam 024). Warden `deploy/policy.yaml`: `svc/signing` added to
the on-behalf secret read rule.
