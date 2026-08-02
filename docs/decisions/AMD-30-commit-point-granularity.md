# AMD-30-commit-point-granularity — "Committed" for AMD-04/FR-R-05 is per-version presence in `plugin.json.versions[]`, not literal `index.json` reference

> **Status.** Proposed correction to `requirements/AMENDMENTS-v1.md`, raised during
> `go3/publish-contract` implementation review. `requirements/` is not tracked in this
> git repository (it lives only in the primary checkout, outside any worktree's git
> history), so this write-up cannot be committed directly into
> `requirements/AMENDMENTS-v1.md` from an isolated worktree. It is checked in here,
> alongside the code it justifies, so the two do not silently disagree while the
> requirements-doc edit is applied by someone with write access to that path. The text
> below is written so it can be pasted in as AMD-30 verbatim.

| | |
|---|---|
| Severity | high |
| Blocks implementation | True |
| Target file | `requirements/reportportal-plugin-marketplace-plan.md` |
| Target section | §6.4 "Publish atomicity" / "Duplicate-version publish" (AMD-04's amendment text), FR-R-05, and the "Orphan cleanup" paragraph's parenthetical "(the commit point — *not* `plugin.json`, which carries no version list)" |
| Merged from | (raised during `go3/publish-contract` implementation review, not the original six-lens audit — filed here because AMD-04 already owns this section and a requirement must not be amended by commit message) |

## Rationale

AMD-04 defines "committed" as "referenced by `index.json`" and FR-R-05 repeats that
definition verbatim. Taken literally this is unimplementable for any plugin with more
than one published version: `index.json`'s per-plugin entry (`domain.IndexPlugin`)
carries exactly one field for version identity, `latestVersion` — by §6.4's own
directory-layout table and the `rebuildIndex`/`IndexPlugin` shape, `index.json` never
lists non-latest versions at all. The moment a plugin's second version publishes and
becomes `latestVersion`, its first version is no longer "referenced by `index.json`"
under any reading of that phrase — so a literal implementation of AMD-04 branch 1
("version not committed → publish proceeds and overwrites") would treat every
non-latest version as eligible for overwrite by a same-numbered republish with
different content, for the entire remaining lifetime of the plugin. That directly
contradicts FR-R-05's own guarantee ("a committed version is never overwritten") for
exactly the versions FR-R-05 exists to protect — it would only "protect" whichever
version happens to be the current latest at any given moment, which is a much weaker,
semver-order-dependent guarantee than what §6.2's legacy-hotfix workflow and FR-U-02's
version-history detail page assume. The orphan-cleanup paragraph's own parenthetical —
"`index.json` … carries no version list" — states the same fact as a design constraint
elsewhere in the same document, one section away from AMD-04's definition; the two were
never reconciled.

The alternative already implemented in `internal/publish/service.go`
(`domain.PluginState.Versions`, i.e. `plugin.json`) is a durable, append-only, full
per-version history: once an entry lands in `Versions` via a successful `plugin.json`
compare-and-swap, that fact persists for the life of the plugin regardless of which
version is later `latestVersion`, giving every version — not just the current latest —
the "never overwritten" guarantee FR-R-05 promises. It is checked (and, since the
`go3/publish-contract` BLOCKING fix, *committed*) before `index.json` is touched at all
— a strictly *earlier*, not later, point in the write sequence than the literal
amendment names — so nothing that was "committed" under AMD-04's literal wording stops
being committed under this correction; the correction only extends the guarantee to
versions the literal wording couldn't cover.

The previous implementation round adopted this same alternative but only argued it in a
source comment and a commit message, which the `go3/publish-contract` review correctly
rejected as amending a requirement outside the requirements corpus. This document is
that write-up, addressing what was asked: what breaks under the amendment's literal
definition (immutability of every non-latest version, silently, the moment a newer
version publishes), what the alternative guarantees instead (immutability of every
version, permanently, checked at or before the point `index.json` would have been
consulted), and — below — what the amendment text must say for the two to stop
disagreeing.

The rejected alternative — extending `index.json`'s schema to enumerate every version
per plugin, not just `latestVersion` — was considered and set aside: it changes a
CDN-cached, `max-age=300` public response shape consumed by the whole
catalogue-listing/orphan-cleanup surface, for a fact (`Versions[]`) that `plugin.json`
— already per-plugin-scoped and already read on every version-detail composition per
§6.4's response-composition table — records more cheaply and without inflating
`GET /api/v1/plugins`.

## Amendment text (for insertion into `requirements/AMENDMENTS-v1.md` as AMD-30)

In AMD-04's amendment text, replace "A version is **committed** iff it is referenced by
`index.json` (the commit point)" with:

> A version is **committed** iff it is present in `plugins/{id}/plugin.json`'s
> `versions[]` list. `plugin.json` is written before `index.json` is regenerated on
> every publish (§6.4 "Publish atomicity"), so this is a strictly earlier — never later
> — commit point than an `index.json` reference; `index.json` remains the commit point
> for **catalogue-listing visibility** (`GET /api/v1/plugins` showing the plugin/its
> `latestVersion` at all), which is a distinct guarantee from per-version immutability.

Apply the same substitution to FR-R-05's "a **committed** version (one referenced by
`index.json`, the §6.4 commit point)".

In the "Orphan cleanup" paragraph, replace "unreferenced by `index.json` (the commit
point — *not* `plugin.json`, which carries no version list)" with:

> unreferenced by `plugin.json`'s `versions[]` list (the per-version commit point — see
> AMD-30) **and** the plugin itself is unreferenced by `index.json`'s `latestVersion`
> pointer or, for FR-OP-04, `plugin.json` is a tombstone

— i.e. orphan cleanup must check `plugin.json`, not `index.json`, for the same reason
branch 1 must.

## Cross-reference

Implemented by `internal/publish/service.go`'s `publish()`; see the doc comment there
and `internal/publish/service.go`'s `PublishVersion` for the "committed" check this
document justifies. Regression coverage: `internal/publish/service_test.go`
(`TestPublishVersionDifferentContentReturns409VersionAlreadyPublished` and neighbors).
