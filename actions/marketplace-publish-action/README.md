# Marketplace Publish Action

Composite GitHub Action that requests an Actions OIDC token (audience configurable, default `marketplace.reportportal.io`) and POSTs a multipart publish bundle to `service-marketplace`.

## Usage

```yaml
permissions:
  id-token: write
  contents: read

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: reportportal/marketplace-publish-action@v1
        with:
          registry-url: https://marketplace.reportportal.io
          jar-path: build/libs/plugin-jira-cloud-1.4.2.jar
          plugin-id: plugin-jira-cloud
          changelog-path: CHANGELOG.md
```

See [examples/publish.yml](examples/publish.yml) and [docs/examples/github-workflows/publish.yml](../../docs/examples/github-workflows/publish.yml).
