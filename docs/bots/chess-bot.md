# 中国象棋机器人完整技术方案

> **源码位置**：`internal/games/chess/bot/`（engine.go + difficulty.go + opening_book.go + position_tables.go + user_analysis.go）  
> **目标定位**：普通人的上位水准 —— 对新手有压迫感但不碾压，对中级玩家有挑战但可战胜，对高手有抵抗力但非不可击败

---

## 目录

1. [概述与定位](#1-概述与定位)
2. [架构总览](#2-架构总览)
3. [核心数据结构](#3-核心数据结构)
4. [决策流程详解](#4-决策流程详解)
5. [关键算法](#5-关键算法)
6. [性能特性](#6-性能特性)
7. [主流方案对比](#7-主流方案对比)
8. [测试与验证](#8-测试与验证)
9. [能力详解与后续方向](#9-能力详解与后续方向以普通人上位水准为目标)

---

## 1. 概述与定位

### 1.1 设计目标

**核心定位：普通人的上位水准**

机器人不是"不可战胜的 AI"，而是"值得反复对战的学习伙伴"。目标是让普通玩家：
- **有可玩性**：不会因碾压而沮丧，也不会因太弱而无趣
- **有思考空间**：决策逻辑透明可理解，玩家能从中学习战术技巧
- **有提升路径**：通过观察机器人的落子，玩家能改进自己的技术

**能力水平：**
- 超越 50-60% 的休闲玩家（中等偏上）
- 能被 30-40% 的进阶玩家稳定击败（留有挑战空间）
- 偶尔打出精彩操作（展示高级战术，供玩家学习）

**设计原则：**
1. **可解释优先**：所有决策规则透明，玩家能理解"为什么这样走"
2. **适度失误**：不追求完美最优解，允许次优但合理的决策
3. **攻守平衡**：根据局势动态调整进攻/防守权重，模拟人类思维
4. **冷启动即用**：无需训练数据，部署即可提供有意义的对战体验

### 1.2 技术路线

**经典博弈树搜索引擎：NegaMax + α-β 剪枝 + 迭代加深 + 静态搜索**

- **无模型、无训练、零外部依赖**：编译即部署，无需 GPU 或模型文件
- **策略水平**：业余初段 ~ 业余3段（相当于社区棋社中等偏上水平）
- **设计目标**：可调试、规则透明、决策耗时可控（0.5~4s 动态拟人化）、有趣味性
- **核心特色**：
  - 完整的静态搜索（Quiescence Search，Delta 剪枝）
  - Zobrist 置换表（64K，generation 淘汰，哈希含执子方，见 §5.1）
  - 空着裁剪（Null Move Pruning，R=2，无验证搜索，见 §5.2）
  - 后期着法缩减（Late Move Reduction，带全深度重搜验证）
  - 和棋判定：三次重复判和感知实战历史 + 自然限着求和倾向（contempt）+ 困毙判负（见 §5.5）
  - 并发根节点评估（每层全部根着法重算）
  - 时间预算控制：searchTimeMs=2000ms，迭代加深超时停止（见 §4.1）
  - 多档难度系统：DifficultyConfig 三档预设 + Top-N softmax 扰动（见 §9.2）
  - 增量 Zobrist 哈希：zobristStep，每节点省去全盘 90 格重算（见 §5.1）
  - 将军检测限流：orderMoves 仅对基础分前 8 候选做将军检测（见 §5.4）
  - PV 复用：根着法按上轮评分重排 + 历史/杀手表跨迭代积累（见 §4.1）
  - 拟人化：动态思考时间（humanDelay 0.5~4s）+ 收官粗糙（大优势降 1 层）
  - 开局库：62 条线路 / 150+ 局面条目，加权随机选择（见 §9.4）

### 1.3 模块清单

| 模块 | 文件 | 职责 |
|------|------|------|
| 搜索引擎 | engine.go | 决策链、NegaMax（含 NMP）、置换表、Zobrist（含执子方）、局面评估 |
| 难度配置 | difficulty.go | DifficultyConfig 三档预设（easy/normal/hard）+ normalize + DifficultyFor |
| Controller | controller.go | 消息驱动（MsgGameStart/MsgCcTurn/MsgCcMoveMade/MsgGameOver）：棋盘镜像、动态思考时间（humanDelay 0.5~4s）、消费 options["difficulty"]、调用 DecideMoveDetailed |
| 开局库 | opening_book.go | 62 条开局线路 / 150+ 局面条目，覆盖中炮/飞相/仙人指路/过宫炮/起马/巡河炮/边马 + 多种黑方应招，加权随机选择（见 §9.4） |
| 位置表 | position_tables.go | 6 张位置表（车/马/炮/象/士/兵），黑方镜像 row=9-row |
| 用户复盘分析 | user_analysis.go | AnalyzeUserDecisions：离线用引擎求"当时最优走法"，失误分级（Critical/Major/Minor）+ 绝杀错失检测 + 亮点标记（见 §8.3） |

---

## 2. 架构总览

### 2.1 整体架构图

```
┌─────────────────────────────────────────────────────────────┐
│                     Room / GameSession                       │
│  广播消息 (MsgCcMoveMade / MsgGameStart / MsgGameOver)       │
└──────────────────────┬──────────────────────────────────────┘
                       │ 消息路由（象棋无隐藏信息）
┌──────────────────────▼──────────────────────────────────────┐
│                   Bot Controller                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ board: 9×10 棋盘镜像（从广播消息重建）                │   │
│  │ currentPlayer: 当前执子方（红/黑）                     │   │
│  │ moveCount: 已落子数                                    │   │
│  └──────────────────────────────────────────────────────┘   │
│  humanDelay(0.5~4s) → Engine.DecideMove → SubmitBotAction    │
└──────────────────────┬──────────────────────────────────────┘
                       │ BoardState
┌──────────────────────▼──────────────────────────────────────┐
│                  Decision Engine                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ DecideMove(): 落子决策                                 │   │
│  │  决策链:                                               │   │
│  │  1. 开局库 Lookup（精确局面命中，62 条线路 150+ 局面） │   │
│  │  2. 迭代加深 1→4 层（searchTimeMs=2000ms 超时停止）   │   │
│  │     ├─ negamax(depth, α, β)（含 NMP R=2）            │   │
│  │     ├─ LMR (非吃子非杀手, >4手降1层, >8手降2层)      │   │
│  │     └─ depth=0 → quiescence                          │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ rule.AllLegalMoves(): 合法着法生成（rule 层，         │   │
│  │  过滤送将/对将/白脸将）                               │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ evaluate(): 局面评估                                   │   │
│  │  = material + position + kingSafety                  │   │
│  │    + pawnStructure + mobility + check                │   │
│  └──────────────────────────────────────────────────────┘   │
└──────────────────────┬──────────────────────────────────────┘
                       │ 规则计算
┌──────────────────────▼──────────────────────────────────────┐
│                    Evaluation Layer                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ material     │  │ position     │  │ kingSafety   │      │
│  │ (子力价值)   │  │ (位置加成)   │  │ (将安全)     │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ pawnStructure│  │ mobility     │  │ check        │      │
│  │ (兵形)       │  │ (机动性)     │  │ (将军动态)   │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 决策数据流

```
Room 广播消息 → Controller.OnBroadcast
    ├─ MsgGameStart: 初始化棋盘镜像
    ├─ MsgCcTurn: 确认机器人回合
    ├─ MsgCcMoveMade: 更新棋盘镜像 board[Idx(row,col)] = piece
    └─ 机器人回合 → humanDelay(0.5~4s) → Engine.DecideMoveWithContext(board, camp, GameContext)
         ↓
    DecideMoveWithContext 决策链（无上下文时 GameContext 零值，行为同 DecideMoveDetailed）:
         ├─ 1. 开局库 Lookup（精确局面 key 命中，62 条线路 150+ 局面）
         └─ 2. 迭代加深 1→4 层（每层并发评估全部根着法，
              │   取最高分；置换表跨层复用，历史/杀手表每层重建；
              │   searchTimeMs=2000ms 超时停止加深）
              └─ negamax(depth, α, β)
                   ├─ Zobrist Hash（含执子方 + zobristStep 增量）+ 置换表查询
                   ├─ 重复局面判和（实战历史 GameContext.History + 搜索路径）
                   ├─ 终局判定（将死/困毙均 -90000-depth，困毙判负同中国规则）
                   ├─ Move Ordering（五重打分）
                   ├─ NMP（非被将军且 depth≥3，R=2）
                   ├─ LMR（非吃子非杀手，>4 手降 1 层、>8 手降 2 层 + 超α重搜）
                   ├─ α-β 剪枝
                   └─ depth=0 → quiescence
                        ├─ 被将军：搜索全部应将着法（链深过深则静态评估收敛）
                        ├─ 安静局面：stand-pat
                        ├─ MVV-LVA 排序 + Delta 剪枝（安全余量 200）
                        └─ 仅延伸吃子着法
         ↓
    返回最佳落子 (fromRow, fromCol, toRow, toCol) → MsgCcMoveMade
```

**关键输入**：
- `board []int`: 一维 90 格棋盘（`rule.Idx(row,col) = row*9+col`）
- `camp`: 当前执子方（红=1，黑=2）

---

## 3. 核心数据结构

### 3.1 棋盘表示

```go
// board 为一维 []int，长度 90；Idx(row,col) = row*Cols + col
// 棋子编码：十位=阵营（1 红 / 2 黑），个位=兵种（rule/rule.go）
// 红方：11=帅, 12=车, 13=马, 14=炮, 15=相, 16=仕, 17=兵
// 黑方：21=将, 22=车, 23=马, 24=砲, 25=象, 26=士, 27=卒
// Empty = 0；红方在 row 6-9（下方），黑方在 row 0-3（上方）
```

**特点**：
- 一维 90 格，红黑由十位区分、兵种由个位区分（`CampOf=p/10`、`TypeOf=p%10`）
- `rule.AllLegalMoves` / `AllLegalCaptures` 生成全量合法着法（过滤送将/对将）
- `IsInCheck` / `IsCheckmate` / `IsStalemate` / `FlyingGeneralsOk`（白脸将检测）齐备

### 3.2 Move 结构

```go
// rule.Move（走法生成时即携带吃子信息）
type Move struct {
    FromRow, FromCol int
    ToRow,   ToCol   int
    Captured int // 被吃棋子编码（0 = 无吃子）
}
```

排序评分（MVV-LVA 等）在 `orderMoves` 内即时计算，不随 Move 携带。

### 3.3 置换表条目

```go
type chessTTEntry struct {
    key   uint64      // Zobrist hash
    value int         // 评估值
    depth int         // 搜索深度
    bound ttBoundType // Exact/LowerBound/UpperBound
    age   uint16      // 世代标记（LRU 淘汰）
}

type ttBoundType int
const (
    Exact       ttBoundType = iota // 精确值
    LowerBound                     // 下界（α 提升）
    UpperBound                     // 上界（β 降低）
)
```

**设计要点**：
- `age` 实为 generation 世代标记：每轮迭代加深 `advanceGeneration()`，get 命中即保活（刷新为当前世代）
- 查询时仅在条目深度 ≥ 请求深度时可用
- 按边界类型正确剪枝（Exact 直接返回，Lower 仅提升 α，Upper 仅压低 β）
- 存储策略：空槽 / 更深深度 / 非当前世代条目直接覆盖
- 容量 65536 条目（64K），迭代加深循环外创建，跨层复用
- 哈希含执子方（zobristSide），增量更新（zobristStep）

### 3.4 子力价值表

按兵种 `TypeOf` 计分（`pieceValues`，红黑同价）：

| TypeOf | 兵种 | 分值 | 说明 |
|--------|------|------|------|
| 1 | 帅/将 | 10000 | 被将死即输（搜索返回 -90000-depth） |
| 2 | 车 | 600 | 最强攻击子 |
| 3 | 马 | 270 | 灵活机动 |
| 4 | 炮 | 285 | 远程攻击 |
| 5 | 相/象 | 120 | 防守子 |
| 6 | 仕/士 | 110 | 九宫守卫 |
| 7 | 兵/卒 | 30 | 过河后由位置表加成（最高 +100） |

---

## 4. 决策流程详解

### 4.1 决策链（DecideMove）

```
DecideMove(board, camp)
  ├─ 1. 开局库 Lookup（任意回合均查询，key 为精确局面，
  │     62 条线路覆盖前 5~8 回合常见定式；中盘不可能误命中 → 立即返回）
  │
  └─ 2. 迭代加深 1→4 层（searchTimeMs=2000ms 超时停止加深）
        tt := newChessTranspositionTable()  // 循环外创建，跨层复用
        for depth := 1; depth <= 4; depth++ {
            // 超过时间预算即停止加深
            tt.advanceGeneration()          // 进入新世代，旧条目降权
            hh, kh := newHistoryTable(), newKillerTable() // 每层重建

            // 并发评估全部根着法（按 NumCPU 分片）：
            //   CopyBoard + ApplyMoveWithCapture 后 -negamax(depth-1)
            // 收集结果取最高分更新 bestMove/bestScore
        }
        return bestMove  // 无合法着法返回 (-1,-1,-1,-1)
```

**关键设计思路**：
- **短路执行**：开局库命中立即返回，避免无效搜索
- **迭代加深**：置换表跨层复用带来剪枝收益；根着法按上轮评分重排（PV 复用），历史/杀手启发表跨迭代积累
- **时间预算**：searchTimeMs=2000ms，迭代加深超时时停止加深

### 4.2 搜索算法（negamax）

```go
func negamax(board []int, depth, alpha, beta, camp int, hash uint64,
             tt *chessTranspositionTable, hh *historyTable,
             kh *killerTable, history []uint64) int {
    // 1. hash 由调用方增量传入（根层 zobristHash，子层 zobristStep）

    // 2. 重复局面检测：hash 在 history 中出现 ≥2 次 → 三次重复判和
    //    （history = 实战对局历史 GameContext.History + 当前搜索路径，
    //     根节点由 DecideMoveWithContext 传入实战序列，离线分析传 nil）

    // 3. 置换表查询（entry.depth >= depth 时按边界类型使用）

    // 4. 生成全部合法着法（depth==0 之前，叶节点可复用给 quiescence）
    moves := rule.AllLegalMoves(board, camp)
    if len(moves) == 0 {
        return -90000 - depth // 将死/困毙均判负（中国象棋困毙同负）
    }
    if depth == 0 {
        return quiescence(board, moves, alpha, beta, camp,
                          quiescenceDepth, append(history, hash))
    }

    // 5. Move Ordering（五重打分，见 §5.4）
    orderMoves(moves, board, camp, hh, kh, depth)

    // 6. 空着裁剪（NMP）：非被将军且 depth>=3，R=2（见 §5.2）
    //    -negamax(board, depth-3, -beta, -beta+1, opp) >= beta → 返回 beta

    // 7. NegaMax 递归
    best := -100000
    originalAlpha := alpha
    newHistory := append(history, hash) // 追加当前哈希到搜索路径
    for i, m := range moves {
        moveCount := i + 1
        nb := rule.CopyBoard(board)
        captured := rule.ApplyMoveWithCapture(nb, m)

        // 7.1 LMR：非吃子且非杀手，第 5+ 手降 1 层、第 9+ 手降 2 层；
        //     降深结果 > alpha 时用全深度重搜验证（见 §5.3）
        score := -negamax(nb, searchDepth, -beta, -alpha, opp, ...)

        if alpha >= beta {
            kh.add(m, depth)   // β 剪枝 → 更新杀手启发
            break
        }
        if score > originalAlpha && captured == 0 {
            hh.add(m, depth)   // 历史启发更新（+1<<depth）
        }
    }

    // 8. 置换表存储（fail-low→Upper / fail-high→Lower / 窗口内→Exact）
    tt.put(hash, best, depth, bound)
    return best
}
```

### 4.3 静态搜索（quiescence）

```go
func quiescence(board []int, moves []rule.Move, alpha, beta, camp,
                depth int, history []uint64) int {
    // 1. 重复局面检测（同 negamax）

    // 2. 被将军：stand-pat 无效，搜索全部应将着法（全量合法走法）
    if inCheck {
        if len(moves) == 0 { return -90000 - depth } // 将死
        if depth <= -quiescenceDepth {
            // 应将链过深（连将循环等极端情形），直接静态评估收敛
            return evaluate(board, camp)
        }
    } else {
        // 3. 安静局面：stand-pat 作为下界（不吃也是合法选择）
        standPat := evaluate(board, camp)
        if depth <= 0 { return standPat }
        if standPat >= beta { return standPat }
        best = standPat

        // 4. 仅延伸吃子：首节点复用上层传入走法过滤吃子，
        //    深层节点用 rule.AllLegalCaptures；按 MVV-LVA 插入排序

        // 5. Delta 剪枝：standPat + 受害者价值 + 200 < alpha → 跳过
    }

    // 6. 递归延伸（depth-1），β 截断返回
}
```

**关键特性**：
- 只延伸吃子着法（被将军除外，应将需搜全部着法）
- Delta 剪枝安全余量 200（覆盖将军加分等评估波动）
- 应将链过深直接静态评估收敛，防无限递归；深度符号约定：安静局面从
  quiescenceDepth(4) 递减，被将军链可递减至 -4

### 4.4 评估函数（evaluate）

```go
func evaluate(board []int, camp int) int {
    // 双方各累计：material / position / kingSafety /
    //             pawnStructure / mobility

    // 全盘扫描（子力+位置单次遍历累计；将安全/兵形/机动性各另扫 2 次）
    for row := 0; row < 10; row++ {
        for col := 0; col < 9; col++ {
            piece := board[row][col]
            if piece == Empty { continue }

            // 子力价值 + 位置加成（6 张位置表）按阵营累计
        }
    }

    // 将安全
    myKingSafety = kingSafety(board, camp)
    enemyKingSafety = kingSafety(board, oppColor(camp))

    // 兵形
    myPawnStructure = pawnStructure(board, camp)
    enemyPawnStructure = pawnStructure(board, oppColor(camp))

    // 机动性
    myMobility = mobility(board, camp)
    enemyMobility = mobility(board, oppColor(camp))

    // 将军动态
    // 对方被将军 +50；己方被将军 -100（加倍惩罚）

    // 综合评分 = 子力 + 位置 + 将安全 + 兵形 + 机动性 + 将军动态（差值制）
    return score
}
```

**评估项权重**：
| 评估项 | 分值范围 | 说明 |
|--------|---------|------|
| 子力价值 | 将 10000 / 车 600 / 炮 285 / 马 270 / 象 120 / 士 110 / 兵 30 | 基础物质优势 |
| 位置加成 | 0~100/子 | 6 张位置表（车巡河线、马中心、炮中路、兵过河后递增） |
| 将安全 | -80~+120 | 九宫内士/象守子数（+30/个，≤4）+ 王暴露度（-10/空邻格，≤8） |
| 兵形 | 0~+50 | 过河兵左右协同（+15/对）+ 深入奖励（(4-r)*5，最高 +20） |
| 机动性 | 0~+100+ | 车/马/炮 PseudoMoves 伪走法数（+2/格） |
| 将军动态 | +50/-100 | 对方被将军 +50，己方被将军 -100（加倍惩罚） |

---

## 5. 关键算法

### 5.1 Zobrist Hash

**问题**：简单 hash 冲突率高，影响置换表准确性

**方案**：Zobrist 哈希（含执子方 + 增量更新）

```go
var zobristTable [rule.Rows * rule.Cols][28]uint64 // 90 格 × 28 状态
//（棋子编码 11~27，实际使用 0, 11~27）
var zobristSide uint64 // 执子方哈希常量

func init() {
    // 确定性种子 LCG：seed = 0x123456789abcdef0
    // seed = seed*6364136223846793005 + 1442695040888963407
}

func zobristHash(board []int, camp int) uint64 {
    var h uint64
    for i, v := range board {
        if v != rule.Empty { h ^= zobristTable[i][v] }
    }
    if camp == rule.CampBlack { h ^= zobristSide }
    return h // ✅ 含执子方
}

// zobristStep 增量更新：从旧哈希走一步到新局面（含执子方翻转）
func zobristStep(oldHash uint64, fromIdx, toIdx, piece, captured int) uint64 {
    h := oldHash
    h ^= zobristTable[fromIdx][piece] // 移除起点
    h ^= zobristTable[toIdx][piece]   // 添加终点
    if captured != rule.Empty {
        h ^= zobristTable[toIdx][captured] // 移除被吃
    }
    h ^= zobristSide // 每步翻转执子方
    return h
}
```

**关键特性**：
- 90 格 × 28 种棋子状态，确定性种子（便于调试复现）
- **哈希含执子方**（`zobristSide` 常量，黑方时异或）：
  同一棋子摆放、不同轮走方的局面拥有不同哈希 →
  置换表条目不跨执子方误用；重复局面检测正确区分不同走方
- **增量更新**（`zobristStep`）：negamax/quiescence 每节点通过
  `zobristStep(parentHash, fromIdx, toIdx, piece, captured)` 增量计算子哈希，
  省去全盘 90 格扫描；每步自动异或 `zobristSide` 翻转执子方

### 5.2 空着裁剪（Null Move Pruning）

**原理**：如果一方"跳过不走"仍能达到 β 值，说明局面明显优势，可以剪枝

```go
// negamax 内（Move Ordering 之后、递归循环之前）
inCheck := rule.IsInCheck(board, camp)
if !inCheck && depth >= 3 {
    // 不实际走子，直接以缩减深度、零窗口搜索对方回合
    nullScore := -negamax(board, depth-1-2, -beta, -beta+1, opp, ...) // R=2
    if nullScore >= beta {
        return beta // 空着仍能守住 beta，当前节点可剪枝
    }
}
```

**实现细节 / 局限**：
- 直接在原 board 上以对方阵营搜索（无需复制棋盘，仅交换走子权）
- **无验证搜索**（fail-high 后不做全深度确认）、**无连续空着防护**；
  象棋中 zugzwang（被迫走子恶化局面）风险远低于国象，可不加低子力护栏

### 5.3 后期着法缩减（Late Move Reduction）

**原理**：经过良好排序后，排在前面的着法更可能是最佳着法，后面的着法可以降低搜索深度

```go
// 递归循环内（非独立函数）
reduction := 0
if moveCount > 4 && captured == 0 && !kh.isKiller(m, depth) {
    reduction = 1                  // 第 5+ 手且非吃子非杀手：降 1 层
    if moveCount > 8 {
        reduction = 2              // 第 9+ 手：再降 1 层
    }
}
searchDepth := depth - 1 - reduction
if searchDepth < 0 { searchDepth = 0 }

score := -negamax(nb, searchDepth, -beta, -alpha, opp, ...)
if reduction > 0 && score > alpha {
    // 降深结果超 α：全深度重搜验证，避免漏掉战术
    score = -negamax(nb, depth-1, -beta, -alpha, opp, ...)
}
```

**效果**：减少 20-30% 搜索节点；带"超 α 重搜"比纯缩减更稳健

### 5.4 Move Ordering（五重打分）

```go
func scoreMove(move Move, board []int, color int) int {
    score := 0

    // 1. MVV-LVA（Most Valuable Victim - Least Valuable Attacker）
    captured := board[move.ToRow][move.ToCol]
    attacker := board[move.FromRow][move.FromCol]
    if captured != Empty {
        score += pieceValues[captured] * 100 - pieceValues[attacker]
    }

    // 2. 杀手着法（Killer Move）
    if isKillerMove(move, depth) {
        score += 50000
    }

    // 3. 历史启发（History Heuristic）
    score += historyScore[move.FromRow][move.FromCol][move.ToRow][move.ToCol] * 10

    // 4. 将军着法（需 CopyBoard 模拟）
    boardCopy := copyBoard(board)
    applyMove(boardCopy, move)
    if isInCheck(boardCopy, oppColor(color)) {
        score += 20000
    }

    // 5. 兵推进（兵卒接近九宫）
    if isPawn(attacker) && isNearPalace(move.ToRow, move.ToCol, color) {
        score += 5000
    }

    return score
}
```

**排序效果**：吃子优先 → 杀手着法 → 历史好着 → 将军着法 → 兵推进

**实现注意**：将军检测已限流——仅对基础分前 `checkProbeLimit=8` 的候选做
CopyBoard + 走子 + IsInCheck，其余候选跳过将军检测。
省去大量全盘复制，是评估之外的第二热路径优化。

### 5.5 和棋判定（重复局面 / 自然限着 / 困毙）

**问题**：长将循环导致对局无法结束；劣势方无法利用和棋规则自保；
困毙（无合法走法且未被将军）曾被误判为和棋（中国象棋规则困毙判负）

**方案**：实战历史 + 搜索路径双重 Zobrist 序列追踪，三次重复判和；
限着计数逼近判和线时启用求和倾向（contempt）

```go
// negamax/quiescence 签名携带 history []uint64（实战历史 + 搜索路径哈希栈）
repeatCount := 0
for _, h := range history {
    if h == hash { repeatCount++ }
}
if repeatCount >= 2 { return 0 } // 历史出现 2 次 + 当前 = 三次重复
// 每层递归前 newHistory := append(history, hash)

// 根层（DecideMoveWithContext）：
//   - GameContext.History：controller 随每手追加 zobristHash(board, 执子方)，
//     实战中已重复两次的局面在搜索内计 0 分 → 劣势方天然倾向求和、优势方回避；
//   - 求和倾向：HalfmoveClock ≥ 16 ply（8 回合，约限着线 60%）且根评估领先 ≥ 100 时，
//     切换到 margin（200）内分值最高的吃子候选（bestCaptureMove），
//     吃子重置限着计数，避免被服务端 13 回合自然限着判和；
//   - 困毙：无合法走法且未被将军 → -90000-depth（判负，同中国规则）。
```

**服务端权威判和**（session，机器人只需感知）——三条规则按优先级依次判定：
1. 不变作和：同一局面（含执子方，`rule.PositionHash`）出现 3 次；
2. 简单和棋局面：双方均无车马炮兵卒（`rule.InsufficientMaterial`，帅仕相无法将死对方）；
3. 自然限着：连续 13 回合（26 ply）双方均未吃子（吃子重置，休闲对战快速终局）。
判和原因经 `GameOverPayload.DrawReason`（repetition/natural_limit/insufficient_material）广播。

**关键特性**：
- 实战历史由 controller 维护（gameTrack.history + halfmoveClock）并经 GameContext 传入搜索；
  离线分析（复盘等）走 DecideMoveDetailed 传零值上下文，行为与旧版一致；
- 评估层：双方均无攻子局面 evaluate 直接返回 0（子力不足判和）

---

## 6. 性能特性

### 6.1 当前性能指标

| 指标 | 数值 |
|------|------|
| 16 手自对弈总耗时 | 9.15s |
| 平均每手 | 0.57s |
| 搜索节点数 | ~120K/手 |
| 置换表命中率 | 60-80% |

### 6.2 主要优化点与效果

| 优化点 | 实现方案 | 效果 |
|--------|---------|------|
| 开局库精确编码 | positionKey（90 格逐字节 + 执子方），62 条线路 150+ 局面 | 开局阶段着法可靠 |
| 静态搜索 | quiescence 叶子延伸吃子/应将 + Delta 剪枝（余量 200） | 消除水平线效应，无效搜索 -15~20% |
| 搜索加速 | Zobrist 置换表 + LMR（带超α重搜）+ Move Ordering 五重打分 | 支撑 4 层迭代加深在秒级完成 |
| 空着裁剪 | NMP R=2（非被将军 depth≥3，无验证搜索） | 优势局面剪枝 |
| 重复局面检测 | 实战历史 + 搜索路径双重 Zobrist 三次重复判和 + 求和倾向（contempt） | 消除循环局面；劣势求和/优势避和（见 §5.5） |
| 评估增强 | 将安全/兵形/机动性动态项 | 决策质量提升，单手 0.57s 可接受 |

### 6.3 热点函数分析

| 函数 | 调用频率/局 | 单次耗时 | 总耗时占比 |
|------|------------|---------|-----------|
| evaluate | ~50,000 | 10-20μs | **40%** |
| rule.AllLegalMoves | ~10,000 | 5-10μs | **25%** |
| negamax（内部节点） | ~100,000 | 1-2μs | **20%** |
| quiescence | ~5,000 | 20-50μs | **10%** |
| 其他（哈希/置换表） | - | - | **5%** |

**性能现状**：评估函数（全盘扫描逐格累计）为第一热点；Move Ordering 的将军检测
已限流（仅前 8 候选）；Zobrist 已采用增量更新（zobristStep）。
当前性能（0.57s/步）已满足目标，无进一步优化压力（见 §9 不推荐方向）。

---

## 7. 主流方案对比

### 7.1 不同技术路线的适用性

| 路线 | 代表 | 强度 | 实现成本 | 对本项目的适用性 |
|------|------|------|---------|----------------|
| **α-β 规则系（当前）** | 本工程 | 业余3~5段 | 低 | ✅ **最适合**：强度可控，易调节 |
| **NNUE** | Pikafish | 职业级 | 高 | ❌ 过强：远超目标水平，训练成本高 |
| **MCTS** | 某些开源引擎 | 业余2~4段 | 中 | ⚠️ 可选：天然有随机性，但实现复杂 |

**结论**：当前 α-β 规则系是最优选择，只需调整参数和增加随机性即可达到目标水平。

### 7.2 技术差距矩阵

| 维度 | 当前实现 | 象眼 ElephantEye | Pikafish | 差距分析 |
|------|---------|-----------------|----------|---------|
| **搜索深度** | 4 层（等效 ~6 层 with LMR） | 8~12 层 | 15+ 层 | **主要差距**：看不到深层战术（对目标定位而言是特性） |
| **置换表** | 64K，generation 淘汰（key 含执子方） | 数百 MB，LRU + age | 数 GB，large pages + prefetch | 容量差 100x+（无需补齐） |
| **Move Ordering** | MVV-LVA + 杀手 + 历史 + PV 复用 + 将军检测限流 | + PV 优先 + SEE | + NNUE 预排序 | 可加 SEE 指导排序 |
| **评估函数** | 手工表 + 简单动态项 | 精细手工（机动性/保护/兵形） | NNUE（轻量神经网络） | **本质差距**：NNUE vs 手工（对本项目是特性） |
| **静态搜索** | delta 剪枝 | 标配 | + SEE 指导 | 可加 SEE 指导 |
| **空着裁剪** | R=2（无验证搜索） | R=3 + 验证搜索 | R=3 + adaptive | 可加验证搜索 |
| **LMR** | ✅ 简单规则 | ✅ 复杂公式 | ✅ NNUE 指导 | 可用但粗糙（够用） |
| **重复检测** | ✅ 三次重复判和 | ✅ + 长将判负 | ✅ + 亚洲规则 | 缺长将判负 |
| **开局库** | 62 条线路 / 150+ 局面条目，加权随机选择 | 数万局统计 | 数百万局 + 在线更新 | 已覆盖常见定式 |
| **杀棋搜索** | ❌ 靠 NegaMax 自然发现 | ✅ Mate hunt | ✅ DTS + mate threat | 缺专门处理（对目标定位而言是特性） |
| **增量哈希** | zobristStep 增量更新（含执子方翻转） | 部分增量 | NNUE 增量 | — |

### 7.3 "普通人上位水准"适配度分析

| 特性 | 对强度的影响 | 对可玩性的影响 | 当前状态 |
|------|-------------|---------------|---------|
| 搜索深度 4 层 | 中等（看不到深组合） | ✅ 正相关（给人类反击空间） | 合适 |
| 评估函数粗糙 | 中等（会犯低级错误） | ✅ 正相关（人类也会犯错） | 合适 |
| 开局库覆盖 | 62 条线路 / 150+ 局面条目，覆盖前 5~8 回合常见定式 | ✅ 正相关（开局有充分指导） | ✅ 已实现 |
| 无杀棋搜索 | 弱（漏杀常见） | ✅ 正相关（人类也常漏杀） | 合适 |
| 决策时间 0.5~4s | 无影响 | ✅ 拟人化动态调整（humanDelay） | ✅ 已实现 |
| 多档难度 | - | ✅ 正相关（easy/normal/hard 三档） | ✅ 已实现 |

---

## 8. 测试与验证

### 8.1 单元测试

```bash
# 开局库（红方首着命中 / 屏风马精确 / 防误命中回归）
go test ./internal/games/chess/bot/ -run TestOpeningBook -v

# 静态搜索（回收吃识别 / 安静局面等于静态评估）
go test ./internal/games/chess/bot/ -run TestQuiescence -v

# 自对弈合法性回归（16 手逐手比对 AllLegalMoves）
go test ./internal/games/chess/bot/ -run TestEngineSelfPlayMovesAlwaysLegal -v

# 100 局自战回归（同难度 80 局 + 跨难度梯度局 20 局，须加长 timeout）
# ⚠️ 仅在修改机器人出牌/决策逻辑后运行（./scripts/run_tools.sh bot），常规测试一律跳过
#（run_tools.sh test 已用 -skip '^TestSelfPlay_100Games$' 排除；象棋自战耗时约 20 分钟）
go test ./internal/games/chess/bot/ -run TestSelfPlay_100Games -count=1 -v -timeout 45m

# 开局库线路合法性回归（防非法着法再入库）
go test ./internal/games/chess/bot/ -run TestOpeningBookLinesLegal -v

# 真人复盘分析（失误分级 / 绝杀检测 / 非法快照跳过）
go test ./internal/games/chess/bot/ -run TestAnalyzeUser -v

# 难度调节测试（DifficultyFor / normalize / DecideMoveDetailed）
go test ./internal/games/chess/bot/ -run TestDifficulty -v

# 增量 Zobrist 正确性（zobristStep == zobristHash 全盘重算）
go test ./internal/games/chess/bot/ -run TestZobristStep -v

# 收官粗糙 / 动态思考时间
go test ./internal/games/chess/bot/ -run "TestApplyEndgameCoarsening|TestHumanDelay" -v

# 和棋判定（重复局面求和 / 困毙判负 / 子力不足评估 / 求和倾向吃子选择）
go test ./internal/games/chess/bot/ -run "TestEvaluateInsufficientMaterial|TestNegamaxStalemateLoses|TestLosingBotSeeksRepetitionDraw|TestContemptPrefersCapture" -v

# 服务端判和（三次重复 / 自然限着 / 子力不足 / 快照恢复）
go test ./internal/games/chess/session/ -run "TestDraw|TestNoFalseDraw|TestSnapshotDrawState" -v

# 规则层和棋纯函数（PositionHash / InsufficientMaterial）
go test ./internal/games/chess/rule/ -run "TestPositionHash|TestInsufficientMaterial" -v
```

**测试覆盖**：
- ✅ 开局库：红方首着合法（6 种首着加权随机）、中炮后黑方应招合法（多种应招加权随机）、黑方初始局面不误命中
- ✅ 开局库覆盖：条目总数 ≥ 100，红方首着种类 ≥ 5，加权随机验证屏风马占比最高
- ✅ 静态搜索：弃车吃卒的回收吃场景正确 stand-pat；初始局面返回静态评估
- ✅ 自对弈 16 手全部合法，且引擎不改写传入棋盘
- ✅ 100 局自战：非法走子为零、引擎不改棋盘，跨难度局 hard 对 easy 保持压制（详见 selfplay_test.go）
- ✅ 开局库线路全量合法性（TestOpeningBookLinesLegal；曾修复 6 条坐标错误线路，详见 §9.4）
- ✅ 难度调节：DifficultyFor 预设命中、normalize 零值回退、DecideMoveDetailed easy 深度限制
- ✅ 增量 Zobrist：zobristStep 与全盘重算一致性验证
- ✅ 收官粗糙：大优势降深、easy 档不降
- ✅ 和棋判定：劣势方走向三次重复（0 分）、困毙深负分、子力不足评估为 0、contempt 吃子切换；
  服务端三次重复/自然限着/子力不足判和与快照恢复（详见 rule/draw_test.go、session/play_test.go、bot/engine_draw_test.go）
- ✅ 动态思考时间：简单/复杂局面延迟范围正确

**自战实测基线**（自然限着改为 13 回合后待重新实测，以下为旧 40 回合口径下的历史数据）：

| 指标 | easy | normal | hard |
|------|------|--------|------|
| 席位局数 | 50 | 40 | 30 |
| 席位胜率 | 14 胜 18 和（28%） | 26 胜 14 和（65%） | 20 胜 10 和（67%） |
| 平均深度 / 最大深度 | 1.99 / 2 | 2.76 / 3 | 3.73 / 4 |
| 开局库命中 | 19 | 53 | 75 |

跨难度梯度局 20 局（先后手各半）：hard 对 easy 10/10 全胜；normal 对 easy 8 胜 2 和 0 负。平均手数 80~84/局，终局均为将死/困毙/三次重复判和，无手数上限强停局。

### 8.2 真人复盘分析链路（user_analysis.go）

真实复盘以真人玩家为中心：

```
session.handleMove（走子前，bot/托管跳过）
  → recordUserMove: 记录 UserMoveSnapshot{Board, Camp, From/To}
  → session.endGame → 异步 generateUserReplayReports
  → bot.AnalyzeUserDecisions(decisions)：
       对每个决策点用引擎 DecideMove 求"当时最优走法"
       evalAfterMove 对比实际/最优走法后的局面评估
       ├─ 与引擎一致且收益 >150 分 → 亮点标记
       ├─ 引擎可绝杀而实际未绝杀 → Critical
       ├─ 分差 ≥150 → Critical；≥60 → Major；其余 → Minor
       └─ 丢子换算描述（≥600 车 / ≥270 马/炮 / ≥110 士象 / ≥30 兵卒）
  → replay.BuildUserReport → FormatUserReplayReport
  → replay.SaveUserReport（replays/chess/<room>_<player>_<ts>.txt）
```

---

## 9. 能力详解与后续方向（以"普通人上位水准"为目标）

### 9.1 当前能力速查（实现方案见对应章节）

| 能力 | 实现方案 | 详见 |
|------|---------|------|
| 搜索引擎 | NegaMax + α-β + 迭代加深（4 层）+ 置换表 + 时间预算（2s） | §2/§4 |
| 静态搜索 | quiescence（叶子延伸）+ Delta 剪枝 | §4 |
| LMR | 后半段安静着法降深 + 全深验证 | §5 |
| Move Ordering | 五重打分（MVV-LVA/杀手/历史等）+ 将军检测限流（checkProbeLimit=8） | §5 |
| 重复检测/和棋 | 实战历史三次重复判和 + 自然限着求和倾向 + 困毙判负 | §5.5 |
| 评估 | 子力 + 位置表 + 将安全/兵形/机动性 + 将军动态 | §4 |
| 开局库 | 62 条线路 / 150+ 局面条目，加权随机选择，覆盖前 5~8 回合常见定式 | §1.3 / §9.4 |
| 多档难度 | DifficultyConfig 三档预设 + Top-N softmax 扰动（easy/normal/hard） | §9.2 |
| 增量 Zobrist | zobristStep 增量更新（含执子方翻转），每节点省去全盘重算 | §5.1 |
| PV 复用 | 根着法按上轮评分重排 + 历史/杀手表跨迭代积累 | §4.1 |
| 拟人化 | 动态思考时间（humanDelay 0.5~4s）+ 收官粗糙（applyEndgameCoarsening ≥600 降 1 层） | §9.3 |
| 真人复盘分析 | session 快照 → `AnalyzeUserDecisions` 离线求最优 + 失误分级（Critical/Major/Minor）+ 绝杀错失/亮点 | §8.2 |

---

### 9.2 多档难度系统

三档难度（easy/normal/hard）适配不同水平玩家。服务端 handler 统一转发
`options["difficulty"]`（easy/normal/hard 白名单，随 GameStartPayload 下发），
Controller 消费后传递至引擎。

#### 难度参数配置（与 `bot/difficulty.go` 实现一致）

| 参数 | 简单 | 普通 | 困难 |
|------|------|------|------|
| **最大搜索深度** | 2层 | 3层 | 4层 |
| **开局库使用概率** | 30% | 50% | 80% |
| **Top-N 扰动** | Top-5 候选 | Top-3 候选 | Top-2 候选 |
| **时间预算** | 2000ms | 2000ms | 2000ms |

```go
type DifficultyConfig struct {
    Name            string  // 难度名称：easy/normal/hard
    MaxDepth        int     // 迭代加深最大搜索深度（2 / 3 / 4）
    OpeningBookRate float64 // 开局库使用概率 (0-1]：低档位概率性放弃定式，模拟人类不熟开局
    RandomTopN      int     // Top-N 候选按 softmax 概率选择：1 = 永远选最优（既有行为）
    SearchTimeMs    int     // 迭代加深时间预算（毫秒）；0 = 不限时（跑满 MaxDepth）
}
```

- `DifficultyFor(name)`：空或未知档位回退 normal
- 零值配置 `normalize()` 回退引擎既有行为（4 层搜索 / 始终用开局库 / 永远选最优 / 2s 预算），
  保证会话托管兑底、复盘分析等未携带难度的调用方行为稳定
- `QuickDifficultyPresets`：测试专用快速预设（`SearchTimeMs=999999` 等效不限时），难度梯度仍由 MaxDepth 保证

#### Top-N softmax 扰动

Top-N 候选按 softmax 概率选择，模拟不同水平玩家的决策偏差：
- 简单档：约30%的着法不是最优，模拟新手失误
- 普通档：约15%的着法有偏差，模拟中级玩家犹豫
- 困难档：约5%的微小偏差，保持挑战性

**实现细节**：Engine.DecideMoveDetailed 按 DifficultyConfig 驱动决策链；
Top-N softmax 扰动温度 = 前 N 名分差/4；bestScore < 将死阈值时扰动不生效，
保证必胜着法不被扰动。

---

### 9.3 拟人化特征

#### 动态思考时间（humanDelay）

根据局面复杂度动态调整延迟：
- 简单局面（无威胁）：0.5~1.5s
- 中等局面（有吃子/将军）：1.5~2.5s
- 复杂局面（多重威胁）：2.5~4s

**实现**：基于合法着法数量和最高分差值判断复杂度

#### 收官粗糙（applyEndgameCoarsening）

静态评估领先 ≥600（约等于多一车）时降 1 层搜索深度，模拟人类优势局面下的
不精细；easy 档 MaxDepth=2 不再降。

#### 人类错误模拟的近似覆盖

视线盲区/防守过度/计算深度限制由"浅搜索深度（easy 档 2 层）+ Top-N 扰动"
整体近似覆盖——浅深度天然漏算深层战术，Top-N 扰动模拟次优选择。

---

### 9.4 开局库

开局库覆盖前 5~8 回合常见定式，避免"乱下"。

#### 覆盖范围

62 条开局线路，生成 150+ 局面条目：

**覆盖**：
- 红方首着：中炮类（15 种）、飞相类（9 种）、仙人指路（8 种）、过宫炮（6 种）、起马（5 种）、巡河炮（4 种）、边马（2 种）
- 黑方应招：屏风马、反宫马、顺炮、列炮、单提马、飞象、卒底炮、对兵局等

**实现方式**：`buildFromLines()` 从预定义线路逐步走子，沿途每个局面的下一步着法以线路权重累加到 entries 中；写入前逐手校验合法性，非法着法及其后续不再入库（历史问题：曾发现 6 条线路坐标错误——车/兵阻挡误判、相/炮目标点被自家棋子占据等，非法着法入库后被精确局面命中导致引擎走出非法棋，已修复并由 TestOpeningBookLinesLegal 回归防复发）

#### 统计权重

**实现**：每条记录附加"流行度"字段（`OpeningEntry.Weight`），按加权随机选择
```go
type OpeningEntry struct {
    Move   rule.Move
    Weight int // 流行度/权重（越高越常见）
}
// 选择时按 Weight 加权随机
```

**验证**：TestOpeningBookWeightedSelection 确认屏风马应招在中炮后占比最高（权重 20+12+10+...）

---

### 9.5 后续优化方向

| 方向 | 说明 | 预估 |
|------|------|------|
| 开局库继续扩充 | 接入 ElephantEye 在线数据库，覆盖 10+ 回合 | 2~3 天 |
| 长将/长捉判负 | 棋例“禁止着法”裁判逻辑（长将单方判负、双方不变作和的完整待判裁定） | 2~3 天 |
| VCF/VCT 专项测试 | 杀棋搜索正确性验证 | 1~2 天 |

### 9.6 不推荐的优化方向

| 优化项 | 原因 |
|--------|------|
| **增量评估** | 当前性能已足够（0.57s/步），且会降低评估的"人类误差"特征 |
| **NNUE** | 评估太准，难以模拟人类错误；训练成本高 |
| **搜索深度>4层** | 远超目标水平，破坏可玩性 |
| **杀棋搜索** | 偏离"普通人上位"目标，人类也常漏杀 |

---

**最终目标**：
- 简单档：新手胜率 30~40%，有明显进步空间
- 普通档：中级玩家胜率 45~55%，需要认真思考
- 困难档：高手胜率 60~70%，有挑战但可战胜
- 所有档位：决策时间 0.5~4s，符合人类节奏

---

## 附录：文件索引（关键函数/行号）

| 函数 | 文件:行 | 功能 |
|------|---------|------|
| Engine.DecideMove | engine.go:51 | 落子决策基础接口（委托 DecideMoveWithContext，零值上下文） |
| Engine.DecideMoveWithContext | engine.go:74 | 落子决策总入口（开局库 + 迭代加深 + 并发根评估 + 判和上下文） |
| bestCaptureMove | engine.go:232 | 求和倾向：margin 内最优吃子候选 |
| negamax | engine.go:302 | NegaMax + α-β + NMP + LMR + 实战历史重复判和 |
| quiescence | engine.go:430 | 静态搜索（吃子/应将延伸 + Delta 剪枝） |
| evaluate | engine.go:543 | 局面评估（含子力不足判和 0 分） |
| kingSafety / pawnStructure / mobility | engine.go:592 / 652 / 705 | 动态评估项 |
| orderMoves | engine.go:752 | 五重打分排序 |
| zobristHash | engine.go:1001 | Zobrist 哈希（含执子方，见 §5.1） |
| zobristStep | engine.go:1016 | 增量 Zobrist（含执子方翻转，见 §5.1） |
| Controller.OnBroadcast | controller.go:58 | 广播消息处理（GameStart/Turn/MoveMade/GameOver） |
| rule.PositionHash / InsufficientMaterial | rule/draw.go:24 / 41 | 服务端重复局面哈希 / 子力不足判定 |
| Session.checkDraw 判和 | session/play.go:141 | 三次重复 → 子力不足 → 自然限着 |
| ChessOpeningBook.Lookup | opening_book.go:68 | 开局库查询 |
| positionKey | opening_book.go:93 | 精确局面键（90 格 + 执子方） |
| AnalyzeUserDecisions | user_analysis.go:25 | 真人走子离线复盘分析 |

---

**设计理念**: 普通人的上位水准 - 打造值得反复对战的学习伙伴  
**参考文档**: 
- [机器人系统总览](../bots.md)
- [斗地主机器人技术方案](ddz-bot.md)
- [五子棋机器人技术方案](gomoku-bot.md)
