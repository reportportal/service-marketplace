---
name: marketplace-story
description: Implements one ReportPortal marketplace user story in service-marketplace. Use when implementing a marketplace story, a US-MRKT requirement, or when changing the registry, operator UI, publish path, or licensing code.
---

# Marketplace story

One story only. Do not add work the story does not ask for.

Do not edit `.github/workflows`. Do not write secrets, Secret Manager values, or Terraform. Do not commit or push unless the person asks. The pipeline commits after `scripts/check.sh` passes.

New files belong under `cmd/`, `internal/`, `web/operator/`, `docs/openapi/`, or `docs/schemas/`.

Sensitive surfaces are `internal/auth`, publish OIDC, `internal/license`, signed URLs, and `docs/openapi`. Name each one the change would touch, and stop for a person before editing it.

If the story needs no code change in this repository, say so and do not edit.

Each acceptance criterion maps to a test. An OpenAPI change updates `docs/openapi/service-marketplace-v1.yaml`, `internal/httpapi/wire_contract_test.go`, and `internal/openapispec/` in the same change.

Before you finish, run this from the repository root:

```bash
bash scripts/check.sh
```

When asked for a plan, return JSON only:

```json
{
  "story_id": "US-MRKT-...",
  "no_code_change": false,
  "no_code_reason": "",
  "files": ["internal/httpapi/health.go"],
  "tests": [{"criterion": "acceptance criterion text", "test": "TestName"}],
  "openapi_changes": false,
  "sensitive_surfaces": [],
  "notes": ""
}
```

Copy each acceptance criterion into `tests[].criterion`. `sensitive_surfaces` uses the names listed above. For no code change, set `no_code_change` true, give the reason, and leave `files` and `tests` empty.
