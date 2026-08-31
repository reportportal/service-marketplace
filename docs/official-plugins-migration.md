# Official plugins — migration to the registry (PUB-008)

This guide covers **US-MRKT-PUB-008**: move Official plugins onto `service-marketplace`.

## Per plugin

1. Add root `marketplace-manifest.json` to the plugin JAR (see [marketplace-manifest-rfc.md](marketplace-manifest-rfc.md)).
2. Set `compatibility.reportportal` floor to the oldest RP release the plugin is built/tested against; prefer an **open upper bound** (`>=25.1`).
3. Set `access` to `public` or `premium` (+ `contactUrl` for premium).
4. Set `pf4jId` to the plugin's `Plugin-Id` from `gradle.properties`/`build.gradle`, copied verbatim (`Azure DevOps`, `GitHub`, ...). It is what ReportPortal matches installed plugins on; omit it only if the JAR is not a PF4J plugin.
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
