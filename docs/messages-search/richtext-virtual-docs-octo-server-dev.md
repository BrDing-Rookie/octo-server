# octo-server 开发文档 — 富文本虚拟子文档消费（Part B / B2）

> 拆分来源：`richtext-search-part-b-v2-virtual-docs.md`（B2 方案，已拍板）。本文件只含
> **octo-server reader 侧**改动；产出方（indexer）侧见配套
> 《octo-search-indexer 开发文档 — 富文本内嵌媒体虚拟子文档》。
> 基线：octo-server origin/main `8d2f4e3`。
> **前置依赖：indexer 侧 PR 必须先落地并完成 backfill**，否则本仓改动无数据可读（但 helper 是
> no-op，提前合也无害，见 §5）。

---

## 0. 这个仓负责什么

indexer 已为富文本内嵌的每个图片/文件产出独立 OS 子文档（`payload.type∈{2,5,8}` +
`virtual=true` + `parentMessageId`）。octo-server reader 需要：
1. **让这些子文档进 `_search_media`/`_search_files`**——白名单已命中，几乎零改；
2. **不让它们污染文本检索**（`_search`/`_search_all`/`_search_around`）——加排除 helper；
3. **可见性按父消息判定**——撤回父富文本 → 子媒体同步消失。

---

## 1. 输入契约（reader 从 indexer 读到的子文档）

只列 reader 实际消费的字段（完整契约见 indexer 开发文档 §2）：

| 字段 | 类型 | reader 用途 |
|---|---|---|
| `payload.type` | int (2/5/8) | 媒体/文件端点白名单命中（与普通媒体消息无差别） |
| `payload.image` / `payload.file` / `payload.video` | object | `singleMediaHit`/`singleFileHit` 直接投影（结构同普通消息） |
| `virtual` | bool | **文本检索端点排除**用（`must_not(virtual=true)`，reader 内部使用，不出现在响应里） |
| `parentMessageId` | long | **可见性查询**用（撤回/删除按父判定，reader 内部使用，不出现在响应里） |
| `messageId` | long | = 父值（indexer 契约保证）；reader 直接透传到响应 `message_id`，前端无感 |

> ⚠️ **关键契约**（由 indexer 单方面保证、reader 不重复实现）：子文档 `messageId`
> 字段必须 = 父富文本 messageId（long），**不是** ES `_id` 的复合键字符串
> （`<父>-rt<i>`）。reader 端 `singleMediaHit` / `singleFileHit` 现有逻辑直接读
> `doc.messageId` 透传到响应 `message_id`——**零代码改动**。前端拿这个 `message_id`
> 走"跳到原消息"接口即直达父富文本，**前端零改**。
>
> 反例：若 indexer 哪天把 `messageId` 字段塞成子文档的复合键字符串，reader 投影后
> 前端拿 `message_id` 走 sync 接口会因类型不对（前端期望 long）或父消息不存在
> （MySQL 没有该 id）而炸。indexer owner 改契约前**必须**同步 reader。
>
> 字段名（`virtual` / `parentMessageId`）由 indexer owner 最终锁定，以 indexer
> 开发文档 §7 为准，两边对齐后再动代码。

---

## 2. 改动一：`Doc` 补字段（`source.go`，仅 reader 内部使用）

```go
type Doc struct {
    // ... 既有字段
    ParentMessageID *int64 `json:"parentMessageId,omitempty"` // 内部用：可见性 coalesce
    Virtual         bool   `json:"virtual,omitempty"`         // 内部用：文本检索 must_not 标记
}
```

**对外响应字段完全不变**——`MediaHit` / `FileHit` 不新增 `parent_message_id`。
indexer 契约保证子文档 `messageId` 字段 = 父富文本 messageId（见 §1 关键契约），
现有 `singleMediaHit` / `singleFileHit` 透传 `doc.messageId` 到响应 `message_id`
即得到父值——**reader 投影代码零改、前端零改**。前端拿 `message_id` 走 sync 接口
即可直达父富文本。

---

## 3. 改动二：文本检索端点排除虚拟子文档（`dsl.go` 加 helper + 调用点）

仿 #444 `applySystemMessageHardFilter` 风格抽 helper：

```go
// applyExcludeVirtual 在文本检索/浏览 DSL 上挂 must_not(virtual=true)。
// _search / _search_all / _search_around 的消息表面不能把富文本派生的虚拟媒体子文档
// 当作独立消息召回（会与父富文本消息双发，且子文档没有正文 searchText）。
//
// _search_media / _search_files 的媒体/文件表面【不】调用本 helper：那里恰恰需要
// 这些子文档进入结果集。
func applyExcludeVirtual(b *elastic.BoolQuery) {
    b.MustNot(elastic.NewTermQuery("virtual", true))
}
```

调用点（4 处）：
- `buildSearchMessagesDSL`（`search_messages.go:159` 附近，紧挨 `applySystemMessageHardFilter`）
- `buildAnchorDSL`（`search_around.go:241`）
- `buildAroundDSL`（`search_around.go:255`）
- `buildSearchAllDSL`（`search_all.go`）——白名单加了 14（Part A）后，子文档以 `payload.type∈{2,8}`
  身份也在白名单内会被召回，**必须**加 `applyExcludeVirtual`

**不**调用 helper 的端点：`buildSearchMediaDSL`、`buildSearchFilesDSL`（它们正需要子文档）。

> 与 Part A 的衔接：Part A 把 14 加入 `_search_all` 白名单后，`_search_all` 就**必须**调
> `applyExcludeVirtual`。建议 **Part A 的 PR 里就引入该 helper** 并在 `_search`/`_search_all`/
> `_search_around` 调用——即使 indexer 还没写 `virtual` 字段，helper 只是 must_not 一个不存在
> 的字段，no-op、不影响现有召回；本 Part B 落地时无需再动这些位置。

---

## 4. 改动三：可见性按父 id 判定（`visibility.go` / `filterVisible`）

**现状**：`filterVisible` 把每条 doc 的 `messageId` 批量喂给 `messageService.GetMessages(...)`
查 MySQL 撤回/删除/接收方可见性。

**改法**：可见性键改成 `coalesce(parentMessageId, messageId)`——对 `virtual=true` 的 doc，用
`parentMessageId` 进 MySQL 查询；普通 doc 走原路径。

实现细节：
- `messageVisibilityProbe` 在分组前，先把每条 doc 映射成"可见性 key"（一个 int64）：
  `virtual && parentMessageId!=nil ? *parentMessageId : messageId`；
- 一次 MySQL 查询拿父消息可见性结果；
- 同一父下的多个子文档共享同一可见性判定结果。

保证：**撤回父富文本 → 所有派生媒体子文档同步消失**；**接收方对父不可见 → 子也不可见**。

> 注：indexer 写入时已把子文档 `revoked`/`visibles`/`spaceId` 继承父值（写时快照），本改动是
> reader 侧的**实时权威校验**（MySQL 为准），两者叠加，撤回后即使子文档 `revoked` 字段没来得及
> 同步，reader 也会按父 id 实时过滤掉。

---

## 5. 落地步骤（octo-server 侧，indexer 落地后）

1. `source.go` 加 `Doc.ParentMessageID` / `Doc.Virtual`（仅内部用：可见性 coalesce
   + 文本检索 must_not；**不**加任何对外响应字段）
2. `dsl.go` 加 `applyExcludeVirtual` helper
3. 4 个文本检索 builder（`_search` / `_search_all` / `_search_around` anchor+window）调用 helper
4. `filterVisible` 用 `coalesce(parentMessageId, messageId)` 做 MySQL 可见性查询
5. 测试：
   - **DSL pin**：`_search` / `_search_all` / `_search_around` 都带 `must_not(virtual=true)`；
     `_search_media` / `_search_files` **不带**（pin 验证两侧差异）
   - `filterVisible`：构造 virtual 子文档，撤回父 → 子被过滤
   - `buildMediaHits` / `buildFileHits`：virtual doc 投影出的响应 `message_id` = 父值
     （透传子文档 `messageId` 字段，依赖 indexer 契约——无 reader 代码改动）
6. **测试环境验证（集群 A，走 `dmworkim-develop`）**：
   - indexer rollout + backfill 完成后
   - `_search_media` / `_search_files` 查富文本里的"合同图片"/"xx.pdf"附件 → 应直接召回
   - `_search` / `_search_all` 命中富文本父消息（Part A 行为），不再额外冒出子文档污染列表

> 可提前合（解耦）：第 2、3 步的 `applyExcludeVirtual` 是 no-op 安全的，可随 Part A PR 先合；
> 第 1、4 步（消费 virtual 字段 + 可见性父 id）建议等 indexer 字段名锁定后再合。

---

## 6. 影响矩阵（octo-server 端点）

| 端点 | 改动 |
|---|---|
| `/_search` | 加 `MustNot(virtual=true)`（helper 一行） |
| `/_search_all` | 同上；Part A 加 14 白名单后**必须**加（否则子文档以 type=2/8 污染列表） |
| `/_search_around` | 同上（anchor + window 两处都调） |
| `/_search_media` | **响应字段完全不变**：白名单 type∈{2,5} 已命中虚拟子文档；响应 `message_id` 由现有逻辑透传子文档 `messageId` 字段（=父值，indexer 契约保证），**前端零改** |
| `/_search_files` | **响应字段完全不变**：白名单 type=8 已命中虚拟子文档；响应 `message_id` 由现有逻辑透传子文档 `messageId` 字段（=父值，indexer 契约保证），**前端零改** |
| `filterVisible` | 可见性键改 `coalesce(parentMessageId, messageId)` |
| 游标 / 分页 / 聚合 | **不动** |

---

## 7. 回滚（octo-server 侧）

- `applyExcludeVirtual` helper **留下无害**——即使 indexer 回滚不写 virtual 子文档，reader 端
  helper 也只是 must_not 一个不存在的字段，no-op。
- `filterVisible` 的 `coalesce` 改动对普通 doc 是原路径（`parentMessageId` 为 nil → 用
  `messageId`），无副作用，可留存。
- 回滚后 `_search_media`/`_search_files` 行为退回到"搜不到富文本内嵌媒体"——上线前状态，不更糟。

---

## 8. 跨仓依赖清单（开工前对齐）

| 依赖项 | 由谁定 | octo-server 影响 |
|---|---|---|
| `virtual` 字段名 | indexer owner | `Doc.Virtual` tag + helper term query 字段名 |
| `parentMessageId` 字段名 | indexer owner | `Doc.ParentMessageID` tag + 可见性 key 取值 |
| 子文档 `payload.type` 取值范围 | indexer | `_search_media`(2,5)/`_search_files`(8) 白名单是否覆盖全 |
| 子文档 `messageId`=父值、`timestamp`=父值 | indexer 契约保证 | **前端零改的关键前提**：reader 游标排序键不被打散，且响应 `message_id` 直接透传 = 父值，前端走 sync 直达父富文本 |
| indexer backfill 完成 | indexer | 测试环境验证的前置 |
