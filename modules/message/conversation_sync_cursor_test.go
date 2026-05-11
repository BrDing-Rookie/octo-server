package message

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/stretchr/testify/assert"
)

// TestMaxConversationVersion PR-B：sync 游标必须基于 raw conversations 推进，
// 否则被服务端隐藏的尾部会话（archived 子区 / 无效群）会让 cursor 停留在前一个
// 较小的 version，下一次 sync 反复拉到同一批 raw conversations。
func TestMaxConversationVersion(t *testing.T) {
	cases := []struct {
		name string
		raw  []*config.SyncUserConversationResp
		base int64
		want int64
	}{
		{
			name: "empty falls back to base",
			raw:  nil,
			base: 42,
			want: 42,
		},
		{
			name: "max version greater than base wins",
			raw: []*config.SyncUserConversationResp{
				{Version: 10},
				{Version: 50},
				{Version: 30},
			},
			base: 5,
			want: 50,
		},
		{
			name: "base wins when all raw versions are smaller",
			raw: []*config.SyncUserConversationResp{
				{Version: 1},
				{Version: 2},
			},
			base: 100,
			want: 100,
		},
		{
			name: "skips nil entries",
			raw: []*config.SyncUserConversationResp{
				nil,
				{Version: 7},
				nil,
			},
			base: 0,
			want: 7,
		},
		{
			name: "tail-only filtered case: max comes from last entry even if caller would have discarded it",
			raw: []*config.SyncUserConversationResp{
				{Version: 11},
				{Version: 12},
				{Version: 99}, // 假设这是 archived/invalid → 会被业务层过滤掉，但 cursor 仍要推进
			},
			base: 10,
			want: 99,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maxConversationVersion(tc.raw, tc.base)
			assert.Equal(t, tc.want, got)
		})
	}
}
