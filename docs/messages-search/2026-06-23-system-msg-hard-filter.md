# messages_search: hard-filter system messages from `_search_messages`

Date: 2026-06-23
Surface: `POST /v1/messages/_search` (`_search_messages` reader path)

## 1. Problem

Empty-keyword "browse" mode on `/v1/messages/_search` (legacy `_search` surface)
recalled control-plane events — "GroupCreate", "GroupMemberAdd", "Tip",
"FriendApply", etc. — instead of being limited to user-authored content. This
violates the indexer's "搜索硬过滤" contract for the 1000-2000 system-message
range.

## 2. Root cause

`buildSearchMessagesDSL` only excluded `payload.type == 99` (Cmd):

```go
b.MustNot(elastic.NewTermQuery("payload.type", payloadTypeCmd))
```

It did not exclude the **1000-2000 system range** defined by the indexer
(`FriendApply=1000` … `Tip=2000`). When the keyword is empty, the only
discriminator left is the channel filter + `revoked=false`, so every system
event indexed in the channel surfaced as a "hit".

## 3. Fix

Add a single `RangeQuery` to `must_not` in `buildSearchMessagesDSL`:

```go
b.MustNot(elastic.NewTermQuery("payload.type", payloadTypeCmd))
b.MustNot(elastic.NewRangeQuery("payload.type").Gte(payloadTypeSystemMin).Lte(payloadTypeSystemMax))
```

Constants `payloadTypeSystemMin = 1000` / `payloadTypeSystemMax = 2000` are
added to `modules/messages_search/source.go` alongside the existing
`payloadType*` family, with a comment that points back to the indexer spec
(§2.2) as the source of truth.

## 4. Affected endpoints

| Endpoint | Status | Why |
|---|---|---|
| `POST /v1/messages/_search` (`_search_messages`) | **Fixed** — adds 1000-2000 must_not range | Only surface that previously matched system events |
| `POST /v1/messages/_search_all` | Unchanged — already safe | Whitelist filter `terms payload.type in [1, 8, 11]` excludes anything outside text/file/mergeForward |
| `POST /v1/messages/_search_files` | N/A | Hard-filters `payload.type == 8` |
| `POST /v1/messages/_search_media` | N/A | Hard-filters `payload.type in [2, 5]` |
| `POST /v1/messages/_search_around` | Out of scope here | `buildAnchorDSL` is a separate code path; review tracked separately if needed |

## 5. Tests

- `TestBuildSearchMessagesDSL_FiltersSystemMessages` (new, `dsl_test.go`):
  asserts both the term (`payload.type == 99`) and range (`payload.type ∈
  [1000, 2000]`) clauses are emitted in `must_not`, in both keyword and
  empty-keyword (browse) branches. Walks the parsed query tree rather than
  relying on substring pins so the assertion survives unrelated DSL
  formatting changes.
- `TestBuildSearchMessagesDSL_Shape` and
  `TestBuildSearchMessagesDSL_NoKeywordSkipsMultiMatch` updated to also
  require `"gte":1000` / `"lte":2000` in the emitted DSL — keeps the
  literal-JSON pins in sync without dropping the existing `payload.type=99`
  pin.
- Staging verification (TODO): seed a channel with a `GroupMemberAdd`
  (`payload.type=1002`) event and a regular text message, hit
  `/_search_messages` with empty keyword, confirm only the text message is
  returned.

## 6. References

- Indexer spec §2.2 (search hard-filter contract):
  `~/Projects/_refs/wukongim-message-indexer/docs/specs/2026-06-04-v1.6-decisions.md`
- Code: `modules/messages_search/search_messages.go::buildSearchMessagesDSL`,
  `modules/messages_search/source.go` (`payloadTypeSystemMin`/`Max`)
