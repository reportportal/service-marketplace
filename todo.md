# TODO

Full audit report (design vs code, all 26 states):
<https://claude.ai/code/artifact/46f273de-a993-4dfa-bf38-14bb0e2b8dc5>

## Scope — what the designer marked Ready for dev

Re-read 2026-09-11: **eight of the ten sections** in Figma file `s522VaX9KYO57AKFYW7zLE` now carry
the badge. Six of them are new since the August read.

| Section | Node | Screens | Marked |
|---|---|---|---|
| `01 · Catalog` | `26918:4358` | 13 | since August |
| `01C · Row states` | `27004:8154` | 18 row states | since August |
| `02 · Plugin Detail` | `26918:4364` | 27 | **new** |
| `03 · Versions` | `26918:4372` | 15 | **new** |
| `04 · Integrations` | `26918:4377` | 6 | **new** |
| `05 · Installation` | `26918:4380` | 7 | **new** |
| `06 · Settings` | `26918:4393` | 7 | **new** |
| `07 · Upload Plugin` | `27647:20231` | 8 | **new** |
| `08 · Org Discovery` | `26918:4398` | 2 | no — frames say `ITERATION 2`, section is at 30% opacity |
| `09 · Components (Marketplace)` | `27288:11576` | library | no |

Reading the flag: the Figma MCP does **not** expose it. `get_metadata` returns only ids, names,
types and geometry, and `get_screenshot` of a section does not include it either — the badge is
canvas chrome, not node content. What works: open the file in a browser, select the section in the
layers panel, press `shift+2` (zoom to selection), and the badge renders next to the section title.
Dev Mode's own "Ready for development" panel needs a Dev seat. Figma's REST API carries `devStatus`
on nodes and would make this one request — that needs a PAT, which the MCP server cannot use.

Use the **RP Auto Account** (`auto_epm-rpp_notifications@epam.com`) for the MCP: Full seat on EPAM
Figma, 200 calls/day. The personal account holds View seats only — 6 calls per *month*.

## Blocking a decision — for the designer

- [ ] **Is there a `community` trust tier?** Corrected 2026-09-11 after reading the Go source; the
      earlier entry here was wrong. The registry has **two independent axes**:
      `TrustTier = official | partner` (`internal/domain/types.go:68-72`) and
      `AccessTier = public | premium` (`:61-65`). There is **no `verified`** anywhere in the registry
      or in service-api, and **no `community`** either. The design asks for official / partner /
      community, so the only open question is `community`: drop it from the taxonomy, or add the
      value to `TrustTier` (a registry change, ours).
      Everything else about tier is **our** gap, not a decision: the client collapses both axes into
      one `free | premium` enum and throws trust away (`pluginTiers.ts:20-25`), which is why
      `Available. Partner` and `Installed. Premium` do not render even though both values reach the
      client today.
- [ ] **Is `Installed · disabled` iteration 1 or 2?** The frame name says `ITERATION 2`, but commit
      `00394c7c8` already built it, citing that frame. Either the label is stale or the PR carries
      scope early. Not a defect either way.
- [ ] **Row contact link, or promo modal?** Spec says the premium action "becomes a contact link";
      commit `3065591e5` ("One Discover Premium, not two") deliberately consolidated to a modal. The
      new `Discover Premium Modal` frame probably settles it — it is the only frame in the section
      with no `SPEC —` annotation, so read it before deciding.
- [ ] **Offline hides real warnings.** An advisory / blocked / removed plugin renders as a clean row
      while the registry is unreachable. Each spec is individually satisfied; their intersection is
      the problem.
- [ ] **Is the `Installed` chip sanctioned, and is chip order normative?** Neither appears in any of
      the ten catalogue frames, which all show the same six chips.
- [ ] **What defines an expired licence?** The designer flagged this on the frame himself. Nothing
      in the wire contract or the client carries expiry today — needs a backend answer first.

## Blocking a decision — for service-api

- [x] ~~Does `updateAvailable` already apply the ReportPortal compatibility range, or must the
      client?~~ **Answered 2026-09-11 by reading our own code: service-api applies it.**
      `ProductVersion.isCompatible` parses the range through `CompatibilityRange.parse`, and an
      unparseable or unknown range answers no, so no update is offered. Row state 5 is a client
      change, not a contract change.
- [ ] **The versions LIST drops what the versions TABLE needs.** `MarketplaceVersionDetail` carries
      `compatibility` and `advisory`; `MarketplaceVersionSummary` deliberately does not — its own
      javadoc says "Carries no compatibility range — only version detail does". But
      `03 · Versions` is a list, and three of its screens (`Table. With Incompatible`,
      `Table. No Compatible Versions`, `Table. With Advisory`) need both per row. Today that costs
      one detail request per version. Ask for a compatibility verdict (a boolean is enough) and the
      advisory on the summary. Raise it on service-api#2804 — this is our own narrowing, decided
      before `03 · Versions` was marked ready.
- [ ] Is `locked` derived per-plugin from the instance licence? If not, a paying customer can be
      shown Discover Premium.

## Code — ready to fix now

- [ ] **Badge collapsing.** `utils.js:182-186` returns an unranked array, so a plugin that is both
      blocked and advisory shows two pills. Spec: one badge, highest severity. The checked-in
      fixture `catalogue.json` already contains this exact case. No test asserts badge count.
- [ ] **Advisory severity ignored on the row.** `low/medium` and `high/critical` render identically
      — `advisory` is an object read as a boolean (`utils.js:185`), tone hard-coded
      (`pluginsItem.jsx:95`). Severity does reach the client; only the detail page uses it.
- [ ] **Incompatible available rows show a live Install.** No compatibility field on the row shape,
      no disabled variant in `getRowAction`, and `disabled` is never passed to the button. The user
      clicks and learns from a server error.
- [ ] **Outdated-but-incompatible installed rows are indistinguishable from up-to-date ones.** No
      compatibility concept exists in the page at all.
- [ ] **Tooltips: none of the twelve specified ones exist.** The page imports no tooltip component,
      though the app ships three. Only native `title` is used, twice, one with legacy copy. All the
      explanatory text exists — but only on the detail page.
- [ ] **Seven category chips, not six** (`pluginsFilter.js:42-50`) — pending the designer's answer
      above.
- [ ] **Premium `contactUrl` falls back to ReportPortal** (`premiumPromo.jsx:53`), so a premium
      plugin with no published contact link routes enquiries to us instead of the vendor. Connects
      to service-marketplace PR #13, which carried this field through to the listing.
- [ ] **Icon fallback works but is untested** (`pluginIcon.jsx:34,44`).
- [ ] **No in-flight feedback on Install.** An `installing` set exists in the store
      (`selectors.js:244`) but never reaches the catalogue. Neither specified nor built.

## Coverage of the six newly marked sections (audited 2026-09-11)

Roughly 30 of the ~70 new screens render today, 12 are partial and 26 do not exist. The gaps are
not 26 separate jobs — they collapse into five roots, listed under "Five roots" below.

| Section | Screens | Works | Partial | Absent |
|---|---|---|---|---|
| `02 · Plugin Detail` | 27 | 17 | 4 | 4 |
| `03 · Versions` | 15 | **0** | 4 | 11 |
| `04 · Integrations` | 6 | **6** | 0 | 0 |
| `05 · Installation` | 7 | **0** | 1 | 6 |
| `06 · Settings` | 7 | 2 | 2 | 3 |
| `07 · Upload Plugin` | 8 | 5 | 1 | 2 |

`04 · Integrations` needs no work: all six screens are served by ReportPortal's pre-existing
integration machinery, which this branch touched by 18 lines. `07 · Upload` is mostly the same
inherited modal.

### Five roots

- [ ] **Every failure is one generic toast.** A single `catch` in `sagas.js:362-365` dispatches
      `showDefaultErrorNotification` with the raw server string, and `error.code` is read nowhere in
      `controllers/plugins`. The registry does distinguish the cases — `LICENSE_JWT_MISSING`,
      `LICENSE_JWT_INVALID`, `LICENSE_ENTITLEMENT_DENIED`, `LICENSE_EXPIRED`
      (`internal/httpapi/errors.go:35-38`), a blocked-artifact 403 carrying `{blocked, blockedAt,
      reason}`, `409 CONFLICT` for a duplicate upload, 503 and a 410 tombstone. **Mapping those codes
      unlocks eight screens**: the four `Install. Failed.*`, the three `Versions. Downgrade. Failed.*`
      and `Upload. Failed. Version Exists`.
- [ ] **Nothing is confirmed before it mutates.** Install from the header
      (`availablePluginDetail.jsx:100-103`), install from a row (`installedTab.jsx:459-462`), upgrade
      (`:462-463`) and downgrade (`:499-501`) all POST on click. Four screens:
      `Install. Confirm. From Header`, `Install. Confirm. From Row`, `Versions. Upgrade. Confirm`,
      `Versions. Downgrade. Confirm`. The page already uses `confirmationModal` for enable/disable,
      so the pattern is in place.
- [ ] **Compatibility never reaches the client, and service-api is where it stops.** Corrected twice:
      the registry publishes it per version (`domain/types.go:90,102`) and service-api *applies* it
      (`ProductVersion`, `CompatibilityRange`) — but it does not *publish* it.
      `MarketplaceVersionDetail.compatibility` is service-api's model of the **registry's** response,
      deserialised by `MarketplaceClient.getVersion` and consumed internally by the install handler.
      The pinned contract with the UI is `app/src/controllers/plugins/__fixtures__/contract-marker.json`,
      which lists every field of every marketplace route service-ui is served: `compatibility` appears
      on none of them (verified by grep, zero hits).
      So `Install. Select Version. Latest not compatible` **is** blocked on a contract change — an
      earlier note here claiming otherwise was wrong. The frame needs two things service-api withholds:
      a per-version compatibility verdict, and the running ReportPortal release for the tooltip
      ("Needs ReportPortal 26.2 or later. This instance runs 26.1"). The second is `rp.product.version`,
      which `ProductVersion` owns and deliberately does not expose — `info.build.version` is the
      service's own build, not the product release.
- [x] ~~Two install refusal codes are unmapped.~~ **Done 2026-09-12.** 40044
      (`MARKETPLACE_PLUGIN_INCOMPATIBLE`) and 40045 (`MARKETPLACE_COMPATIBILITY_UNKNOWN`) now map to
      their own messages, and they say different things on purpose: incompatible points at choosing
      another version, unknown points at the instance, because the server refuses it when
      `rp.product.version` is unset or the declared range will not parse
      (`InstallMarketplacePluginHandlerImpl.verifyCompatible`). `sagas.test.js` used 40045 as its
      example of an unrecognised code and now uses 40099. Until the catalogue contract carries a
      verdict, this refusal is still the only place compatibility reaches a user at all.
- [ ] **`tier` is `free | premium` only** (`pluginTiers.ts:20-25`), so the client throws the trust
      axis away. Corrected 2026-09-11: `Available. Partner` and `Installed. Premium` are **not
      blocked** — `partner` and `premium` both reach the client today; only `Available. Community`
      waits on the designer question above.
- [ ] **`03 · Versions` is a flat list, not a table.** `pluginMarketplaceBlocks.jsx:284-304` renders
      version / date / action, and is hidden entirely on an available plugin's page (`onUseVersion`
      is null there). No header row, no columns, no per-version changelog or advisory, no row
      expansion, no history screen. Eleven of the fifteen screens have nothing to build on.

### Smaller, unblocked

- [ ] **`Upload. Confirm. Replace Existing` does not exist.** `saveFiles` POSTs straight to
      `URLS.plugin()` with no check for an installed plugin of the same name and version
      (`uploadPluginModal.jsx:85-88`). The only confirm in that flow cancels the upload.
- [ ] **Licence form has no field states.** `canSubmit` (`marketplaceLicence.jsx:119-123`) just
      disables the button — no `error`, no `isRequired`, no message, so the user sees a dead button
      and no reason. A server rejection is stored (`reducer.js:330-331`) but nothing reads it:
      `marketplaceTab.jsx:33-35` selects only `configured`/`customerId`/`loading`. So
      `Add Modal Required`, `Add Modal Invalid` and `Success` cannot render.
      Delete *is* confirmed (`marketplaceLicence.jsx:188-208`), but inline rather than as the
      `confirmationModal` dialog the rest of the app uses.
- [ ] **No in-flight or failed feedback on a row.** `installError` (`reducer.js:226-227`) and
      `isMarketplacePluginInstallingSelector` (`selectors.js:244-245`) are both written and exported,
      and both are read by nothing.
- [ ] **`Plugin Detail. State. Update Available` has no banner on the detail page** — the update CTA
      exists only as a catalogue row action.
- [ ] **`State. Uploaded Manually` is really "no registry entry".** Upload provenance is not stored,
      so it cannot be told from a plugin the registry simply does not know, and
      `Confirm. Uninstall. Uploaded Manually` has no variant copy.

### A contract fixture pinned to a response nobody sends

- [ ] `app/src/controllers/plugins/__fixtures__/catalogue.json:95` carries `"tier": "verified"`.
      Grepping both producers finds no such value: the registry defines `official | partner`
      (`internal/domain/types.go:71-72`) and service-api never emits the string. The fixtures are
      meant to be a slab of what the server really sends, and the wire-contract tests assert against
      them — so a contract test is currently pinned to an impossible response.
      **Fixed on both sides:** the service-ui fixture reads `official` and `wireContract.test.js`
      gained a guard rejecting any tier outside `official|partner` (2026-09-11); the generator that
      produces the fixture — `MarketplaceWireContractTest.java:372-379` in service-api — now emits
      `official` too (2026-09-12), so regenerating no longer reintroduces the impossible value.
      Note while fixing it: `partner` is declared in `TrustTier` but unreachable through the API —
      `lifecycle.SetTier` refuses everything except `official` (`internal/lifecycle/service.go:40-42`).
      So the second tier exists in the type and in no response.

## A crash the marketplace makes reachable

- [ ] `integrationSettingsContainer.jsx:97-99` resolves the settings component from a hardcoded
      seven-plugin map plus UI extensions. A marketplace-installed plugin in neither leaves
      `IntegrationSettingsComponent` undefined and the render at `:112` throws. The create path
      degrades gracefully for the same case (`addIntegrationModal.jsx:95-97`) — the settings path has
      no equivalent. Before the marketplace, installing an arbitrary third-party plugin was not
      possible, which is what makes this new.

## Not yet audited

- [ ] `Catalog.List.Discover Premium Modal` (`27090:46669`) — in a Ready-for-dev section, but read
      no further than its metadata. Bears directly on the contact-link question above.
- [ ] Frame-level specs for the six new sections. Only the screen inventory was pulled; the `SPEC —`
      annotations inside each frame are unread. Pull them per screen as work starts, not in bulk.

## service-marketplace PR #13

- [ ] Awaiting drcrazy's response on the `pf4jId` reply
      ([comment](https://github.com/reportportal/service-marketplace/pull/13#issuecomment-5498431568)).
      Evidence lives in `requirements/integration/STAGE0-id-mapping-decision.md`; all 14 official
      plugins' live `pluginId` values are in the reply.
- [ ] No human review yet on service-ui#5673, service-api#2804, or plugin-template#19 (draft).
