package messages_search

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"go.uber.org/zap"
)

// checkChannelAccess enforces the channel-membership gate shared by all four
// /_search* endpoints: a caller may only search conversations they can
// already read.
//
//   - p2p (1)    — always allowed: the OS channelId is the fakeChannelID
//     derived from (loginUID, peerUID), so the caller can only ever reach
//     their own conversations regardless of the channel_id they send.
//   - group (2)  — loginUID must be an *active* member of the group.
//     ExistMemberActive (is_deleted=0 AND status=Normal) is the fail-closed
//     whitelist variant, so blacklisted/removed members lose search too.
//   - thread (5) — membership is checked against the parent group parsed
//     from the `{group_no}____{short_id}` channel_id. A channel_id that does
//     not parse is denied, NOT skipped — groupNoFromChannel's empty-string
//     fallback is for display joins only and must not be reused here.
//
// Non-members get NOT_FOUND with resource=channel (anti-enumeration: the
// response must not reveal whether the group exists). Lookup errors fail
// closed with INTERNAL_ERROR.
func (h *Handler) checkChannelAccess(c *wkhttp.Context, channelType uint8, channelID, loginUID string) bool {
	var groupNo string
	switch channelType {
	case channelTypeGroup:
		groupNo = channelID
	case channelTypeThread:
		parsed, _, err := thread.ParseChannelID(channelID)
		if err != nil {
			respondNotFound(c, "channel")
			return false
		}
		groupNo = parsed
	default:
		return true
	}

	ok, err := h.groupService.ExistMemberActive(groupNo, loginUID)
	if err != nil {
		h.Error("channel access check failed",
			zap.Error(err),
			zap.String("group_no", groupNo))
		respondInternal(c)
		return false
	}
	if !ok {
		respondNotFound(c, "channel")
		return false
	}
	return true
}
