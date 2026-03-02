package botfather

import (
	"errors"
	"net/http"

	"github.com/TangSengDaoDao/TangSengDaoDaoServerLib/pkg/wkhttp"
	"go.uber.org/zap"
)

// getGroups 获取机器人所在的群组列表
func (bf *BotFather) getGroups(c *wkhttp.Context) {
	botUID := c.GetString("bot_uid")
	if botUID == "" {
		c.ResponseError(errors.New("bot_uid not found"))
		return
	}

	type GroupInfo struct {
		GroupNo string `json:"group_no"`
		Name    string `json:"name"`
	}

	var groups []GroupInfo
	_, err := bf.ctx.DB().SelectBySql(
		"SELECT gm.group_no, g.name FROM group_member gm INNER JOIN `group` g ON gm.group_no = g.group_no WHERE gm.uid = ? AND gm.is_deleted = 0",
		botUID,
	).Load(&groups)
	if err != nil {
		bf.Error("查询机器人群组失败", zap.Error(err))
		c.ResponseError(errors.New("查询群组失败"))
		return
	}

	c.JSON(http.StatusOK, groups)
}
