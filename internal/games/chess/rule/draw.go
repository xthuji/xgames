package rule

// 和棋判定：参照《象棋竞赛规则》与主流引擎（Pikafish/ElephantEye）通行做法。
//
// 三条判和规则（由服务端按优先级依次判定）：
//  1. 不变作和：同一局面（含执子方）重复出现 3 次；
//  2. 自然限着：连续 13 回合（26 ply）双方均未吃子；
//  3. 简单和棋局面：双方均无车/马/炮/兵卒（只剩帅仕相），任何一方都无法将死对方。
//
// 回合数常量仅约定口径（ply 计数），计数与判定由服务端 Session 维护。

// DrawReason 和棋原因（写入 GameOverPayload.DrawReason，前端可用于展示）
type DrawReason string

const (
	DrawRepetition   DrawReason = "repetition"            // 三次重复不变作和
	DrawNaturalLimit DrawReason = "natural_limit"         // 自然限着（连续未吃子）
	DrawNoMaterial   DrawReason = "insufficient_material" // 双方均无取胜可能
	DrawAgreement    DrawReason = "agreement"             // 双方协议和棋
)

// PositionHash 局面哈希（含执子方），用于重复局面计数。
// FNV-1a 遍历 90 格棋子与执子方；同一棋盘 + 同一执子方哈希一致，
// 红黑同形不同执子方哈希不同（与竞赛规则"重复局面"口径一致）。
func PositionHash(board []int, camp int) uint64 {
	const (
		fnvOffset uint64 = 14695981039346656037
		fnvPrime  uint64 = 1099511628211
	)
	h := fnvOffset
	for _, v := range board {
		h ^= uint64(v)
		h *= fnvPrime
	}
	h ^= uint64(camp) * 0x9E3779B97F4A7C15
	h *= fnvPrime
	return h
}

// InsufficientMaterial 双方均无取胜可能：双方都无车/马/炮/兵卒（只剩帅/将、仕/士、相/象）。
// 帅仕相无法过河进攻，任何一方都无法构成将死，判和。
func InsufficientMaterial(board []int) bool {
	for _, p := range board {
		if p == Empty {
			continue
		}
		switch TypeOf(p) {
		case 2, 3, 4, 7: // 车 / 马 / 炮 / 兵卒
			return false
		}
	}
	return true
}
