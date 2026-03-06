package user

// BotFriendApplyHook 机器人好友申请通知回调
// botfather 模块注册这个回调来接收通知，避免循环依赖
type BotFriendApplyHook func(applyUID, applyName, robotID, remark, token string)

var botFriendApplyHook BotFriendApplyHook

// RegisterBotFriendApplyHook 注册机器人好友申请通知回调
func RegisterBotFriendApplyHook(hook BotFriendApplyHook) {
	botFriendApplyHook = hook
}
