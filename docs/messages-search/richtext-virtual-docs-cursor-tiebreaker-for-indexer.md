# 给 indexer 的请求：富文本虚拟子文档需要一个唯一排序字段 `subSeq`

> 面向：octo-search-indexer 开发者。
> 关联：Part B / B2 虚拟媒体子文档方案。
> 一句话：请给虚拟子文档多写一个数值字段 `subSeq`,让"同一父富文本派生的多个子文档"在排序上能被区分,否则 `_search_media`/`_search_files` 翻页会**漏图**。

---

## 1. 变更原因（为什么需要）

B2 方案里,一条富文本(type=14)的每个内嵌图片/文件被产出成独立子文档,并且按契约
**子文档的 `messageId`=父值、`timestamp`=父值**。

reader 的搜索排序键是 `[timestamp, messageId]`,翻页靠 OpenSearch 的 `search_after`(排他)。
问题:**同一父富文本的 N 个子文档,`(timestamp, messageId)` 完全相同** → 排序 tuple 不唯一。
一旦这些兄弟子文档**骑在分页边界**上,下一页 `search_after=(timestamp, messageId)` 会把
**剩下同 tuple 的兄弟全部跳过、静默丢失**。

后果:`_search_media`/`_search_files` 里,一条多图富文本的部分图片在翻页后**搜不到**。
注意这不是只有"图数 > page_size"才触发——哪怕一条 3 图富文本,只要那几张正好跨页就会漏。

> 这一条**作废**了之前 Part B 文档里"游标/分页不动"的说法。

---

## 2. 期望方式（请 indexer 怎么改）

给每个 doc 增加一个**数值唯一区分字段** `subSeq`,作为排序的第三 tiebreaker:

| doc 类型 | `subSeq` 取值 |
|---|---|
| 普通消息 doc | `0`(messageId 本身唯一,无需区分) |
| 富文本父 doc（type=14） | `0`(父消息本身也是个 doc,与子文档区分) |
| 富文本虚拟子文档 | 该 block 在父消息 content 里的序号 `1,2,3,...`(**从 1 起**,按 block 顺序) |

- **mapping**：加 `"subSeq": { "type": "integer" }`(数值默认带 doc_values,可排序)。
- **写入**：虚拟子文档按 block 序写 `1..N`(从 1 起);普通 doc / 富文本父 doc 写 `0`。
- 目标性质:`(messageId, subSeq)` **全局唯一** → `(timestamp, messageId, subSeq)` 唯一,
  search_after 不再丢兄弟。

> reader 侧已落地（2026-06-24）：`Doc.SubSeq` 用 Go `int` 默认 0，存量 doc 在 OS 缺该字段
> 反序列化为 0，等同"普通 doc / 父 doc"约定。这意味着 **indexer 可以分两步上线**：
> 先 mapping + reader 全上线（subSeq=0 全场，排序行为零变化）→ 再 indexer 投影双写。
> 上线期间 reader 不会爆，pre-Part-B 的 cursor 也能容错降级（解出 subSeq=0,
> search_after 末位 0 → 同 (ts,msgID) 的所有 subSeq>=1 子文档下一页全部纳入,正确语义）。

> 命名 `subSeq` 是建议值,若与现有契约/习惯冲突可换(如 `derivativeSeq`/`blockSeq`),
> 锁定后告诉 reader 侧同步。

---

## 3. 游标变更（reader 侧会做什么,indexer 只需出字段）

indexer 出了 `subSeq` 之后,**reader 侧**负责:
- 排序键由 `[timestamp, messageId]` 改为 `[timestamp, messageId, subSeq]`(5 个端点统一);
- 把 `subSeq` 编进分页 cursor(cursor 线格式升级,reader 做容错解码,旧 cursor 平滑降级)。

**indexer 不碰 cursor**,只要保证 `subSeq` 写对(同父兄弟各不相同、普通 doc=0)即可。

---

## 4. 对 indexer 的净增量

- mapping 加 1 个字段 `subSeq`(integer)。
- 投影虚拟子文档时多写一个序号(其实就是遍历 content blocks 的 index)。
- 一次性 reindex 时同步写入(和 B2 本来就要做的 reindex 合并,无额外 reindex)。
- 其余 B2 契约(messageId=父值 / timestamp=父值 / virtual / parentMessageId)**全部不变**。

---

## 5. 待确认

1. 字段名 `subSeq` 是否 OK(vs `derivativeSeq`/`blockSeq`)。
2. ~~普通 doc 是写 `0` 还是省略~~ → **已拍板（2026-06-24）**：普通 doc / 父富文本 doc 写 `0`,
   虚拟子文档 `1` 起递增。reader 侧 `Doc.SubSeq` Go `int` 缺省=0,与该约定自然兼容,
   indexer 显式写 0 vs 省略对 reader 等价(但显式写 0 更清晰、便于日志/审计)。
3. 是否同意把它纳入 v1.10 mapping 一起上(和虚拟子文档同批)。
