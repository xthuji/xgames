// Package msg 斗地主协议：游戏私有消息类型常量、payload 结构与牌转换。
//
// 平台信封（protocol.Message / NewMessage / ParsePayload）与平台级消息
// （房间/匹配/用户/排行）仍在 platform/protocol；这里只放斗地主专属内容。
package msg

import (
	"xgames/internal/games/ddz/card"
	"xgames/internal/platform/protocol"
)

// 客户端 → 服务端 消息类型
const (
	MsgBid       protocol.MessageType = "bid"        // 叫分（1/2/3 分）
	MsgDouble    protocol.MessageType = "double"     // 加倍
	MsgPlayCards protocol.MessageType = "play_cards" // 出牌
	MsgPass      protocol.MessageType = "pass"       // 不出
	MsgAfk       protocol.MessageType = "afk"        // 挂机/取消挂机（托管开关）
)

// 服务端 → 客户端 消息类型
const (
	MsgDealCards  protocol.MessageType = "deal_cards"  // 发牌
	MsgBidTurn    protocol.MessageType = "bid_turn"    // 轮到叫分 / 加倍
	MsgBidResult  protocol.MessageType = "bid_result"  // 叫分 / 加倍结果
	MsgLandlord   protocol.MessageType = "landlord"    // 地主确定
	MsgPlayTurn   protocol.MessageType = "play_turn"   // 轮到出牌
	MsgAfkChanged protocol.MessageType = "afk_changed" // 挂机状态变更通知
	MsgCardPlayed protocol.MessageType = "card_played" // 有人出牌
	MsgPlayerPass protocol.MessageType = "player_pass" // 有人不出
)

// 斗地主专属错误码
const (
	ErrCodeNotYourTurn  = 3002
	ErrCodeInvalidCards = 3003
	ErrCodeCannotBeat   = 3004
	ErrCodeMustPlay     = 3005
)

// ErrorMessages 斗地主错误码对应的默认文案（注册时并入平台文案表）
var ErrorMessages = map[int]string{
	ErrCodeNotYourTurn:  "还没轮到您",
	ErrCodeInvalidCards: "无效的牌型",
	ErrCodeCannotBeat:   "您的牌大不过上家",
	ErrCodeMustPlay:     "您必须出牌",
}

// --- 客户端请求 Payloads ---

// BidPayload 叫分请求
type BidPayload struct {
	Score int `json:"score"` // 0 = 不叫, 1/2/3 = 叫分（须大于当前最高分）
}

// DoublePayload 加倍请求（地主确定后的加倍阶段）
type DoublePayload struct {
	Double bool `json:"double"` // true = 加倍, false = 不加倍
}

// AfkPayload 挂机请求（托管开关：挂机后由机器人策略自动决策）
type AfkPayload struct {
	Afk bool `json:"afk"` // true = 挂机, false = 取消挂机
}

// PlayCardsPayload 出牌请求
type PlayCardsPayload struct {
	Cards []protocol.CardInfo `json:"cards"`
}

// --- 服务端响应 Payloads ---

// DealCardsPayload 发牌通知
type DealCardsPayload struct {
	Cards       []protocol.CardInfo `json:"cards"`        // 玩家自己的手牌
	BottomCards []protocol.CardInfo `json:"bottom_cards"` // 底牌（地主确定后才显示具体内容）
}

// BidTurnPayload 轮到叫分 / 加倍通知
type BidTurnPayload struct {
	PlayerID   string `json:"player_id"`
	Timeout    int    `json:"timeout"`    // 超时时间（秒）
	Phase      string `json:"phase"`      // call=叫分阶段, double=加倍阶段
	HighBid    int    `json:"high_bid"`   // 叫分阶段当前最高分（0=尚无人叫）
	Multiplier int    `json:"multiplier"` // 当前倍数
}

// BidResultPayload 叫分 / 加倍结果通知
type BidResultPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Phase      string `json:"phase"`      // call=叫分阶段, double=加倍阶段
	Score      int    `json:"score"`      // 叫分阶段所叫分数（0=不叫）
	Double     bool   `json:"double"`     // 加倍阶段是否加倍
	Multiplier int    `json:"multiplier"` // 决策后的当前倍数
}

// LandlordPayload 地主确定通知
type LandlordPayload struct {
	PlayerID    string              `json:"player_id"`
	PlayerName  string              `json:"player_name"`
	BottomCards []protocol.CardInfo `json:"bottom_cards"` // 底牌
	Multiplier  int                 `json:"multiplier"`   // 底倍（叫分确定的分数）
}

// PlayTurnPayload 轮到出牌通知
type PlayTurnPayload struct {
	PlayerID string `json:"player_id"`
	Timeout  int    `json:"timeout"`   // 超时时间（秒）
	MustPlay bool   `json:"must_play"` // 是否必须出牌（新一轮开始时为 true）
	CanBeat  bool   `json:"can_beat"`  // 是否有牌能打过上家
}

// CardPlayedPayload 出牌通知
type CardPlayedPayload struct {
	PlayerID   string              `json:"player_id"`
	PlayerName string              `json:"player_name"`
	Cards      []protocol.CardInfo `json:"cards"`
	CardsLeft  int                 `json:"cards_left"` // 剩余手牌数
	HandType   string              `json:"hand_type"`  // 牌型名称
}

// PlayerPassPayload 不出通知
type PlayerPassPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
}

// AfkChangedPayload 挂机状态变更通知（广播给全房间）
type AfkChangedPayload struct {
	PlayerID string `json:"player_id"`
	Afk      bool   `json:"afk"`
}

// GameStateDTO 游戏状态数据传输对象（重连恢复用）
type GameStateDTO struct {
	Phase        string                `json:"phase"`          // bidding/doubling/playing
	Players      []protocol.PlayerInfo `json:"players"`        // 所有玩家信息
	Hand         []protocol.CardInfo   `json:"hand"`           // 自己的手牌
	BottomCards  []protocol.CardInfo   `json:"bottom_cards"`   // 底牌
	CurrentTurn  string                `json:"current_turn"`   // 当前回合玩家 ID
	LastPlayed   []protocol.CardInfo   `json:"last_played"`    // 上家出的牌
	LastPlayerID string                `json:"last_player_id"` // 上家 ID
	MustPlay     bool                  `json:"must_play"`      // 是否必须出牌
	CanBeat      bool                  `json:"can_beat"`       // 是否能打过
	Multiplier   int                   `json:"multiplier"`     // 当前倍数
}

// GameOverExtra 斗地主 game_over 扩展数据（放入平台 GameOverPayload.Extra）
type GameOverExtra struct {
	PlayerHands []PlayerHand `json:"player_hands"` // 所有玩家剩余手牌
	Multiplier  int          `json:"multiplier"`   // 最终倍数
}

// PlayerHand 玩家手牌信息（用于游戏结束展示）
type PlayerHand struct {
	PlayerID   string              `json:"player_id"`
	PlayerName string              `json:"player_name"`
	Cards      []protocol.CardInfo `json:"cards"`
}

// --- 牌转换（领域牌 ↔ 协议牌） ---

// CardToInfo 领域牌 → 协议牌
func CardToInfo(c card.Card) protocol.CardInfo {
	return protocol.CardInfo{Suit: int(c.Suit), Rank: int(c.Rank), Color: int(c.Color)}
}

// CardsToInfos 领域牌组 → 协议牌组
func CardsToInfos(cards []card.Card) []protocol.CardInfo {
	infos := make([]protocol.CardInfo, len(cards))
	for i, c := range cards {
		infos[i] = CardToInfo(c)
	}
	return infos
}

// InfoToCard 协议牌 → 领域牌
func InfoToCard(info protocol.CardInfo) card.Card {
	return card.Card{Suit: card.Suit(info.Suit), Rank: card.Rank(info.Rank), Color: card.CardColor(info.Color)}
}

// InfosToCards 协议牌组 → 领域牌组
func InfosToCards(infos []protocol.CardInfo) []card.Card {
	cards := make([]card.Card, len(infos))
	for i, info := range infos {
		cards[i] = InfoToCard(info)
	}
	return cards
}
