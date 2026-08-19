# 斗地主机器人完整技术方案

> **源码位置**：`internal/games/ddz/bot/`（核心决策）+ `internal/games/ddz/rule/`（牌型判定与候选生成）+ `internal/games/ddz/session/`（对局流程与托管）
> **设计理念**：普通人的上位水准 —— 打造值得反复对战的学习伙伴
> **关联文档**：[../bots.md](../bots.md)（机器人全景）、[mahjong-bot.md](mahjong-bot.md)、[gomoku-bot.md](gomoku-bot.md)、[chess-bot.md](chess-bot.md)

---

## 目录

1. [概述与定位](#1-概述与定位)
2. [设计纲领：胜利优先级阶梯](#2-设计纲领胜利优先级阶梯)
3. [架构总览](#3-架构总览)
4. [规则判定与核心数据结构](#4-规则判定与核心数据结构)
5. [决策流程详解](#5-决策流程详解)
6. [关键算法](#6-关键算法)
7. [性能优化](#7-性能优化)
8. [主流方案对比与定位](#8-主流方案对比与定位)
9. [测试与验证](#9-测试与验证)
10. [优化路线：当前能力与下一步](#10-优化路线当前能力与下一步)

---

## 1. 概述与定位

### 1.1 设计目标

**核心定位：普通人的上位水准**

机器人不是"不可战胜的 AI"，而是"值得反复对战的学习伙伴"。目标是让普通玩家：
- **有可玩性**：不会因碾压而沮丧，也不会因太弱而无趣
- **有思考空间**：决策逻辑透明可理解，玩家能从中学习策略
- **有提升路径**：通过观察机器人的出牌和复盘报告，玩家能改进自己的技术

**能力水平：**
- 超越 60-70% 的休闲玩家（中等偏上）
- 能被 20-30% 的资深玩家稳定击败（留有挑战空间）
- 偶尔打出精彩操作（展示高级技巧，供玩家学习）

**设计原则：**
1. **胜利优先**：所有启发式都只是"本阵营获胜"的近似，冲突时按 §2 优先级阶梯裁决
2. **可解释优先**：所有决策规则透明，玩家能理解"为什么这样出"
3. **适度失误**：不追求完美最优解，允许次优但合理的决策
4. **风格多样**：不同难度展现不同风格（保守/激进/均衡），增加趣味性
5. **冷启动即用**：无需训练数据，部署即可提供有意义的对战体验

### 1.2 技术路线

**纯 Go 规则启发式引擎 + 轻量级 UCT 残局搜索 + 人格化难度系统**

- **无模型、无训练、零外部依赖**：编译即部署，无需 GPU 或模型文件
- **三档难度**：easy/normal/hard，通过 BotPersonality 参数调节行为风格，全链路打通（前端选择 → 服务端转发 → 引擎参数）
- **记牌差异**：normal 仅跟踪 A/2/大小王，hard 全量跟踪 15 个点数；视图缺失的点数按"未知"保守处理
- **策略水平**：中等偏上（超越 60-70% 休闲玩家，能被资深玩家击败）
- **设计目标**：可调试、规则透明、决策耗时可控（<1s）、有趣味性

### 1.3 模块清单

| 模块 | 文件 | 行数 | 职责 |
|------|------|------|------|
| 类型定义 | `types.go` | 96 | DecisionEngine 接口、GameContext、PassInfo、PlayRecord、BotPersonality |
| 决策入口 | `engine.go` | 218 | DecidePlay 三级调度（P0 硬规则 → MCTS → 规则引擎）、决策日志生成 |
| 人格化参数 | `personality.go` | 87 | intentionalImperfection 故意犯错逻辑（含 P0 护栏） |
| Controller | `controller.go` | 505 | 消息驱动状态跟踪、GameContext 构造、拟人延迟、复盘适配器接入 |
| 统一策略 | `strategy.go` | 1039 | 领出/跟牌完整决策链、农民上家主攻模式及所有辅助函数 |
| 局势化 | `situation.go` | 224 | 掌权牌/跑牌/顶牌/谨慎/快走等局势判断 |
| 记牌器 | `tracker.go` | 116 | 计数数组、未知牌池、炸弹预警、绝对控制 |
| 手数计算 | `handcount.go` | 308 | DFS+记忆化 MinHandCount、回合缓存机制 |
| MCTS 残局 | `mcts.go` | 506 | UCT 树搜索、三席位阵营 rollout、并行模拟 |
| 叫分 | `bid.go` | 172 | 手牌强度评分（含结构因子）、叫分/加倍阈值 |
| 决策日志 | `replay.go` | 316 | DecisionLog/ReplayReport 通用类型与格式化输出 |
| 复盘适配器 | `replay_adapter.go` | 250 | DDZReplayAdapter 决策记录、复盘报告生成 |
| 离线复盘分析 | `user_analysis.go` | 194 | 真人出牌快照重建决策上下文，对比引擎最优解判定失误 |
| 牌型判定 | `rule/`（rule.go/beat.go/validator.go） | 596 | 16 种牌型解析、可压判定（含拆牌跟牌）、窗口比较 |
| 候选生成 | `rule/hints.go` | 673 | 全牌型合法候选生成、评分排序、带牌小牌化 |

---

## 2. 设计纲领：胜利优先级阶梯

机器人出牌的唯一顶层目标是**本阵营获胜**（地主：自己先走完；农民：己方任一人先走完）。所有启发式都只是该目标的近似，冲突时按下表优先级裁决：

| 优先级 | 目标 | 含义 | 硬约束 |
|--------|------|------|--------|
| **P0** | 一手清空 | 领出时整手牌恰为一个合法牌型；跟牌时存在能压过上家且恰好清空手牌的候选 | **硬规则**：最先检查（`immediateWinPlay`），优先于 MCTS、故意犯错、一切过滤，绝不替换 |
| **P1** | 阵营配合 | 农民：队友大牌不压、队友接近走完让牌送权、接应队友小牌、地主 ≤2 张时死封；**农民上家主攻模式**（手牌易走完时以自己走完为第一路径，见 §5.4）；地主：防两家农民跑牌 | 硬规则（策略层实现），MCTS 不得违背（主攻模式除外，见 §5.5） |
| **P2** | 节奏与控制 | 夺取并保持牌权：跟牌压制、拆牌重计划、残局 MCTS 搜索、夺权牌调度 | 可计算、可放宽 |
| **P3** | 结构优化 | 手数最小化、不拆对子/三条、节省 2/王掌权牌 | **只作 tie-breaker**，永不否决 P0~P2 |

阶梯的典型裁决示例：
- 对手一手顺子可走 5 张以上（P2 节奏价值）时，允许我方拆散至多 1 组对子/三条跟牌（P3 结构代价让位）
- 自己是地主上家且 3 步内可走完时，"自己走完"优先于"辅佐队友"（P1 内部的主攻/辅攻动态指派）
- 领出一手能走完的牌（如剩一对）时整手打出，不被故意犯错机制替换（P0 硬护栏）

---

## 3. 架构总览

### 3.1 整体架构图

```
┌─────────────────────────────────────────────────────────────┐
│                     Room / GameSession                       │
│  广播消息 (game_start / bid_turn / play_turn / card_played  │
│            / player_pass / game_over)                        │
│  定向消息 (deal_cards)                                       │
└────────────────┬───────────────────┬────────────────────────┘
                 │ OnBroadcast       │ OnTargeted
                 ▼                   ▼
┌─────────────────────────────────────────────────────────────┐
│                 Controller (controller.go)                   │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  gameTrack (per room, mutex-protected)              │    │
│  │  - hands / counts / recent[2] / actionSeq           │    │
│  │  - passes / cumPasses: 过牌推理                     │    │
│  │  - stuck / bombNum / passStreak / difficulty        │    │
│  │  - personality: BotPersonality                      │    │
│  │  - replayAdapter: *DDZReplayAdapter                 │    │
│  └─────────────────────────────────────────────────────┘    │
│  buildContextLocked() → GameContext + handCountCache        │
│  延迟 1~3s → Engine.DecidePlay → SubmitBotAction            │
│  onGameOver() → 异步生成 ReplayReport                       │
└────────────────┬────────────────────────────────────────────┘
                 │ GameContext
                 ▼
┌─────────────────────────────────────────────────────────────┐
│                    Engine (engine.go)                        │
│  DecidePlay 三级结构:                                        │
│    1. immediateWinPlay() → P0 一手清空硬规则                │
│    2. mctsEnhancedPlay() → 残局 MCTS 接管?                  │
│       (队友跟牌/死封场景豁免，主攻模式放开队友跟牌)          │
│    3. unifiedPlay() → 规则引擎                              │
│    4. intentionalImperfection() → 故意犯错 (仅领出+非MCTS)  │
│    5. MustPlay 兜底 → 最小单张                              │
│    6. Record DecisionLog → replay.Logger                    │
│  DecideBidScore / DecideDouble → bid.go                     │
└────────┬────────────────────────────────────┬───────────────┘
         │                                    │
         ▼                                    ▼
┌────────────────────────┐    ┌──────────────────────────────┐
│  MCTS 残局 (mcts.go)   │    │  规则引擎                     │
│  UCT 树搜索            │    │  ┌────────────────────────┐  │
│  三席位阵营 rollout    │    │  │ unifiedLead (领出)      │  │
│  200~1000 次 rollout   │    │  │ unifiedFollow (跟牌)    │  │
│  batchSize=100 并行    │    │  │ + farmerUpperAttackMode │  │
└────────────────────────┘    │  │ + situation.go 辅助     │  │
                              │  │ + tracker.go 记牌       │  │
                              │  │ + handcount.go 手数     │  │
                              │  │ + rule/hints.go 候选    │  │
                              │  └────────────────────────┘  │
                              └──────────────────────────────┘
```

### 3.2 决策数据流

```
Room 广播消息 → Controller.OnBroadcast/OnTargeted
  ├─ game_track：hands / counts / recent[2] / actionSeq / passes / stuck / bombNum
  └─ 机器人回合 → 延迟 1~3s → buildContextLocked 构造 GameContext
       ├─ RemainingCards = 全副牌 − actionSeq 已出牌（难度决定跟踪范围）
       ├─ PassInfos[2]：对手本轮过牌时未能压过的参照牌（过牌推理素材）
       ├─ CumPasses：跨轮次累积过牌声明 {牌型: 最高KeyRank}
       ├─ UnbeatenStreak：连续让牌/管不住对手的次数（≥2 触发拆牌重计划）
       ├─ handCountCache：回合内 MinHandCount 结果缓存（命中率 >80%）
       └─ Personality：BotPersonality 参数（Aggression/Risk/Cooperation/Mistake）
    → Engine.DecidePlay（三级结构，见 §5.1）
    → 提交 MsgPlayCards/MsgPass
    → DDZReplayAdapter.RecordDecision()

游戏结束 → onGameOver()
    → DDZReplayAdapter.GenerateReport() + AnalyzeUserDecisions()（真人失误分析）
    → 保存 ReplayReport 至 data/replays/ddz/{roomCode}_{timestamp}.txt
    → 前端 API 查询 /api/replays
```

**关键输入 GameContext**（types.go）：
- IsLandlord、Hand、BottomCards、RecentPlays[2]
- MustPlay/CanBeat、PlayerCounts[2]
- RemainingCards（记牌视图）、PlayedBombs
- UpIsLandlord/DownIsLandlord（农民角色定位）
- HasPlayed、PassInfos[2]、UnbeatenStreak
- Personality（人格化参数）

### 3.3 人格化难度系统

#### 难度链路（全链路打通）

```
前端难度选择 (easy/normal/hard)
  → 服务端 handler.go：options["difficulty"] = p.Difficulty（白名单校验）
  → Controller：DefaultPersonalities[difficulty] 加载人格参数
  → 引擎：记牌视图完整度（normal 仅关键牌 / hard 全量）+ 故意犯错概率
  → 复盘报告：Difficulty 字段记录真实档位（ctx.Personality.Name）
```

#### 三档难度配置

| 参数 | Easy | Normal | Hard |
|------|------|--------|------|
| AggressionLevel | 0.6（保守） | 1.0（均衡） | 1.3（激进） |
| RiskTolerance | 0.7（谨慎） | 1.0（标准） | 1.2（大胆） |
| CooperationWeight | 0.9（稍弱） | 1.0（标准） | 1.3（强配合） |
| MistakeProbability | 0.12（12%） | 0.03（3%） | 0.0（0%） |

所有难度**共用同一套出牌策略**（`unifiedPlay`），差异只体现在两处：
1. **记牌视图完整度**：Controller 构造 RemainingCards 时，normal 仅跟踪 A/2/大小王，hard 全量 15 点数；缺失点数按"未知"保守处理（信息越少决策越保守）
2. **故意犯错概率**：easy/normal 在领出回合以一定概率替换次优候选

#### 故意犯错机制（personality.go: intentionalImperfection）

**触发条件**：
- 仅在领出回合生效（跟牌必须正确应对）
- 非残局阶段（对手 ≤3 张时保持严谨）
- 仅在规则引擎决策后执行（MCTS 决策 winRate>0 不犯错）
- 根据 MistakeProbability 概率触发

**限制条件**：
- **P0 护栏：清空手牌的必胜出牌永不替换**（`len(chosen) == len(hand)` 直接返回）
- 不犯致命错误：不拆王炸、不拆炸弹
- 次优候选也必须安全（再次检查是否破坏炸弹/王炸）

**预期效果**：Easy 难度胜率约 40-45%，Normal 55-60%，Hard 70-75%。

### 3.4 决策解释与复盘系统

#### 通用架构（internal/games/bot/replay/）

**核心类型**：
- `DecisionLog`：单次决策日志（时间戳、上下文、候选列表、最终选择、推理说明、胜率评估）
- `ReplayReport`：整局复盘报告（关键转折点、错过机会、玩家风格、改进建议、统计数据）
- `DecisionLogger`：日志收集器（内存存储、格式化输出、报告生成）

**游戏适配器模式**：每个游戏实现自己的 Adapter（如 `DDZReplayAdapter`），负责转换游戏特定类型为通用格式，支持文本输出与结构化存储。

#### 斗地主适配器（replay_adapter.go）

**RecordDecision**：将候选着法转换为通用格式，构建上下文（难度、余牌数、UnbeatenStreak），生成自然语言解释后记录。

**斗地主特色分析**：
- **过牌推理**：检测连续让牌次数（`UnbeatenStreak ≥ 2`），识别拆牌夺权时机
- **农民配合分析**：判断队友接近走完时是否送牌帮助收尾
- **炸弹检测**：自动标记炸弹/王炸使用时刻
- **角色感知**：区分地主/地主上家（顶牌位/主攻位）/地主下家（跑牌位）的不同策略

**离线失误分析**（user_analysis.go）：session 记录真人每个决策点的手牌快照（UserMoveSnapshot），复盘时以 hard 难度人格（无故意犯错）重建决策上下文，用规则引擎求"当时最优出法"，与玩家实际选择对比判定失误与亮点。

#### 后端 API 与前端

- `GET /api/replays` - 获取复盘报告列表（支持分页、roomCode/playerId/game 过滤）
- `GET /api/replays/{filename}` - 获取单个报告详情
- 文件存储：`data/replays/ddz/`（文件名 `{roomCode}_{playerID}_{timestamp}.txt`，以真人玩家为中心）
- 前端 `replayApi.ts`：`fetchLatestReplayText` 内置 4 次×800ms 重试（报告异步落盘竞态）

---

## 4. 规则判定与核心数据结构

### 4.1 计数数组（tracker.go）

```go
[rankCount]int   // rankCount = 15
// 索引 0~14 映射面值 3,4,5,6,7,8,9,10,J,Q,K,A,2,BlackJoker,RedJoker
// 数值 = 该点数在手牌中的张数
```

**核心函数**：`handToCounts`（手牌转计数）、`subtractCounts`（逐位相减）、`countsToCards`（计数转抽象手牌）。

### 4.2 PassInfo 过牌推理结构（types.go）

```go
type PassInfo struct {
    Valid          bool                        // 是否有单轮参照牌
    Target         rule.ParsedHand             // 本轮未能压过的牌
    CumulativePass map[rule.HandType]card.Rank // 累积：{牌型: 最高KeyRank}
}
```

**双层设计**：优先使用单轮信息（Valid=true），无则回退到跨轮次累积声明。

### 4.3 ParsedHand 与 KeyRank 语义（rule/）

```go
type ParsedHand struct {
    Type    HandType    // Single/Pair/Trio/Straight/Bomb/Rocket...
    KeyRank card.Rank   // 关键牌点数
    Length  int         // 顺子/连对/飞机长度
    Cards   []card.Card // 包含的卡牌
}
```

支持 16 种牌型：Single, Pair, Trio, TrioWithSingle, TrioWithPair, Straight, PairStraight, Plane, PlaneWithSingles, PlaneWithPairs, Bomb, FourWithTwo, FourWithTwoPairs, Rocket, Invalid。

**KeyRank 语义**：顺子、连对、飞机等序列牌型的 KeyRank 是**最大点数**（窗口终点）而非起始点数。例如顺子 `4-5-6-7-8` 大过 `3-4-5-6-7`（因为 8 > 7）。`validator.go` 的 `isStraight`/`isPairStraight`/`isPlane` 均按最大点数解析。

### 4.4 统一可压判定源（rule.CanBeatWithHand）

全系统对"能否管上"的判定统一走 `rule.CanBeatWithHand(hand, target)`，支持**拆牌跟牌**（如拆一对凑顺子）。调用点：

- `session/play.go`：`notifyPlayTurn` 广播 `MsgPlayTurn.CanBeat` + 决定是否进入 `timerAutoPass` 自动过
- `session/play.go`：复盘快照
- `session/ai_play.go`：托管代打的 `gctx.CanBeat`
- `session/dto.go`：重连状态还原

**顺子族窗口比较**（beat.go）：`findWinningStraight`/`findWinningPairStraight`/`findWinningPlane` 均按**窗口终点**与 `KeyRank` 比较——`45678` 可压 `34567`；窗口长度不足或无同型无炸弹时不误报。四带二/四带两对支持同型大牌压牌。

**服务端唯一判定源的意义**：真人（前端按钮可用性 + 自动过）、机器人（跟牌评估入口）、托管（代打判定）三端行为完全一致，不会出现"提示给出顺子、按钮却灰着、随后自动过"的矛盾。

### 4.5 mctsNode（mcts.go）

```go
type mctsNode struct {
    cards    []card.Card
    visits   int
    wins     int
    parent   *mctsNode
    children []*mctsNode
}
```

UCB1 选择公式（C=√2）：`exploit = wins/visits`，`explore = C·√(ln(parentVisits)/visits)`，未访问节点优先探索。

---

## 5. 决策流程详解

### 5.1 决策入口（engine.go: DecidePlay 三级结构）

```
DecidePlay(gctx)
  ├─ [1] P0 硬规则 immediateWinPlay()（strategy.go）
  │     存在一步清空手牌的出法 → 直接返回，优先于 MCTS 与一切策略
  │     ├─ 领出：整手牌恰为一个合法牌型 → 打出整手
  │     └─ 跟牌：存在能压过上家且恰好清空手牌的候选 → 打出
  ├─ [2] mctsEnhancedPlay() → 残局 MCTS 接管（见 §5.5）
  │     豁免场景返回 nil，落到规则引擎
  ├─ [3] unifiedPlay() → 规则引擎（领出 unifiedLead / 跟牌 unifiedFollow）
  ├─ [4] 故意犯错（仅领出 + 未走 MCTS + MistakeProbability>0）
  ├─ [5] MustPlay 兜底 → 最小单张（禁止返回 nil，避免服务端拒收导致回合卡死）
  └─ [6] 记录 DecisionLog → replay.Logger
```

P0 检查前置于 MCTS 是关键设计：残局恰是"一手清空"最高频的时机（如手牌剩一对），若 MCTS 先接管会把整手牌拆散逐张出。

### 5.2 领出决策链（strategy.go: unifiedLead）

按优先级依次短路执行：

```
unifiedLead(gctx)
  ├─ [1] 整手走完检查：ParseHand(hand) 为合法单一牌型 → 直接打出（P0）
  ├─ [2] 带牌走完 finishingLead()：三带一/三带二/四带二/飞机带翅等恰好清空手牌
  ├─ [3] 两步走完+夺权牌 twoStepFinishWithControl()（不限制手牌数量，见下）
  ├─ [4] 残局冲刺：手数 ≤3 且敌方/队友均 >2 张 → sprintLead()（见 §5.6）
  ├─ [5] 残局死封：minOpponentCount ≤ 2 && 农民上家 → biggestSingleOrPair()
  ├─ [6] 送队友：队友 ≤ 2 张且地主不危急 → pickRunCard() 领小牌送权
  ├─ [7] 局势化三分支
  │     ├─ 对手 ≤ 5张 → pickCautiousLead() 谨慎控牌
  │     ├─ 地主 || 农民且下家是队友 → pickFastShed(allowSmall=true)
  │     └─ 农民上家 && 对手 > 10张 → pickFastShed(allowSmall=false) 防跟跑
  ├─ [8] 角色分工
  │     ├─ 地主下家（跑牌位）→ pickRunCard()
  │     └─ 地主上家（顶牌位）→ 主攻模式 ? pickRunCardFiltered(过滤掌权牌)
  │                              : pickBlockCard()（见 §5.4）
  ├─ [9] 通用候选评估
  │     过滤: 不拆炸弹/王炸, 出后手数必须 -1
  │     四维排序: wins(预估胜出) > split(拆牌代价) < power(掌权消耗) < score
  │     潜在炸弹预警: 裸 Q+ 大单/对降级 fallback
  └─ [10] 兜底 fallbackLead() → 面值最小整组
```

**关键设计**：

- **两步走完+夺权牌**（`twoStepFinishWithControl`）：手牌可分两步走完、第一步是绝对夺权牌（按记牌与对手余牌验证敌对方压不住，支持单张/对子/顺子/连对/飞机/炸弹等所有牌型）时，先出夺权牌拿回牌权再出剩余部分，大批量出牌不受手数限制。
- **残局冲刺**（`sprintLead`，§5.6）：手数 ≤3 且敌方/队友均未临门时，无论角色与席位都以攻为主——小牌先出、三条必带翅、掌权牌作夺权资源、最后一手保留大牌收尾。
- **手数最小化过滤**：只有出牌后总手数严格 -1 的候选才进入排序，避免"出一手拆两手"。
- **预估胜出（likelyWin）**：绝对控制牌（未知池无更大同路）或过牌推理确认对手压不过，且无炸弹威胁。
- **掌权王牌保护**（situation.go: powerLeadCost）：非胜出的掌权牌消耗按 3 倍计罚，王牌留作后手收权。
- **潜在炸弹预警**：未知池中存在某点数 4 张全在对手方时，裸出 Q+ 大单/对会被白炸，降级为 fallback。
- **顶牌位（pickBlockCard）**：中段单张（J→7）→ 最大单张（≤A）→ 中段对子（7~J）→ 最大对子（≤A），用中段牌限制地主、逼其消耗 2/王，自身掌权牌不先手出手，绝不断拆对子/三条/王炸/炸弹。

### 5.3 跟牌决策链（strategy.go: unifiedFollow）

```
unifiedFollow(gctx)
  ├─ [0] 一次走完优先：任何候选能清空手牌 → 直接打出（P0，优先于一切策略）
  ├─ [1] 农民配合（队友出牌 && 已开张）
  │     ├─ 队友 ≤ 2张 → 让牌（牌权留给队友冲刺）
  │     ├─ 冲刺模式 → pickShedFollow() 同路跑牌 → pickReliefBeat() 最小接应夺权（§5.6）
  │     ├─ 主攻模式 → pickShedFollow() 借队友 ≤J 小牌跑自己的小牌（§5.4）
  │     ├─ 辅攻 + 队友领 ≤10 单/对 → pickReliefBeat() 同路最小接应
  │     └─ 其余（队友领大牌）→ 让牌
  ├─ [2] 残局死封：danger && 农民上家 → strongestBeat()（王炸 > 炸弹 > 同型大点数）
  ├─ [3] 跑牌方积极跟牌：地主 || 地主下家 || 冲刺 + 目标 ≤J → pickShedFollow()
  │     （允许消耗 Q/K/A，2/王保留）
  ├─ [4] 通用候选评估
  │     过滤: 可压性验证 / 炸弹使用条件 / 不拆炸弹/王炸 / 手数
  │     多牌型目标放宽: 顺子/连对/飞机族允许拆 ≤1 组进入择优（见下）
  │     掌权保护: 非胜出不压 ≤10 小牌（UnbeatenStreak≥2 或主攻或冲刺时放开）
  │     排序: control(预估胜出) > after(手数) < score
  └─ [5] 未开张兜底：HasPlayed=false → anyBeat() 不计代价先出一手
```

**关键设计**：

- **多牌型目标放宽**：目标为顺子/连对/飞机族（`isMultiTypeTarget`）时，`after > current` 的候选不直接丢弃，允许拆散至多 1 组对子/三条（`splitPenalty ≤ 1`）进入择优。设计依据：对手一手可走 5 张以上，放行的节奏价值（P2）高于我方拆 1 组的结构代价（P3）。护栏：不拆炸弹/王炸、掌权牌保护维持不变。
- **拆牌重计划（replan）**：`UnbeatenStreak >= 2`（连续管不住对手）或对手 ≤5 张时，原出牌计划已失效——跳过手数过滤、放开 Q/K/A 掌权保护（2/王仍保留），允许拆散对子/三条/顺子压住对手夺回牌权。
- **掌权牌保护**：非预估胜出时不用 Q/K/A/2 压 ≤10 的小牌；`UnbeatenStreak ≥ 2` 时放开 Q/K/A；主攻模式时 Q/K/A 放宽为夺权资源（§5.4）；冲刺模式整体跳过保护（§5.6）；2/王在任何情况下都保留为收权底牌。
- **炸弹使用时机（shouldUseBomb）**：对手危急 / 炸完即可走完 / 手牌 ≤5 张三种场景才动用炸弹。
- **接应队友（pickReliefBeat）**：不拆炸弹、出牌后手数不增，优先与队友同路（单接单、对接对），无同路再放宽到任意可压候选——目标是"接住队友的牌权再送回阵营"。
- **过牌推理（noBeatFromPass）**：对手曾对参照牌过牌 → 推断其没有同路更大的牌；优先单轮信息，否则用累积声明。

### 5.4 农民上家主攻模式（strategy.go: farmerUpperAttackMode）

农民两家的分工骨架是静态的：**上家=顶牌位（辅攻），下家=跑牌位**。但配合的目的是阵营获胜——当上家自己就是更接近走完的一方时，以自己走完为第一路径反而更优。因此在 P1 内部引入**主攻/辅攻的动态指派**：上家默认辅攻，满足触发条件时该回合动态切换为主攻。

#### 判定函数（每回合无状态重算，随出牌进程自然切换）

```
farmerUpperAttackMode(gctx) bool
  必要条件: 自己是地主上家（isFarmerUpperSeat，下家是地主）
  否决条件（任一成立 → 保持辅攻）:
    - minOpponentCount ≤ 2（地主临门：死封是更高优先的胜利手段，两分支天然互斥）
    - teammateCount ≤ 2（队友临门一脚：送权期望值必高于抢跑）
  触发条件（任一成立 → 主攻）:
    A1 手牌少:      len(hand) ≤ 6（约 2~3 个回合内可清空）
    A2 牌型整:      minHandCountWithCache ≤ 3（手数比张数更贴近"容易出完"）
    A3 掌权牌充足:  手牌中 2/王 ≥ 2 张 && len(hand) ≤ 10（可稳定收回牌权两次）
    A4 已验证冲刺:  twoStepFinishWithControl() != nil（两步走完+绝对夺权牌）
```

#### 各决策点的主攻行为

| 决策点 | 辅攻（默认） | 主攻（触发时） |
|--------|--------------|----------------|
| 领出（unifiedLead 上家分支） | `pickBlockCard` 顶牌：中段 7~J 顶地主 | `pickRunCardFiltered` 跑牌：手数不增 + 面值最小整组，**过滤含 2/王的候选**（掌权牌留作收尾夺权）；无合适跑牌候选时回落通用手数领出 |
| 跟队友 ≤J 单/对 | `pickReliefBeat` 最小接应（接住牌权再送回阵营，允许跨型） | `pickShedFollow` 跟随：严格同路、≤J、2/王保留、手数不增——借队友小牌跑掉自己的小牌；找不到就让牌 |
| 跟队友大牌 | 让牌 | 让牌（不变，不为压队友消耗掌权牌） |
| 跟地主牌掌权保护 | Q/K/A/2 非胜出不压 ≤10 小牌 | **Q/K/A 放宽为夺权资源**（可参与压牌进入择优）；**2/王维持保护**（不可再生的收尾底牌） |
| MCTS 队友跟牌豁免 | 不接管（须执行农民配合） | **放开接管**（三席位阵营 rollout 下"压队友抢跑"只有对阵营有利才获高胜率，见 §5.5） |

#### 护栏与约束

- **优先级不变**：P0 一手清空 > 残局死封（地主 ≤2）> 队友 ≤2 送权 > 主攻模式（否决条件保证与死封/送权互斥）
- **不消耗收尾资源**：领出与跟队友路径均过滤 2/王；对地主路径 2/王保护不变；炸弹/王炸任何模式都不拆
- **不抢队友临门一脚**：`teammateCount ≤ 2` 否决 + 主攻跟队友只走 ≤J 同路小牌
- **下家职责不变**：地主下家（跑牌位）行为完全不动；动态指派目前仅限上家
- **难度无关**：所有难度共用主攻判定（与 unifiedPlay 的"难度只体现在记牌视图完整度"原则一致）；normal 难度视图缺失时 A3/A4 按未知保守处理，A1/A2 不受影响

### 5.5 MCTS 残局增强（mcts.go）

**触发条件（shouldUseMCTS）**：自己手牌 ≤5 张，或任一对手 ≤3 张。

**豁免场景（mctsEnhancedPlay，不接管、交还规则引擎）**：
- 队友出牌后的跟牌：须执行农民配合约束——**农民上家主攻模式除外**（主攻以自己走完为第一路径，且 rollout 为三席位阵营判定，"压队友抢跑"只有在对阵营有利时才获高胜率）
- 农民上家残局死封（对手 ≤2 张）：须用最强牌型一次性封堵，模拟胜率难以体现死封的战略价值

**UCT 搜索流程（mctsDecidePlay）**：

```
1. 生成候选（领出: 全部合法牌型，排除炸弹类; 跟牌: 能压过目标的）
2. P0 预筛：存在清空手牌的候选直接返回（immediateWinPlay 的双保险）
3. 构建 UCT 树: root.children = 各候选节点
4. 主循环 (batch=100, total=200~1000)：并行 rollout + 批量回传
5. 选胜率最高的子节点，返回真实模拟胜率（winRate ∈ [0,1]，记入日志观测）
```

**三席位阵营 rollout（mctsSimulateWin）**——模拟贴近真实规则：

- 从 unknown 牌池按上/下家**真实余牌数**（`PlayerCounts[2]`）分别发牌，三席位轮转出牌（我 → 下家 → 上家）
- 农民视角：一席队友、一席地主；地主视角：两席均为敌人
- 领出权被压时正确移交，连续两过开新轮；rollout 出牌策略沿用 `mctsQuickPlay`（领出最小合法组、跟牌最小可压组，不动用炸弹）
- **阵营胜负判定**：农民 = 任一非地主走完记胜；地主 = 自己走完记胜、任一农民走完记负——搜索目标与 P1 阵营配合对齐
- 步数上限随全场剩余牌数自适应（`rolloutPlayCap`），未分胜负时剩余牌少的一方阵营判胜

**发牌（dealOpponentHands）**：未跟踪点数按 2 张估计；池不足时按两家余牌数比例截断，保证不发虚拟牌也不会死循环。

**动态模拟次数（getMctsIterations）**：自己/对手 ≤2 张 → 1000 次（高精度）；自己 ≤5 张 → 500 次；其他 → 200 次。

### 5.6 残局冲刺模式（strategy.go: sprintMode / sprintLead）

**触发条件（sprintMode，不分地主/农民、上家/下家）**：

- 自己手数 ≤3（`minHandCountWithCache ≤ 3`，至多 3 手可出完）
- 否决条件（任一成立 → 不冲刺，走原有路径）：敌方最少余牌 ≤2（死封优先）；农民队友余牌 ≤2（送权优先）

即：只要自己接近走完、而双方都还没到"临门一脚"的距离，无论角色与席位都以攻为主——胜负手在自己，攻即是守。

#### 领出（sprintLead，unifiedLead 优先级 [4]）

候选过滤：不拆炸弹/王炸、出牌后手数必须严格 -1（冲刺阶段绝不允许手数不降）。排序优先级：

1. **after（剩余手数）升序**——多走手数、逐手清空
2. **wins（绝对掌权/预估胜出）优先**——掌权牌作夺权资源：打出后大概率收回牌权的候选先行（控制判定含三带一/三带二：`sprintControl` 在 `isTwoStepControl` 基础上补充带牌型，主体三条无更大即掌权）
3. **finisher（剩余最后一手是绝对掌权牌）优先**——倒数第二手非绝对掌权时，保留最强的一路牌收尾，便于最后跟随对手出牌取胜
4. **maxRank 升序**——小牌先出、大牌留最后一手
5. **张数降序**——同主体优先带牌散牌（三带一/三带二优于裸三条，顺带清走小牌）
6. **sumRank 升序**——面值更小者优先

#### 跟牌（unifiedFollow 三处放宽）

- **跟队友牌**：先 `pickShedFollow` 借队友 ≤J 小牌同路跑牌，借不到再 `pickReliefBeat` 最小接应夺取牌权接管节奏（炸弹不动、手数不增）——自己手数 ≤3 时抢跑优先于单纯让牌
- **积极跟牌**：`pickShedFollow` 的适用席位从"地主/地主下家"放宽到所有席位（含农民上家）
- **掌权保护放开**：Q/K/A/2 压小牌的掌权保护整体跳过（候选排序仍偏向最小可压牌，掌权牌只在必要时消耗）

#### 与既有机制的优先级关系

P0 一手清空 > 两步走完+夺权牌 > 残局冲刺 > 残局死封 / 农民配合（与冲刺互斥：敌方 ≤2 时死封优先、队友 ≤2 时送权优先，由 sprintMode 内建否决保证）> 常规局势化决策。手牌 ≤5 张的场景仍由 MCTS 残局增强接管（§5.5），冲刺主要作用于"手数 ≤3 但张数 >5"的窗口；炸弹/王炸在任何分支仍不拆。

---

## 6. 关键算法

### 6.1 MinHandCount 手数计算（handcount.go）

**算法**：DFS + 记忆化搜索，在 15 维计数数组上操作。

```
minHandCountDFS(c, memo, nodes):
  1. 锚定剩余最小点数 i
  2. 枚举包含 i 的所有合法组合:
     - 单张/对子/三条 / 三带一 / 三带二
     - 炸弹 / 四带二 / 四带两对
     - 锚点作带牌: 更大点数的三条/炸弹带 i
     - 顺子(i起, 长度≥5, 不含2/王) / 连对(i起, 长度≥3)
     - 飞机(长度≥2, 不带/带单/带对, 含锚点是翅膀的场景)
     - 火箭(大小王)
  3. 对每种选择递归求解 1 + minHandCountDFS(剩余)
  4. 取最小值，memo[c] = best
```

**性能保障**：
- 节点上限 20000（`maxHandCountNodes`），超限返回全单张上界
- `handCountCache` 每回合新建，按计数数组键值缓存，实测命中率 >80%
- 带牌选取用贪心策略（`greedySingles`/`greedyPairs`）：优先取现成单张/对子，避免拆大组合

### 6.2 记牌与推理系统（tracker.go）

**记牌器数据流**：

```
全副54张 → 减去 actionSeq 所有已出牌 → RemainingCards
  ├─ hard难度: 跟踪全部15个点数
  └─ normal难度: 仅跟踪 A/2/大小王（关键牌）→ 缺失点数 = 未跟踪(未知)
```

**核心功能**：
- **unknownCounts**：记牌视图减去自己手牌，未跟踪点数标记为 -1（保守处理）
- **potentialBombRanks**：未知池中某点数剩余 4 张 → 成炸预警
- **isAbsoluteControl**：打出某点数后，未知池中不存在更大的同路牌可压
- **过牌推理**（noBeatFromPass）：对手对参照牌过牌 → 推断其没有同路更大的牌

### 6.3 叫分逻辑（bid.go）

**手牌强度评分（handStrength）**：

| 因子 | 分值 |
|------|------|
| 炸弹（4张） | +3 |
| 大王 | +2 |
| 小王 | +1.5 |
| 2 | +1 |
| A | +0.5 |
| 结构加分（structureBonus） | 手数节省×0.3 + 连续点数奖励 |
| 孤张惩罚（isolatedPenalty） | 孤张2: -0.5, 单王无2: -0.3 |

**结构加分**：手数节省 `saved = len(hand) - MinHandCount(hand)`，每节省一手 +0.3；最长连续 ≥5 +1.0（顺子潜力），≥10 +1.5。

**叫分阈值**：≥7.5→3分，≥5.5→2分，≥3.5→1分，≥4.5→加倍。

### 6.4 候选生成与带牌小牌化（rule/hints.go）

**GenerateHints**：领出调用 `generateLeadHints`（全牌型合法组合），跟牌调用 `generateFollowHints`（含拆牌跟牌），结果按 Score 升序排序。

**带牌小牌化（pickSinglesFrom/pickPairsFrom）**：
- 结构优先级：现成单张 > 拆对子 > 拆三张 > 拆炸弹（兜底）
- 若首选含掌权王牌（Q/K/A/2/王）→ 改按点数升序从最小非炸弹组拆小牌
- 目的：小牌顺带走完，大牌留作后手夺权

**兜底带牌（fallbackKickers）**同策略：带牌只取现成单张不拆牌；首选含掌权牌时改从最小非炸弹组拆出小牌。

---

## 7. 性能优化

### 7.1 缓存体系

| 缓存 | 位置 | 生命周期 | 效果 |
|------|------|---------|------|
| handCountCache | handcount.go | 每回合 | MinHandCount 命中率 >80%，避免重复 DFS |
| 记牌视图 | controller.go | 每局 | 增量更新（append actionSeq），非每次全量扫描 |
| cumPasses | controller.go | 跨轮次持久化 | 过牌推理信息不因轮次切换丢失 |

### 7.2 并行化

**MCTS 批量并行**（mcts.go）：batchSize=100，goroutine pool + sync.WaitGroup 并发 rollout；批量完成后统一加锁更新树节点，减少锁竞争；残局决策耗时降低约 40-60%。

### 7.3 剪枝策略

| 策略 | 位置 | 效果 |
|------|------|------|
| MinHandCount 节点上限 20000 | handcount.go | 超限退化为全单张上界 |
| 手数过滤 | strategy.go | 领出只保留出后手数 -1 的候选 |
| 炸弹/王炸保护 | strategy.go | 不拆炸弹/王炸，减少无效候选 |
| MCTS 豁免场景 | mcts.go | 队友配合/死封不走 MCTS，避免无效模拟 |
| rollout 步数上限 | mcts.go | rolloutPlayCap 随全场剩余牌数自适应，防止大手牌局面开销失控 |

### 7.4 拟人工程

- 决策延迟：随机 1~3s（thinkDelay），测试可注入覆盖
- goroutine 异步决策，不阻塞消息处理
- 领出兜底：MustPlay 时禁止返回 nil（engine.go）

---

## 8. 主流方案对比与定位

### 8.1 设计目标再明确

**当前目标：普通人的上位水准（而非最强 AI）**

| 维度 | "最强 AI" 目标 | "普通人上位" 目标 |
|------|---------------|------------------|
| **胜率** | >90% vs 人类 | 60-70% vs 休闲玩家，30-40% vs 资深玩家 |
| **可解释性** | 次要（黑盒也可接受） | **核心**（玩家需理解决策逻辑） |
| **完美程度** | 追求最优解 | **允许次优但合理**（模拟人类思维） |
| **趣味性** | 不重要 | **关键**（风格多样、有学习价值） |
| **技术复杂度** | 越高越好 | **适度**（易维护、易调试） |

### 8.2 方案对比

| 方案 | 优势 | 劣势 | 适配度 |
|------|------|------|--------|
| **当前实现**<br>（规则+轻量MCTS） | ✅ 完全可解释<br>✅ 冷启动即用<br>✅ 农民配合与主攻指派完善<br>✅ 工程简单<br>✅ 人格化难度系统<br>✅ 复盘报告生成 | ❌ 不完全信息处理弱<br>❌ 搜索深度有限 | ⭐⭐⭐⭐⭐<br>**最佳选择** |
| **DouZero**<br>（强化学习） | ✅ 接近最优策略<br>✅ 推理速度快 | ❌ 黑盒不可解释<br>❌ 需训练数据<br>❌ 模型文件大（~120MB）<br>❌ 过强（碾压玩家）<br>❌ 无法调节难度 | ⭐⭐<br>**不适合** |
| **完整 ISMCTS**<br>（信息集树搜索） | ✅ 理论完备<br>✅ 无需训练 | ❌ 计算成本高（5s+/步）<br>❌ 实现复杂<br>❌ 仍为黑盒<br>❌ 对休闲玩家过于强大 | ⭐⭐⭐<br>**过度设计** |
| **PerfectDou**<br>（自博弈+搜索） | ✅ 最强性能 | ❌ 工程量巨大<br>❌ 完全黑盒<br>❌ 需要大量GPU训练 | ⭐<br>**完全不适用** |
| **Rule-Based + RL微调**<br>（混合方案） | ✅ 可解释基础<br>✅ 可通过RL优化参数 | ❌ 需要标注数据<br>❌ 调参复杂<br>❌ 收益不确定 | ⭐⭐⭐<br>**可选但不优先** |

### 8.3 为什么当前方案最适合目标

#### 可解释性 = 学习价值

**玩家能从机器人学到什么**：
- **掌权牌保护**：为什么 Q/K/A/2 不轻易压小牌？→ 留作后手收权
- **手数最小化**：为什么要拆顺子出三带？→ 减少总出牌次数
- **农民配合与主攻**：为什么上家有时辅佐、有时自己冲？→ 谁更接近走完谁主攻
- **残局死封**：为什么地主剩 2 张时要出最大牌？→ 一次性封堵，不给喘息机会
- **故意犯错**：为什么机器人有时出了次优牌？→ 模拟人类失误，增加真实感

DouZero 无法提供这些：玩家看到"AI 出了这张牌"，但不知道"为什么"。

#### 适度失误 = 可玩性

**当前实现的"合理缺陷"恰好让机器人**：
- 记牌不完整（normal 仅跟踪关键牌）→ 偶尔误判对手牌型
- MCTS 仅残局触发 → 中期局势缺乏前瞻，可能错过最优解
- 故意犯错机制（easy/normal）→ 12%/3% 概率替换次优候选
- 不会"算无遗策"，玩家有机会通过技巧获胜；表现出"人类式"思维局限，更亲切

#### 风格多样 = 趣味性

- **保守型**（easy）：Aggression=0.6, Risk=0.7，优先保本，适合新手练习
- **均衡型**（normal）：Aggression=1.0, Risk=1.0，攻守平衡，中等挑战
- **激进型**（hard）：Aggression=1.3, Risk=1.2，频繁拆牌夺权，供资深玩家研究

#### 复盘系统 = 教学工具

- 每步决策都有自然语言解释（"为什么要这样出"）
- 游戏结束自动生成复盘报告（关键转折点、错过机会、真人失误分析、改进建议）
- 后端 API 支持历史查询、统计分析；这是纯 AI 方案无法提供的

### 8.4 能力雷达图（按"普通人上位"目标评分）

```
              可解释性    趣味性    易维护    冷启动    适度挑战    工程成本    复盘能力
              (权重×3)   (权重×2)  (权重×2)  (权重×1)  (权重×2)   (权重×1)    (权重×2)
当前实现        9×3=27    8×2=16    9×2=18    10×1=10   8×2=16     9×1=9      10×2=20
                                                                          Total: 116
DouZero         4×3=12    5×2=10    4×2=8     3×1=3     3×2=6      4×1=4       2×2=4
                                                                          Total: 47
ISMCTS          7×3=21    6×2=12    5×2=10    6×1=6     7×2=14     5×1=5       3×2=6
                                                                          Total: 74

* 权重反映"普通人上位"目标的优先级
```

**结论：当前实现在目标导向下得分最高（116分），远超 DouZero（47分）和 ISMCTS（74分）。**

### 8.5 工程实现差异

| 维度 | 当前实现 | DouZero | ISMCTS |
|------|---------|---------|--------|
| **决策引擎** | 规则启发式 + UCT残局 | 深度神经网络 | 蒙特卡洛树搜索 |
| **训练需求** | 零训练 | 数百万局自博弈 | 无需训练 |
| **模型大小** | 0 MB（纯代码） | ~120 MB（ONNX） | 0 MB |
| **推理速度** | <1s（规则）/<500ms（MCTS） | ~50ms | 5s+ |
| **难度调节** | BotPersonality 参数 | 无法调节 | 调整模拟次数 |
| **决策解释** | ✅ 自然语言解释 | ❌ 黑盒 | ❌ 黑盒 |
| **复盘报告** | ✅ 完整报告生成 | ❌ 不支持 | ❌ 不支持 |
| **代码量** | ~6000 行 Go | ~50000 行 Python + C++ | ~10000 行 |
| **依赖复杂度** | 零外部依赖 | PyTorch + ONNX Runtime | 无 |
| **部署难度** | 编译即部署 | 需加载模型文件 | 编译即部署 |

---

## 9. 测试与验证

### 9.1 测试框架

**完整自博弈模拟测试**（`bot/simulation_test.go`）：
- 每种难度（easy/normal/hard）运行 200 局
- 随机发牌 + 固定种子确保可复现
- 验证所有玩家（包括机器人）必须出牌，不允许整局 0 出牌

**分层单测**：

| 测试文件 | 覆盖内容 |
|---------|---------|
| `bot/win_first_test.go` | P0 一手清空（领出整对直接获胜）、多牌型跟牌放宽、故意犯错 P0 护栏 |
| `bot/strategy_attack_test.go` | 农民上家主攻模式：判定函数表驱动（A1~A4 触发/否决）、主攻领出跑牌、跟队友小牌跟随、Q/K/A 放宽、MCTS 豁免放开 |
| `bot/straight_follow_test.go` | 拆牌跟顺子场景 |
| `bot/engine_test.go` / `controller_test.go` / `tracker_test.go` / `handcount_test.go` / `bid_test.go` | 引擎调度、状态跟踪、记牌、手数、叫分 |
| `rule/beat_test.go` | 顺子/连对/飞机窗口终点比较（45678 压 34567）、四带二同型比较、不误报 |
| `rule/rule_test.go` / `hints_test.go` | 牌型解析与候选生成 |

### 9.2 验证命令

```bash
# 单元测试（排除耗时自战模拟）
go test ./internal/games/ddz/bot/ -skip TestSelfPlay -count=1

# 100 局自战模拟（三座位难度轮转 easy/normal/hard）
# ⚠️ 仅在修改机器人出牌/决策逻辑后运行（./scripts/run_tools.sh bot），常规测试一律跳过
go test ./internal/games/ddz/bot/ -run TestSelfPlay_100Games -count=1 -timeout 15m

# 静态分析
go vet ./internal/games/ddz/bot/
```

### 9.3 基线指标

| 指标 | 说明 |
|------|------|
| 零出牌不变量 | 0 局存在可压候选却整局沉默 |
| 非法出牌 | 0（引擎失语/非法牌型直接 Fatal） |
| 地主/农民胜率 | 地主约 1/3、农民合计约 2/3（3 座位均分轮转） |

策略类改动（如主攻模式）的回归验收口径：100 局模拟农民胜率不低于基线、地主胜率不异常上涨、零出牌不变量保持、非法出牌为零。

---

## 10. 优化路线：当前能力与下一步

### 10.1 当前能力速查（实现方案见对应章节）

| 能力 | 实现方案 | 详见 |
|------|---------|------|
| 胜利优先级阶梯 | P0 一手清空硬规则（`immediateWinPlay` 前置于 MCTS）+ P0 护栏贯穿故意犯错/MCTS 预筛 | §2/§5.1 |
| 统一可压判定源 | `rule.CanBeatWithHand`（session 三端一致）+ 顺子族窗口终点比较 + 拆牌跟牌 | §4.4 |
| 农民上家主攻模式 | `farmerUpperAttackMode` 动态指派（A1~A4 触发 + 死封/送权否决）+ 领出/跟牌/MCTS 三处行为切换 | §5.4 |
| 人格化三档难度 | `DefaultPersonalities` + `intentionalImperfection` + 记牌视图完整度 + 全链路档位传递 | §3.3 |
| MCTS 残局增强 | UCT + 三席位阵营 rollout + 并行 + 真实余牌发牌 | §5.5 |
| 决策解释与复盘 | 通用 replay 层 + `DDZReplayAdapter` + 真人失误离线分析 + `/api/replays` | §3.4 |
| 叫分结构因子 | `structureBonus`（手数节省×0.3 + 连续点数奖励）+ `isolatedPenalty` | §6.3 |
| 决策性能 | `handCountCache` 回合缓存（命中率 >80%）+ MCTS 并行 rollout（batchSize=100） | §7 |
| 收官决策 | 一次走完优先（`finishingLead`）+ 两步走完夺权牌（`twoStepFinishWithControl`） | §5.2 |
| 局势反制 | 拆牌重计划（`UnbeatenStreak`）+ 跑牌方积极跟牌（`pickShedFollow`）+ 带牌小牌化 + 多牌型跟牌放宽 | §5.3 |

### 10.2 待优化项（按优先级）

#### 高优先级：对手建模简化版

**现状**：过牌推理（PassInfo 单轮 + CumPasses 累积）与 MCTS 真实余牌发牌已覆盖"牌张在哪"的推断；但机器人不记忆对手风格，每局都像第一次见面。

**实施内容**：
1. gameTrack 增加 per-opponent EWMA 画像：出大牌率（Aggression）、让牌率（Cooperation）、炸弹使用时机（BombTiming）；单局内维护，不跨局持久化
2. 画像反向调节 personality 参数：对激进对手更谨慎（RiskTolerance ×0.8）、对保守对手更施压（AggressionLevel ×1.2）
3. MCTS 发牌权重叠加风格先验（激进对手的大牌权重上调、保守对手的持炸概率上调）

**约束**：最小样本阈值（<8 次交互用默认画像）；EWMA α=0.1 防单次异常主导；不引入跨局持久化（避免"被针对"的负反馈）。

#### 中优先级：主攻模式动态指派扩展到下家

当前动态主攻/辅攻指派仅限地主上家（顶牌位）。地主下家（跑牌位）可引入对称判定：当队友（上家）手牌易走完而自己牌型差时，从"自己跑牌"切换为"辅佐队友"（如领出更小的牌、不与队友抢小牌跟牌权）。需模拟回归验证收益后再实施。

#### 低优先级：稳定性加固

1. MCTS 超时保护：context 400ms 超时，超时回退规则引擎并记日志
2. 长时间运行下的 goroutine 数量监控（handCountCache 已按回合新建，无泄漏风险）
3. 压测基线：记录当前 100 局自战耗时作为回归基线

### 10.3 明确放弃的优化方向

基于"普通人上位"目标，以下技术路线**不再优先考虑**：

| 原计划 | 放弃原因 | 替代方案 |
|--------|---------|---------|
| DouZero ONNX 接入 | 黑盒决策不可解释，推理速度优势对回合制游戏无意义 | 决策解释系统 + 人格化参数 |
| 完整 ISMCTS 实现 | 理论完备但对休闲玩家过于强大，违背设计初衷 | 保持当前 UCT + 规则引擎两级架构 |
| 深度贝叶斯对手建模 | 开发成本高，玩家感知不明显 | 对手建模简化版（EWMA 风格画像） |
| 完美信息假设下的最优策略 | 不符合斗地主不完全信息特性 | 保持当前保守处理（未知 = 危险） |

**核心理由**：这些"学术级"优化会将机器人推向"不可战胜的 AI"，偏离"值得反复对战的学习伙伴"定位。当前实现在可解释性、适度挑战性、开发维护成本三者间已取得最佳平衡。

### 10.4 演进路径

```
当前状态
✅ 胜利优先级阶梯（P0 一手清空硬规则 + 统一可压判定源 + 三席位阵营 rollout）
✅ 农民上家主攻模式（主攻/辅攻动态指派 + 三处决策点行为切换 + 护栏）
✅ 人格化三档难度（全链路打通：前端选择 → 服务端转发 → 引擎参数 + 复盘真实档位）
✅ 决策解释与复盘系统（四游戏集成 + 后端 API + 前端全链路 + 真人失误分析）
✅ 性能优化（handCountCache + MCTS 并行 rollout + rollout 步数自适应）

↓ 近期
🔄 对手建模简化版：EWMA 风格画像 + MCTS 发牌风格先验
   里程碑：机器人对激进/保守对手呈现可感知的针对性策略

↓ 中期
📋 主攻模式动态指派扩展到地主下家（对称判定 + 模拟回归验证）

↓ 远期
⚡ 稳定性加固：MCTS 超时保护 + 压测回归基线
```

**定位重申**：不追求"最强 AI"，打造"最懂玩家的对手"——新手在 easy 档找到乐趣，进阶玩家通过复盘提升牌技，高手在 hard 档获得挑战。

---

## 附录：文件索引（关键函数）

> 以函数名为准（行号随代码演进会漂移），按决策调用层级排列。

| 函数 | 文件 | 功能 |
|------|------|------|
| Engine.DecidePlay | engine.go | 出牌决策总入口（三级调度 + 故意犯错 + 决策日志） |
| immediateWinPlay | strategy.go | P0 硬规则：一步清空手牌的出法直接返回 |
| intentionalImperfection | personality.go | 故意犯错机制（P0 护栏：清空手牌永不替换） |
| unifiedPlay | strategy.go | 统一出牌分发（领出/跟牌） |
| unifiedLead | strategy.go | 领出决策链 |
| unifiedFollow | strategy.go | 跟牌决策链 |
| farmerUpperAttackMode | strategy.go | 农民上家主攻模式判定（A1~A4 触发 + 否决条件） |
| handPowerCount | strategy.go | 手牌中 2/王（收尾掌权牌）张数 |
| pickRunCard / pickRunCardFiltered | strategy.go | 跑牌出最小非炸组合（可过滤变体供主攻模式排除掌权牌） |
| pickBlockCard | strategy.go | 顶牌（中段优先逼地主消耗 2/王） |
| pickShedFollow | strategy.go | 积极跟牌（≤J 同路小牌，2/王保留，手数不增） |
| pickReliefBeat | strategy.go | 接应队友小牌（同路优先、手数不增） |
| shouldUseBomb | strategy.go | 炸弹使用时机判断 |
| likelyWin | strategy.go | 预估胜出判断（绝对控制 + 过牌推理 + 无炸威胁） |
| noBeatFromPass | strategy.go | 过牌推理（单轮 + 累积声明） |
| twoStepFinishWithControl / isTwoStepControl | strategy.go | 两步走完+绝对夺权牌检测（支持所有牌型） |
| biggestSingleOrPair / strongestBeat | strategy.go | 残局死封（领出/跟牌最强手段） |
| isMultiTypeTarget | strategy.go | 顺子/连对/飞机族目标判断（多牌型跟牌放宽入口） |
| beatsTarget | strategy.go | 候选可压性验证（出牌前必须验证，防止服务端拒收） |
| splitPenalty | strategy.go | 拆牌代价（拆散对子/三条数量） |
| mctsEnhancedPlay | mcts.go | MCTS 接管入口（队友跟牌/死封豁免，主攻放开队友跟牌） |
| mctsDecidePlay | mcts.go | UCT 搜索主循环（含 P0 预筛） |
| mctsSimulateWin | mcts.go | 三席位阵营 rollout（真实余牌发牌 + 领出权移交） |
| dealOpponentHands | mcts.go | 上/下家按真实余牌数发牌（池不足按比例截断） |
| mctsQuickPlay | mcts.go | rollout 简化出牌策略（领出最小/跟牌最小可压） |
| shouldUseMCTS / getMctsIterations | mcts.go | 触发条件与动态模拟次数 |
| MinHandCount | handcount.go | DFS+记忆化手数计算 |
| unknownCounts / potentialBombRanks / isAbsoluteControl | tracker.go | 未知牌池、炸弹预警、绝对控制 |
| handStrength / structureBonus / isolatedPenalty | bid.go | 手牌强度评分（含结构因子与孤张惩罚） |
| GenerateHints | rule/hints.go | 候选生成入口（领出/跟牌，含拆牌跟牌） |
| CanBeatWithHand | rule/beat.go | 统一可压判定源（支持拆牌跟牌 + 顺子族窗口终点比较） |
| ParseHand | rule/rule.go | 牌型解析（16 种牌型，KeyRank=窗口终点） |
| Controller.OnBroadcast / buildContextLocked | controller.go | 广播消息处理、GameContext 构造 |
| DDZReplayAdapter.RecordDecision / GenerateReport | replay_adapter.go | 决策日志记录、复盘报告生成 |
| AnalyzeUserDecisions | user_analysis.go | 真人出牌快照离线失误分析 |
