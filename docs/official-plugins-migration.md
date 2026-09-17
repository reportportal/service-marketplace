# Official plugins — migration to the registry (PUB-008)

This guide covers **US-MRKT-PUB-008**: move Official plugins onto `service-marketplace`.

## Per plugin

1. Add root `marketplace-manifest.json` to the plugin JAR (see [marketplace-manifest-rfc.md](marketplace-manifest-rfc.md)).
2. Set `compatibility.reportportal` floor to the oldest RP release the plugin is built/tested against; prefer an **open upper bound** (`>=25.1`).
3. Set `access` to `public` or `premium` (+ `contactUrl` for premium).
4. **Change the plugin's `Plugin-Id` to the registry id.** In `gradle.properties`/`build.gradle`, replace the current value (`Azure DevOps`, `JIRA Cloud`, `GitHub`, ...) with the id this plugin publishes under — lowercase, digits and hyphens only. This is the step that makes the marketplace able to recognise the plugin once installed: ReportPortal exposes `Plugin-Id` as `IntegrationType.name`, and that name is matched against the registry id byte for byte.

   **This is a breaking change for instances that already run the plugin.** `IntegrationType.name` is what existing integration records point at, so an instance carrying the old name keeps it and the catalogue will not recognise the plugin until it is reinstalled from the marketplace. That is the accepted cost of not carrying a second identifier — decided on service-marketplace#13, 2026-09-16. Plan the rename with the release that first publishes the plugin, not separately.
5. Optionally add `CHANGELOG.md` and screenshots for the publish bundle.
6. Add a GitHub Actions workflow using [`marketplace-publish-action`](../actions/marketplace-publish-action/) (see [examples](examples/github-workflows/publish.yml)).
7. Ensure the registry Helm `publishOidcTrust.allowedSources` maps `reportportal/<plugin-repo>` → `<plugin-id>`.
8. Publish to **staging**, then smoke-test:
   - `GET /api/v1/plugins` lists the plugin
   - `GET .../versions/{ver}` returns `sha256`
   - `GET .../artifact` returns `302` (public) or signed URL (premium)

## Checklist

- [ ] Manifest validates against JSON Schema
- [ ] First publish (`POST /api/v1/plugins`) or subsequent (`POST .../versions`) succeeds
- [ ] Tier is `official`
- [ ] Operator UI shows the plugin with read-only Official badge
- [ ] Staging install from RP `MarketplaceClient` (when available) verifies SHA-256
