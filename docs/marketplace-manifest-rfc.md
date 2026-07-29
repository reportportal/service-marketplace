# marketplace-manifest.json — Author RFC (PUB-001)

**ID:** PUB-001  
**Status:** Phase 1  
**Audience:** Official plugin authors (ReportPortal / EPAM)  
**Normative references:** Marketplace plan §6.2–§6.3, ADR-002

## Purpose

Every marketplace-distributed plugin `.jar` **must** contain a root entry `marketplace-manifest.json`. The registry extracts this file on publish and treats it as the **single source of truth** for author-supplied metadata.

### Immutability

Author-supplied manifest fields are **immutable** once a version is published. The Operator UI and registry APIs cannot edit `id`, `name`, `description`, `category`, `access`, `compatibility`, or other in-jar fields on the registry side (ADR-002). To fix metadata, publish a **new version** with a corrected manifest.

## Schema

Place the file at the **root** of the JAR (same level as typical `plugin.properties` / PF4J metadata).

### Required fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Stable plugin id (`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`). Must match publish target. |
| `name` | string | Display name |
| `version` | string | Semver (e.g. `1.4.2`). Must match publish target on subsequent publishes. |
| `description` | string | Short description |
| `author` | object | `{ "name": string, "email"?: string, "url"?: string }` |
| `license` | string | SPDX id or license name (e.g. `Apache-2.0`) |
| `category` | string | Controlled vocabulary (below) |
| `compatibility` | object | `{ "reportportal": "<semver-range>" }` |

### Optional fields

| Field | Type | Description |
|-------|------|-------------|
| `homepage` | URI | Docs / project page |
| `access` | `public` \| `premium` | Default `public` |
| `contactUrl` | URI | **Required when `access` is `premium`** — purchase / contact CTA |

### Controlled `category` vocabulary

Exactly one of:

- `bug-tracking` — Jira, GitLab Issues, Azure DevOps, …
- `notifications` — Slack, Email, Telegram, …
- `authorization` — LDAP, SAML, AD, …
- `import` — JUnit, Robot Framework, …

Unknown values are rejected with HTTP `422`.

### Compatibility model

`compatibility.reportportal` is a single-axis semver-style range against the ReportPortal release (CalVer, e.g. `25.1`).

Recommended default: open upper bound, e.g. `">=25.1"`.

Use an explicit upper bound only when a future RP release is known to break the plugin.

## Examples

### Public plugin

```json
{
  "id": "plugin-jira-cloud",
  "name": "Jira Cloud Integration",
  "version": "1.4.2",
  "description": "Post and link Jira Cloud issues from ReportPortal",
  "author": {
    "name": "ReportPortal Team",
    "email": "support@reportportal.io",
    "url": "https://reportportal.io"
  },
  "license": "Apache-2.0",
  "category": "bug-tracking",
  "compatibility": {
    "reportportal": ">=25.1"
  },
  "homepage": "https://reportportal.io/docs/plugins/jira-cloud",
  "access": "public"
}
```

### Premium plugin

```json
{
  "id": "plugin-quality-gate",
  "name": "Quality Gate",
  "version": "2.0.0",
  "description": "Quality Gate rules and enforcement",
  "author": {
    "name": "ReportPortal Team",
    "email": "support@reportportal.io"
  },
  "license": "Proprietary",
  "category": "notifications",
  "compatibility": {
    "reportportal": ">=25.1"
  },
  "access": "premium",
  "contactUrl": "https://reportportal.io/pricing/service-packages/"
}
```

## Publish bundle siblings

Alongside the `.jar`, the publish API accepts optional siblings (not inside the jar):

- `CHANGELOG.md` — long-form release notes
- Up to 5 screenshot images (PNG/JPEG, ≤ 2 MB each). Alphabetical filename order is display order (prefix with `01-`, `02-`, …).

The registry stores siblings under the version directory and returns absolute CDN URLs on version detail (`changelogUrl`, `screenshotUrls[]`).

## Machine-readable schema

See [`schemas/marketplace-manifest.schema.json`](schemas/marketplace-manifest.schema.json).
