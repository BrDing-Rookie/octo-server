package message

import "github.com/Mininglamp-OSS/octo-lib/config"

// maxConversationVersion 返回 raw conversations 中的最大 Version，作为 sync 游标推进基准。
//
// 必须基于过滤前的列表计算：服务端会把 archived 子区 / 已删除子区 / 当前用户已退群的会话
// 从响应中剔除（见 syncUserConversation 的过滤循环）。若用过滤后的列表算 cursor，那
// 本批 raw conversations 里最高 version 的会话恰好被过滤掉时，下一次 sync 仍会拉到
// 同一批 raw conversations，客户端陷入循环。
//
// 若所有 raw version 都不大于 baseVersion（或列表为空），返回 baseVersion。
func maxConversationVersion(conversations []*config.SyncUserConversationResp, baseVersion int64) int64 {
	maxVersion := baseVersion
	for _, c := range conversations {
		if c == nil {
			continue
		}
		if c.Version > maxVersion {
			maxVersion = c.Version
		}
	}
	return maxVersion
}
