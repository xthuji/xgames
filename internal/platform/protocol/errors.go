package protocol

// 错误码（平台级；游戏私有错误码由各游戏定义，见 games/*/msg）
const (
	ErrCodeUnknown      = 1000
	ErrCodeInvalidMsg   = 1001
	ErrCodeRateLimit    = 1002 // 速率限制
	ErrCodeRoomNotFound = 2001
	ErrCodeRoomFull     = 2002
	ErrCodeNotInRoom    = 2003
	ErrCodeGameStarted  = 2004 // 游戏已开始

	ErrCodeNotRoomCreator = 2005 // 仅房间创建人可添加机器人
	ErrCodeRoomExists     = 2006 // 服务端仅支持单房间运行，已有房间存在
	ErrCodeGameNotStart   = 3001
	ErrCodeServerMaintenance = 5003 // 服务器维护中
)

// ErrorMessages 错误码对应的消息（游戏私有错误码经 games.RegisterErrorMessages 并入）
var ErrorMessages = map[int]string{
	ErrCodeUnknown:      "未知错误",
	ErrCodeInvalidMsg:   "无效的消息格式",
	ErrCodeRateLimit:    "请求过于频繁",
	ErrCodeRoomNotFound: "房间不存在",
	ErrCodeRoomFull:     "房间已满",
	ErrCodeNotInRoom:    "您不在房间中",
	ErrCodeGameStarted:  "游戏已开始",

	ErrCodeNotRoomCreator:    "仅房间创建人可添加机器人",
	ErrCodeRoomExists:        "服务端仅支持单房间运行，请等待当前对局结束后再创建",
	ErrCodeGameNotStart:      "游戏尚未开始",
	ErrCodeServerMaintenance: "服务器维护中",
}
