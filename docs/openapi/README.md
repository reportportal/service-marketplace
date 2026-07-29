# service-marketplace OpenAPI specification

Source of truth for the Plugin Marketplace registry HTTP API (`/api/v1/`).

| File | Purpose |
| --- | --- |
| `service-marketplace-v1.yaml` | OpenAPI 3.0.3 spec — edit here only |
| `../scripts/sync-openapi-to-docs.mjs` | Copy bundled spec to the ReportPortal docs repo |

## Prerequisites

- Node.js 18+

## Install tooling

From `docs/`:

```bash
cd docs
npm install
```

## Lint

```bash
cd docs
npm run lint:openapi
```

Uses [Spectral](https://stoplight.io/open-source/spectral) with a trimmed ruleset suitable for this spec.

## Preview documentation

```bash
cd docs
npm run preview:openapi
```

Opens Redoc preview at `http://localhost:8080` (default Redocly port).

## Bundle

```bash
cd docs
npm run bundle:openapi
```

Writes `openapi/dist/service-marketplace-v1.yaml`.

## Sync to docs site

After lint passes, publish a copy to the ReportPortal docs repository:

```bash
cd docs
npm run sync:openapi
```

Or run the sync script directly (adjust `DOCS_REPO` if needed):

```bash
cd docs
node scripts/sync-openapi-to-docs.mjs
```

## Related documents

- [Marketplace implementation plan](../../../cursor/reportportal-plugin-marketplace-plan.md) — §6.2–§6.5
- [API Design-First ADR](../../../architecture-decisions/docs/adr/20220817-api-design-first.md)
