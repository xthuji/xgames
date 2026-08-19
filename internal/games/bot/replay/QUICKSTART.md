# 决策解释与复盘系统 - 快速开始指南

## 5分钟快速集成

### 步骤1：创建适配器实例

在你的游戏Controller中：

```go
import "xgames/internal/games/{game}/bot"

// 在游戏开始时创建
adapter := bot.New{Game}ReplayAdapter(gameID)
```

### 步骤2：记录决策

在AI决策函数中：

```go
func DecideMove(...) Move {
    // ... 原有决策逻辑 ...
    
    // 记录决策
    adapter.RecordDecision(
        round,           // 当前轮次
        playerName,      // 机器人名称
        // ... 游戏特定参数 ...
    )
    
    return bestMove
}
```

### 步骤3：生成报告

在对局结束时：

```go
func OnGameOver(winner string, players []string) {
    report := adapter.GenerateReport(winner, players)
    
    // 输出到日志
    log.Info(replay.FormatReplayReport(report))
    
    // 或发送到前端
    sendToClient(report)
}
```

---

## 各游戏快速示例

### 斗地主

```go
// Controller中
type Controller struct {
    replayAdapter *bot.DDZReplayAdapter
}

func (c *Controller) OnGameStart(gameID string) {
    c.replayAdapter = bot.NewDDZReplayAdapter(gameID)
}

func (c *Controller) OnBotTurn(ctx bot.GameContext) []card.Card {
    chosen := engine.DecidePlay(ctx)
    
    c.replayAdapter.RecordDecision(
        c.round,
        c.botName,
        ctx.IsLandlord,
        ctx.Hand,
        ctx,
        candidates,  // 从引擎获取
        chosen,
        "",          // 推理说明（可选）
    )
    
    return chosen
}

func (c *Controller) OnGameOver(winner string) {
    report := c.replayAdapter.GenerateReport(winner, c.players)
    fmt.Println(replay.FormatReplayReport(report))
}
```

### 象棋

```go
func (c *Controller) OnBotTurn(board [9][10]int) ChessMove {
    move := engine.DecideMove(board, c.color)
    
    c.replayAdapter.RecordDecision(
        c.moveCount,
        c.botName,
        c.isRed,
        board,
        bot.ChessMoveInfo{
            FromRow: move.FromRow,
            FromCol: move.FromCol,
            ToRow:   move.ToRow,
            ToCol:   move.ToCol,
            Piece:   board[move.FromRow][move.FromCol],
            Captured: board[move.ToRow][move.ToCol],
            IsCheck: isInCheck(board, oppColor),
            EvalScore: move.Score,
        },
        candidates,
        "",
        move.Score,
        searchDepth,
    )
    
    return move
}
```

### 五子棋

```go
func (c *Controller) OnBotTurn(board [15][15]int) GomokuMove {
    move := engine.DecideMove(board, c.color)
    
    c.replayAdapter.RecordDecision(
        c.moveCount,
        c.botName,
        c.isBlack,
        board,
        bot.GomokuMoveInfo{
            Row:           move.Row,
            Col:           move.Col,
            Threats:       detectThreats(board, move),
            EvalScore:     move.Score,
            IsWinningMove: isWinningMove(board, move),
        },
        candidates,
        "",
        move.Score,
        searchDepth,
        vcfFound,
        vctFound,
    )
    
    return move
}
```

### 麻将

```go
func (c *Controller) OnBotTurn(ctx bot.DiscardContext) int {
    tile := engine.DecideDiscard(ctx)
    
    c.replayAdapter.RecordDecision(
        ctx.TurnNumber,
        c.botName,
        ctx.Hand,
        ctx.Melds,
        bot.MahjongMoveInfo{
            Tile:         tile,
            ActionType:   "discard",
            ShantenAfter: rule.ShantenAfterDiscard(ctx.Hand, tile),
            Ukeire:       rule.UkeireAfterDiscard(ctx.Hand, tile, ctx.Seen),
            DangerLevel:  bot.TileDangerLevel(tile, ctx),
            IsTing:       rule.IsTenpaiWithMelds(ctx.Hand, len(ctx.Melds)),
            TingWaits:    rule.TenpaiWaits(ctx.Hand, len(ctx.Melds)),
        },
        candidates,
        "",
        ctx.OpponentModels,
    )
    
    return tile
}
```

---

## 常见场景

### 场景1：仅记录关键决策

```go
func shouldRecord(log DecisionLog) bool {
    // 只记录强度差异大的决策
    return log.StrengthDiff < -2.0 || log.StrengthDiff > 2.0
}

if shouldRecord(log) {
    adapter.GetLogger().Record(log)
}
```

### 场景2：自定义报告格式

```go
report := adapter.GenerateReport(winner, players)

// 自定义处理
customOutput := formatMyWay(report)
sendToDatabase(customOutput)
```

### 场景3：性能优化（生产环境）

```go
// 仅在调试模式启用完整日志
if config.DebugMode {
    adapter.RecordDecision(...)
} else {
    // 生产环境仅记录关键决策
    if isKeyDecision {
        adapter.RecordDecision(...)
    }
}
```

---

## API速查

### DecisionLogger

```go
logger := replay.NewDecisionLogger(gameType, gameID)

// 记录决策
logger.Record(log DecisionLog)

// 获取所有日志
logs := logger.GetLogs()

// 获取关键决策点
keyMoments := logger.GetKeyMoments()

// 生成报告
report := logger.GenerateReplayReport(winner, players)
```

### 格式化函数

```go
// 格式化单条决策日志
text := replay.FormatDecisionLog(&log)

// 格式化完整报告
text := replay.FormatReplayReport(&report)
```

### 核心类型

```go
// 决策日志
type DecisionLog struct {
    Timestamp    int64
    Round        int
    PlayerName   string
    Context      GameContextInfo
    Candidates   []CandidateInfo
    Chosen       MoveInfo
    Reasoning    string
    StrengthDiff float64
    EvalScore    float64
}

// 复盘报告
type ReplayReport struct {
    GameID              string
    GameType            string
    Duration            time.Duration
    Winner              string
    Players             []string
    KeyMoments          []DecisionLog
    MissedOpportunities []string
    PlayerStyles        map[string]string
    Suggestions         []string
    Statistics          GameStatistics
}
```

---

## 故障排查

### 问题1：编译错误 "undefined: replay"

**解决**：确保导入正确的包
```go
import "xgames/internal/games/bot/replay"
```

### 问题2：日志为空

**检查**：
- 是否调用了`RecordDecision()`？
- `gameID`是否正确？
- 是否在正确的时机调用`GenerateReport()`？

### 问题3：报告格式混乱

**检查**：
- 使用`replay.FormatReplayReport()`而非直接打印结构体
- 确保所有字段都已正确填充

---

## 更多信息

- 详细文档：`internal/games/bot/replay/README.md`
- 使用示例：`internal/games/bot/replay/example_test.go`
- 技术总结：`docs/bots/replay-system.md`
- 实施报告：`docs/bots/replay-implementation-summary.md`

---

**需要帮助？** 查看上述文档或在代码中搜索具体游戏的适配器实现作为参考。
