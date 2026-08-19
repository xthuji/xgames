# 决策解释与复盘系统

统一的决策解释与复盘系统，适用于所有对战类游戏（斗地主、象棋、五子棋、麻将）。

## 架构设计

### 核心组件

```
internal/games/bot/replay/
├── types.go              # 通用类型定义和核心逻辑
└── README.md             # 本文档

internal/games/{game}/bot/
└── replay_adapter.go     # 游戏特定的适配器实现
```

### 设计理念

1. **统一抽象**：定义通用的数据结构（`DecisionLog`、`ReplayReport`等）
2. **适配器模式**：每个游戏实现自己的 Adapter，将游戏特定数据转换为通用格式
3. **可扩展性**：新增游戏只需实现 Adapter，无需修改核心逻辑
4. **格式化输出**：提供文本格式的决策日志和复盘报告

## 核心类型

### DecisionLog（决策日志）

记录单次决策的完整信息：

```go
type DecisionLog struct {
    Timestamp    int64               // 决策时间戳
    Round        int                 // 当前轮次/步数
    PlayerName   string              // 机器人名称
    Context      GameContextInfo     // 游戏上下文摘要
    Candidates   []CandidateInfo     // 所有候选着法
    Chosen       MoveInfo            // 最终选择
    Reasoning    string              // 自然语言解释
    StrengthDiff float64             // 选择 vs 次优的强度差
    EvalScore    float64             // 评估分数
    WinRate      float64             // 胜率预估
    Metadata     map[string]any      // 游戏特定的元数据
}
```

### ReplayReport（复盘报告）

对局结束后的完整分析报告：

```go
type ReplayReport struct {
    GameID              string             // 对局 ID
    GameType            string             // 游戏类型
    Duration            time.Duration      // 对局时长
    Winner              string             // 获胜者
    Players             []string           // 参与玩家
    KeyMoments          []DecisionLog      // 关键决策点
    MissedOpportunities []string           // 错过的更优选择
    PlayerStyles        map[string]string  // 玩家风格总结
    Suggestions         []string           // 改进建议
    Statistics          GameStatistics     // 对局统计
}
```

### DecisionLogger（日志收集器）

负责收集和管理决策日志：

```go
type DecisionLogger struct {
    logs     []DecisionLog
    gameType string
    gameID   string
}

// 主要方法
func (dl *DecisionLogger) Record(log DecisionLog)
func (dl *DecisionLogger) GetKeyMoments() []DecisionLog
func (dl *DecisionLogger) GenerateReplayReport(winner string, players []string) *ReplayReport
```

## 使用示例

### 斗地主

```go
import "xgames/internal/games/ddz/bot"

// 创建适配器
adapter := bot.NewDDZReplayAdapter("game-123")

// 在决策时记录
adapter.RecordDecision(
    round,           // 轮次
    playerName,      // 玩家名称
    isLandlord,      // 是否地主
    hand,            // 手牌
    ctx,             // 游戏上下文
    candidates,      // 候选着法
    chosen,          // 最终选择
    reasoning,       // 推理说明
)

// 对局结束后生成报告
report := adapter.GenerateReport(winner, players)
fmt.Println(replay.FormatReplayReport(report))
```

### 象棋

```go
import "xgames/internal/games/chess/bot"

adapter := bot.NewChessReplayAdapter("game-456")

// 记录落子决策
adapter.RecordDecision(
    round,
    playerName,
    isRed,
    board,
    move,            // ChessMoveInfo
    candidates,      // []ChessCandidate
    reasoning,
    evalScore,
    searchDepth,
)

report := adapter.GenerateReport(winner, players)
```

### 五子棋

```go
import "xgames/internal/games/gomoku/bot"

adapter := bot.NewGomokuReplayAdapter("game-789")

adapter.RecordDecision(
    round,
    playerName,
    isBlack,
    board,
    move,            // GomokuMoveInfo
    candidates,
    reasoning,
    evalScore,
    searchDepth,
    vcfFound,
    vctFound,
)

report := adapter.GenerateReport(winner, players)
```

### 麻将

```go
import "xgames/internal/games/mahjong/bot"

adapter := bot.NewMahjongReplayAdapter("game-012")

adapter.RecordDecision(
    round,
    playerName,
    hand,
    melds,
    move,            // MahjongMoveInfo
    candidates,
    reasoning,
    opponentModels,
)

report := adapter.GenerateReport(winner, players)
```

## 输出格式

### 决策日志示例

```
[第15步] 机器人A: 炮二平五吃马将军
原因:
  - 中局阶段，争夺主动权和攻势
  - 吃子着法，获得物质优势
  - 将军着法，施加压力
  - 靠近九宫，威胁对方将帅
评估分: 285.00
```

### 复盘报告示例

```
=== 中国象棋对局复盘报告 ===

对局 ID: game-456
时长: 15m32s
参与玩家: 机器人A, 玩家B
获胜者: 机器人A

--- 关键决策点 ---
[第15步] 机器人A: 炮二平五吃马将军
原因:
  - 中局阶段，争夺主动权和攻势
  - 吃子着法，获得物质优势
评估分: 285.00

--- 错过的机会 ---
1. 第8步: 有更优选择（评分 320.00 vs 250.00）

--- 玩家风格分析 ---
机器人A: 激进型（偏好进攻）
玩家B: 保守型（偏好防守）

--- 改进建议 ---
1. 玩家B 在第8步出现明显失误，可考虑更优选择
2. 玩家B 开局阶段应更注重布局
```

## 扩展新游戏

要为新游戏添加复盘支持，只需：

1. 创建 `replay_adapter.go` 文件
2. 实现游戏特定的 Adapter 结构体
3. 实现 `RecordDecision` 方法，将游戏数据转换为通用格式
4. 实现辅助函数（着法转换、评分计算、推理生成等）

参考现有游戏的实现作为模板。

## 性能考虑

- 日志记录是轻量级的，不会显著影响决策性能
- 建议在开发/测试环境启用完整日志，生产环境可选择性启用
- 复盘报告生成在对局结束后进行，不影响实时游戏体验

## 未来优化方向

1. **可视化界面**：前端展示决策树和关键节点
2. **数据分析**：基于历史对局的深度分析
3. **教学功能**：针对玩家的个性化建议
4. **云端存储**：保存和分享精彩对局
5. **AI 对比**：与最强 AI 的决策对比分析
