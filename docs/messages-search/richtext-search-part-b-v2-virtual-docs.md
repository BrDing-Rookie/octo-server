# octo-server 富文本(type=14)消息搜索对接 — Part B（B2 方案）：虚拟媒体子文档

> 基于代码：octo-search-indexer `19c3ab8`（PR #21/#23）+ octo-server origin/main `8d2f4e3`。
> 上游 OS：集群 A（`search-opensearch-0`，OpenSearch 2.17.0，索引 `octo-message-v2`，
> 别名 `wukongim-messages-read`，免鉴权）。
> 范围：**富文本消息里内嵌的图片/文件，要能在 `_search_media` 和 `_search_files` 中搜到**。
> 路线：**B2 虚拟媒体子文档**（已拍板，B1 不再保留对比）。

---

## 0. 一句话结论

每条富文本消息里每个内嵌 image/file，由 indexer **额外产出一个独立 OS doc**，长得就像一条普通
图片(type=2)/文件(type=8)消息。`_search_media`/`_search_files` 现有白名单**直接命中**这些子
文档，octo-server 改动近乎为零，游标协议不动；可见性按父消息 id 判定。

---

## 1. 为什么这条路线（一句话）

- **基数天然正确**：一条富文本 N 张图 = N 个子 doc = N 个媒体命中，`1 doc=1 媒体` 的旧模型
  不必动。
- **octo-server 几乎零改**：`_search_media`/`_search_files` 现有 `payload.type∈{2,5,8}` 白名单
  + `singleMediaHit`/`singleFileHit` 直接复用，**游标 / 聚合 / 分页协议全部不变**。
- **代价集中在 indexer**：双写 + 一次性 reindex 富文本历史。换 octo-server 读侧的**稳定**。

---

## 2. 上游就绪现状（前置确认）

- mapping（`internal/esindex/mapping/octo-message.json`）已有 `payload.richText.searchText`。
- `buildRichText` 已经把富文本 content blocks 拍扁成 searchText（用于 Part A）；本 Part 需要
  **新增一个并行投影分支**——结构化抽取每个 image/file block。
- 实测：集群 A 现有 129 条 type=14 富文本 doc，例 `messageId=2062443880774537216`，
  `searchText="[图片] 消息测试 image.png"`——blocks 里有 image，**当前没有任何独立媒体子
  文档**。本设计上线后这条父消息会同时存在 1 个父 doc + ≥1 个媒体子 doc。

---

## 3. 子文档契约（indexer 侧产物）

```jsonc
{
  // ---- 主键 / 路由 ----
  "messageId":   <父messageId>,                 // long，与父相同；ES _id 用复合键，messageId 字段保留父值
  // _id:        "<父messageId>-rt<i>"           // 字符串复合键，避免主键冲突（i 从 0 起，按 content blocks 顺序）
  // _routing:   <父routing>                     // 与父同路由（channelId）

  // ---- 继承父消息的标识 / 时序 / 可见性源数据 ----
  "messageSeq":  <父messageSeq>,
  "clientMsgNo": "<父clientMsgNo>-rt<i>",        // 可省略，看 reader 是否实际读
  "streamNo":    "<父streamNo>",                 // 可省略
  "from":        "<父from>",
  "to":          "<父to>",
  "channelId":   "<父channelId>",
  "channelType": <父channelType>,
  "topic":       "<父topic>",
  "spaceId":     "<父spaceId>",
  "visibles":    [...],                          // 继承父
  "timestamp":   <父timestamp>,                  // epoch_second，与父一致
  "revoked":     <父revoked>,                    // 父撤回则子也算撤回（写入时同步）

  // ---- 业务 payload：长得就像 type=2 / type=5 / type=8 消息 ----
  "payload": {
    "type": 2,                                    // 或 5 / 8
    "image": {                                    // type=2：image block 投影
      "url":     "<block.url>",
      "name":    "<block.name>",
      "caption": "<block.caption>",
      "width":   <block.width>,
      "height":  <block.height>
    }
    // 或 "file":  { "url","name","caption","size","extension" }   // type=8
    // 或 "video": { "url","cover","width","height","second" }     // type=5（若富文本支持视频 block）
  },

  // ---- 父子追踪 ----
  "parentMessageId":   <父messageId>,             // long，与 messageId 同值；冗余但显式，方便审计
  "parentPayloadType": 14,                        // 父原 payload.type
  "virtual":           true,                      // 标记此 doc 由富文本派生

  // ---- payloadRaw 不写（无意义）/ meta 透传 ----
  "meta": {
    "timePluginReceivedMs": <继承>,
    "timeIndexedMs":        <子文档自身的索引时间>
  }
}
```

要点：
- **ES `_id`** 用复合键 `"<父messageId>-rt<i>"`（字符串），避免与父父消息的 `_id` 冲突。`_id` 是
  字符串，复合键不影响 `messageId` 字段保持 long。
- **`messageId` 字段**保留父值（long）——reader 的 cursor 排序键 `timestamp + messageId` 仍然有效；
  同一条父消息下所有子文档天然聚成一组（同 timestamp、同 messageId），分页里不会被打散到不同
  页之间——这是好性质，前端展示父消息附件时它们自然相邻。
- **`virtual: true`** 是文本检索端点的排除标记（见 §5.2）。
- **`revoked` 写入时同步父值**；父消息撤回后子文档同步打 revoked=true（见 §6 一致性）。

---

## 4. mapping 改动（indexer）

最小增量（顶层加 3 个字段，`payload.image` / `payload.file` 复用现有 mapping）：

```json
"parentMessageId":   { "type": "long" },
"parentPayloadType": { "type": "integer" },
"virtual":           { "type": "boolean" }
```

不需要新增 `payload.image.*` / `payload.file.*` 字段——子文档直接用现成的图片/文件 mapping。

mapping 升版本号到 **v1.10**，`octo-server-config/.../mapping/README.md` 同步记录"v1.10 加
parentMessageId/parentPayloadType/virtual 三字段，为富文本虚拟子文档配套"。

---

## 5. octo-server reader 必要改动

虽然口径是"几乎零改"，但**为防止子文档污染文本检索**和**保证可见性正确**，有 3 处必须改：

### 5.1 `Doc` 补字段（`source.go`，仅 reader 内部使用）

```go
type Doc struct {
    // ... 既有字段
    ParentMessageID *int64 `json:"parentMessageId,omitempty"` // 仅 reader 内部用：可见性 coalesce
    Virtual         bool   `json:"virtual,omitempty"`         // 仅 reader 内部用：文本检索 must_not 标记
}
```

**对外响应字段完全不变**——`MediaHit` / `FileHit` 不新增 `parent_message_id`。
indexer 契约保证子文档 `messageId` 字段 = 父富文本 messageId（见 §3 / indexer-dev §2），
reader 现有 `singleMediaHit` / `singleFileHit` 透传 `doc.messageId` 到响应的
`message_id` 即得到父值——前端拿这个 `message_id` 走"跳到原消息"接口即直达父富文本，
**前端零改**。

> ⚠️ **关键契约依赖**（由 indexer 单方面保证、reader 不重复实现）：子文档 OS doc
> 的 `messageId` 字段必须 = 父富文本 messageId（long），**不是** ES `_id` 的复合键
> 字符串。indexer owner 改契约前必须同步 reader——若哪天把 `messageId` 字段塞成
> 子文档复合键(`<父>-rt<i>` 字符串)，reader 投影后前端拿 `message_id` 走"跳到原
> 消息"接口会因类型不对或父消息不存在而炸。

### 5.2 文本检索端点排除虚拟子文档（`dsl.go` 加 helper + 3 个 builder 调用）

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

调用点：
- `buildSearchMessagesDSL`（search_messages.go:159 附近，紧挨 `applySystemMessageHardFilter`）
- `buildAnchorDSL`（search_around.go:241）
- `buildAroundDSL`（search_around.go:255）
- `buildSearchAllDSL`（search_all.go：白名单已经把 type=14 之外限制掉，但白名单加 14 后
  Part A 会把富文本父 doc 召回——子文档 `payload.type=2/5/8` 也在白名单内会被召回，必须
  加 `applyExcludeVirtual`）

> ⚠️ Part A 把 14 加入 `_search_all` 白名单后，**`_search_all` 也必须调 applyExcludeVirtual`**，
> 否则虚拟子文档会以 `payload.type=2/8` 身份混进 `_search_all` 结果。Part A 文档需要
> 同步补这条注意事项。

### 5.3 可见性按父 id 判定（`visibility.go` / `filterVisible`）

现状：`filterVisible` 把每条 doc 的 `messageId` 批量喂给 `messageService.GetMessages(...)` 查
MySQL 撤回/删除/接收方可见性。

改法：可见性键改成 `coalesce(parentMessageId, messageId)`——对 `virtual=true` 的 doc，用
`parentMessageId` 进 MySQL 可见性查询；普通 doc 走原路径。

实现细节：
- `messageVisibilityProbe` 在分组前先把每条 doc 映射成"可见性 key"（一个 int64）；
- 一次 MySQL 查询拿父消息可见性结果；
- 同一父下的多个子文档共享同一可见性判定结果。

这样保证：**撤回父富文本 → 所有派生媒体子文档同步消失**；**接收方对父不可见 → 子也不可见**。

---

## 6. indexer 侧实现要点

### 6.1 投影分支（`buildraw.go`）

当前 `case payloadTypeRichText` 只调 `buildRichText(raw)` 抽 searchText。新增并行抽取：

```go
case payloadTypeRichText:
    parsed.RichText = buildRichText(raw)
    // 新增：返回派生子文档列表，由上游写入循环 emit
    derivatives = appendRichTextDerivatives(derivatives, raw, parent)
```

`appendRichTextDerivatives` 遍历 `raw["content"]` 的 image/file block，每块产出一个
`Doc{...}` 实例，字段按 §3 契约填。

### 6.2 写入路径（consumer / backfill 双写）

- **consumer**：处理每条 Kafka 消息时，写父 doc + N 个子 doc。建议用 ES bulk 一次提交，
  保证原子性（要么全部入，要么全部失败）；失败按现有 batch-level retry / DLQ 走。
- **backfill runner**：同样的 `appendRichTextDerivatives` 路径，重跑历史 type=14 消息时
  同步生成子文档。

`_id` 复合键 `"<parentMessageId>-rt<i>"` 保证 idempotent：重跑 backfill 不会重复创建。

### 6.3 撤回 / edited 联动

- **撤回事件**：父 messageId 进入撤回流（现有路径）→ indexer 一并把所有
  `parentMessageId=<父>` 的子文档也 update revoked=true（或 delete by query）。
- **编辑（edited=true）**：现状 octo-server reader 是否对 edited 富文本重新派发子文档，
  取决于 indexer 是否在 edited 流上重跑 `appendRichTextDerivatives`。**建议**：edit 事件
  到来时，先按 `parentMessageId=<父>` delete by query 清掉旧子文档，再按新 blocks 重建。
  否则编辑后内嵌图变化时会有残留/错配。

### 6.4 一次性 reindex

PR 落地后跑一次全量 backfill（对 type=14 doc 限定 scope），把存量富文本派生子文档。
存储成本：当前 129 条 type=14 doc × 平均嵌入数 ≈ 几百条新 doc，可忽略。

---

## 7. 落地步骤

1. **indexer PR**：
   - mapping 加 3 字段（v1.10）
   - `buildraw.go` 加 `appendRichTextDerivatives` + 双写
   - consumer / backfill 测试覆盖：父 doc + N 子 doc 原子写入、idempotent、撤回联动
   - e2e（harness/gaptable 扩展）：seed 一条 type=14 含 2 图 1 文件 → OS 里 1 父 + 3 子
   - merge 后跑 backfill 一次性 reindex 存量

2. **octo-server PR**（indexer 落地后）：
   - `source.go` 加 `Doc.ParentMessageID` / `Doc.Virtual`（仅 reader 内部用：可见性
     coalesce + 文本检索 must_not；**不**对外暴露成响应字段）
   - `dsl.go` 加 `applyExcludeVirtual` helper
   - 3 个文本检索 builder + `buildSearchAllDSL` 调用 helper
   - `filterVisible` 用 `coalesce(parentMessageId, messageId)` 做 MySQL 可见性查询
   - 测试：
     - DSL pin：`_search` / `_search_all` / `_search_around` 都带 `must_not(virtual=true)`
     - `_search_media` / `_search_files` 不带 `must_not(virtual=true)`（pin 验证）
     - `filterVisible`：构造 virtual 子文档，撤回父 → 子被过滤
     - `buildMediaHits` / `buildFileHits`：virtual doc 投影出的响应 `message_id` = 父值
       （透传子文档 `messageId` 字段，依赖 indexer 契约——无需 reader 代码改动）

3. **文档**：本文件 + `docs/messages-search/` 下补一篇"virtual 子文档可见性父 id 映射"短文，
   便于审计与后续回滚。

4. **测试环境验证（集群 A）**：
   - indexer rollout 后等 backfill 完成
   - 前端走 `dmworkim-develop` 触发 `_search_media` / `_search_files` 查富文本里的"合同
     图片"/"xx.pdf"附件，应该能直接召回
   - `_search` / `_search_all` 命中富文本父消息（Part A 行为），不会再额外冒出子文档
     污染列表

---

## 8. 影响矩阵

| 端点 | B2 改动 |
|---|---|
| `/_search` | DSL 加 `MustNot(virtual=true)`（applyExcludeVirtual helper 一行） |
| `/_search_all` | 同上；Part A 把 14 加白名单后**必须**加该 helper（否则子文档以 type=2/8 身份污染列表） |
| `/_search_around` | 同上（anchor + window 两处都调） |
| `/_search_media` | **响应字段完全不变**：白名单 type∈{2,5} 已能命中虚拟子文档；响应 `message_id` 由现有逻辑透传子文档 `messageId` 字段（=父值，indexer 契约保证），**前端零改** |
| `/_search_files` | **响应字段完全不变**：白名单 type=8 已能命中虚拟子文档；响应 `message_id` 由现有逻辑透传子文档 `messageId` 字段（=父值，indexer 契约保证），**前端零改** |
| `filterVisible` | 可见性键改 `coalesce(parentMessageId, messageId)` |
| 游标 / 分页 / 聚合 | **不动** |

---

## 9. 一致性 / 回滚

### 一致性保证
- **父子原子写入**：indexer ES bulk 一次提交，写入失败整批 retry / DLQ。
- **撤回联动**：父 revoked → indexer 同步 update 所有 `parentMessageId=<父>` 子文档。
- **编辑联动**：编辑事件 → delete by query 清旧子文档 + 重建。
- **可见性单源**：reader 用 `parentMessageId` 进 MySQL，撤回/删除/接收方可见性自动共享。

### 回滚
若上线后发现问题：

1. octo-server reader 端 **`applyExcludeVirtual` helper 留下**——即使 indexer 暂时回滚不写
   virtual 子文档，reader 端有 helper 也只是 must_not 一个不存在的字段，no-op、无副作用。
2. indexer 侧：删 mapping 字段（mapping 不可删，需要 reindex 到新索引）→ 或者保留 mapping 但
   关掉双写、跑 `delete by query virtual=true` 清掉所有子文档。
3. 回滚后 `_search_media` / `_search_files` 行为退回到"搜不到富文本内嵌媒体"——这是上线
   前的状态，不会更糟。

回滚成本：一次性 reindex 或 delete by query，10 分钟内完成（集群 A 现有 type=14 仅 129 条）。

---

## 10. 与 Part A 的衔接

- Part A 已经把 `payload.type=14` 加入 `_search_all` 白名单 + multi_match 加
  `payload.richText.searchText`。
- **Part A 文档需要补一条注意事项**：`_search_all` 在 Part A 加 14 之后，**必须**调
  `applyExcludeVirtual`（哪怕本 Part B 还没上），否则 Part B 一旦上线，立刻会有虚拟子文档
  以 `payload.type∈{2,8}` 身份混进 `_search_all` 结果——而 `_search_all` 的语义是"按消息粒度
  浏览"，不应该把子文档当作独立消息列出来。
- 推荐做法：Part A 的 PR 里就引入 `applyExcludeVirtual` helper 并在 `_search` / `_search_all` /
  `_search_around` 调用——即使 indexer 暂时不写 virtual 字段，helper 是 no-op、不影响现有
  召回；Part B 落地时无需再动这些位置。

---

## 11. 待 indexer owner 拍板的点

1. **`_id` 复合键格式**：`<parent>-rt<i>` 是建议值；是否与 indexer 现有 `_id` 命名规范冲突
   需 owner 确认。
2. **edited 事件处理**：是否走 delete-by-query + 重建路线，还是其它方案（如保留旧子文档
   只 update）。
3. **revoked 联动批量大小 / 一致性窗口**：单父消息子文档数 ≤ 几十，可在同 batch 内同步；
   超大富文本（极端情况几百块）需评估 bulk 上限。
4. **`virtual` 字段命名**：是否与 indexer 既有契约冲突，或更习惯叫 `derived` / `synthetic`。
   命名锁定后 octo-server 端 helper 字段名同步。
