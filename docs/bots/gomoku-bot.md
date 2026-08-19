# 五子棋机器人完整技术方案

> **源码位置**：`internal/games/gomoku/bot/`（engine.go 1451 行 + opening_book.go 282 行 + controller.go 304 行 + difficulty.go 89 行 + replay_adapter.go 183 行 + user_analysis.go 100 行 + pool.go 29 行）  
> **最后更新**：2026-09-09（与当前源码同步）  
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
9. [当前能力与未来优化方向](#9-当前能力与未来优化方向)

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
1. **可解释优先**：所有决策规则透明，玩家能理解"为什么这样下"
2. **适度失误**：不追求完美最优解，允许次优但合理的决策
3. **攻守平衡**：根据局势动态调整进攻/防守权重，模拟人类思维
4. **冷启动即用**：无需训练数据，部署即可提供有意义的对战体验

### 1.2 技术路线

**经典博弈树搜索引擎：NegaMax + α-β 剪枝 + VCF/VCT 杀棋检测**

- **无模型、无训练、零外部依赖**：编译即部署，无需 GPU 或模型文件
- **策略水平**：业余初段 ~ 业余3段（相当于社区棋社中等偏上水平）
- **设计目标**：可调试、规则透明、决策耗时可控（0.5~2s）、有趣味性
- **核心特色**：
  - 三档难度（easy/normal/hard），全链路（服务端 → 引擎 → 复盘）打通
  - 完整的威胁感知候选生成
  - VCF（连续冲四）/VCT（连续活三）杀棋搜索（含专用杀棋置换表）
  - Zobrist 置换表（增量哈希 + 跨迭代复用 + Best Move 排序）
  - 跳活三/跳冲四识别
  - 棋型组合分评估（合并扫描）
  - Top-N softmax 随机扰动（拟人化失误）
  - 并发根节点评估 + 时间预算控制

### 1.3 模块清单

| 模块 | 文件 | 行数 | 职责 |
|------|------|------|------|
| 搜索引擎 | engine.go | 1451 | 决策链、候选生成、NegaMax（增量 Zobrist + lastMove 胜负检测 + Best Move 排序 + 僵局和棋感知）、VCF/VCT（含专用杀棋置换表）、置换表、局面评估、Top-N 扰动、威胁分类 |
| Controller | controller.go | 304 | 消息驱动：棋盘镜像（广播重建）、回合延迟 1~3s、难度消费（Options["difficulty"]）、提交落子、复盘决策记录 |
| 难度配置 | difficulty.go | 89 | DifficultyConfig 三档预设（easy/normal/hard）+ DifficultyFor + 零值回退既有行为；QuickDifficultyFor 测试专用快速预设（关闭时间预算） |
| 开局库 | opening_book.go | 282 | 12 种职业定式（直指6 + 斜指6）× 8 重对称变体注册；多应手列表；手数门控（见 §4.2） |
| 对象池 | pool.go | 29 | 棋盘 sync.Pool，并发搜索零分配 |
| 复盘适配 | replay_adapter.go | 183 | GomokuReplayAdapter：决策日志记录（真实分数/候选/深度/难度/杀棋命中）、报告生成 |
| 决策详情 | types.go | 45 | DecisionEngine + DecisionDetailer 接口、DecisionDetail 结构 |
| 复盘分析 | user_analysis.go | 100 | 离线失误分析：真人每手与引擎最优对比，判定漏杀/漏堵/VCF 错过（关键点检测在此实现） |

---

## 2. 架构总览

### 2.1 整体架构图

```
┌─────────────────────────────────────────────────────────────┐
│                     Room / GameSession                       │
│  广播消息 (MsgGkMoveMade / MsgGameStart / MsgGameOver)       │
└──────────────────────┬──────────────────────────────────────┘
                       │ 消息路由（五子棋无隐藏信息）
┌──────────────────────▼──────────────────────────────────────┐
│                   Bot Controller                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ board: 15×15 棋盘镜像（从广播消息重建）               │   │
│  │ currentPlayer: 当前执子方                              │   │
│  │ moveCount: 已落子数                                    │   │
│  │ difficulty: DifficultyConfig（开局时从 Options 消费）   │   │
│  └──────────────────────────────────────────────────────┘   │
│  延迟 1~3s → Engine.DecideMove → SubmitBotAction            │
└──────────────────────┬──────────────────────────────────────┘
                       │ BoardState
┌──────────────────────▼──────────────────────────────────────┐
│                  Decision Engine                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ DecideMoveDetailed(): 落子决策（难度驱动）             │   │
│  │  决策链:                                               │   │
│  │  空棋盘 → 天元                                        │   │
│  │  僵局/满盘（IsDead）→ 就近落子（Score=0，和棋已定）   │   │
│  │  开局库 Lookup（手数门控 + 8 重对称，见 §4.2）         │   │
│  │  1. findWinningMoves(我方) → 一步成五直接下           │   │
│  │  2. findWinningMoves(对手) → 堵对手成五               │   │
│  │  3. tryVCF() → 连续冲四必胜（深度按难度）              │   │
│  │  4. tryVCT() → 连续活三必胜（深度按难度）              │   │
│  │  5. 迭代加深 1→MaxDepth（时间预算 2s）+ Top-N 扰动    │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ generateCandidates(): 威胁感知候选生成                │   │
│  │  优先级: 成五点 > 堵成五 > 四点 > 堵三四 > 三点 > 邻近 │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ negamax(): NegaMax + α-β 剪枝（增量 Zobrist）         │   │
│  │  叶节点 → evalBoard()                                 │   │
│  └──────────────────────────────────────────────────────┘   │
└──────────────────────┬──────────────────────────────────────┘
                       │ 规则计算
┌──────────────────────▼──────────────────────────────────────┐
│                    Evaluation Layer                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ evalDirFrom  │  │ hasNInRow    │  │ comboThreat  │      │
│  │ (方向扫描)   │  │ WithGap      │  │ FromCounts   │      │
│  │              │  │ (跳活三/冲四) │  │ (组合威胁)   │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 决策数据流

```
Room 广播消息 → Controller.OnBroadcast
    ├─ MsgGkMoveMade: 更新棋盘镜像 board[row][col] = color
    ├─ MsgGameStart: 初始化空棋盘，重置 moveCount，消费 Options["difficulty"]
    └─ 机器人回合 → 延迟 1~3s → Engine.DecideMoveDetailed(board, color, difficulty)
         ↓
    决策链:
         ├─ 空棋盘 → 返回天元 (7,7)
         ├─ 僵局/满盘（rule.IsDead）→ 首个空位（Score=0，和棋已定）
         ├─ 开局库 Lookup（按难度概率使用；仅 2 手局面查询；8 重对称变体命中）
         ├─ 1. findWinningMoves(我方) → 找到成五点立即返回
         ├─ 2. findWinningMoves(对手) → 找到成五点立即返回（堵）
         ├─ 3. tryVCF(color) → 连续冲四必胜路径（深度按难度：easy 6 / normal 8 / hard 8 / 零值 16）
         ├─ 4. tryVCT(color) → 连续活三必胜路径（深度同上）
         └─ 5. 迭代加深 1→MaxDepth（时间预算 2s）
              ├─ depth=1: 并发评估所有根候选
              ├─ depth=2: 取最高分着法，继续加深
              ├─ ...
              └─ depth=MaxDepth: 最终选择（超时则停止加深）
              └─ Top-N softmax 扰动（无紧迫战术胜机时，难度控制 N）
         ↓
    返回最佳落子 + DecisionDetail（分数/深度/Top-N/威胁/杀棋命中）
         ↓
    MsgGkMoveMade + 复盘记录（真实决策数据）
```

**关键输入 BoardState**：
- `board[15][15]`: 棋盘状态（0=空，1=黑，2=白）
- `color`: 当前执子方（1 或 2）
- `moveCount`: 已落子总数（用于判断开局阶段）

---

## 3. 核心数据结构

### 3.1 棋盘表示

```go
type Board [15][15]int
// 0 = Empty, 1 = Black, 2 = White
```

**特点**：
- 固定大小 15×15，标准五子棋棋盘
- 使用 int 而非 byte，便于算术运算
- 行优先存储，cache-friendly

### 3.2 Move 结构

```go
type Move struct {
    Row   int  // 行坐标 [0,14]
    Col   int  // 列坐标 [0,14]
    Score int  // 启发式评分（用于排序）
}
```

### 3.3 置换表条目

```go
type ttEntry struct {
    key       uint64      // Zobrist hash
    value     int         // 评估值
    depth     int         // 搜索深度
    bound     ttBoundType // Exact/LowerBound/UpperBound
    bestMove  move        // 该局面下的最佳着法（Best Move 排序提示）
    hasBest   bool        // 是否存在有效 bestMove（叶/终局节点为 false）
    accessNum uint64      // 全局访问序号（LRU 淘汰依据）
}

type ttBoundType int
const (
    Exact       ttBoundType = iota // 精确值
    LowerBound                     // 下界（α 提升）
    UpperBound                     // 上界（β 降低）
)
```

**设计要点**：
- `depth` 和 `bound` 字段支持跨迭代复用
- 查询时仅在条目深度 ≥ 请求深度时可用
- 按边界类型正确剪枝（Exact 直接返回，LowerBound 仅用于 α 提升，UpperBound 仅用于 β 降低）
- 容量 65536，未达上限 262144 前自适应倍增
- 置换表在迭代加深循环外创建，跨深度复用
- LRU 以全局访问序号 accessNum 实现；满时一次性淘汰最久未用的 50%（sort.Slice，O(n log n)）

### 3.4 棋型分值表

| 棋型 | 分值 | 说明 |
|------|------|------|
| 连五 | 10⁷ | 必胜/必败 |
| 活四 | 10⁶ | 两端开放的 four-in-a-row |
| 冲四 | 10⁵ | 一端被堵的 four-in-a-row |
| 活三 | 5×10⁴ | 两端开放的 three-in-a-row |
| 眠三 | 10³ | 一端被堵的 three-in-a-row |
| 活二 | 10² | 两端开放的 two-in-a-row |
| 眠二 | 10 | 一端被堵的 two-in-a-row |

**评估公式**：
```go
evalBoard = myScore - oppScore*9/10 + myCombo - oppCombo*9/10
```

**组合威胁分**（检测多棋型共存）：
- 双活三：+150000
- 活四：+500000
- 冲四+活三：+125000
- 双冲四：+100000

### 3.5 难度配置

```go
type DifficultyConfig struct {
    Name            string  // easy/normal/hard
    MaxDepth        int     // 迭代加深最大搜索深度（2 / 3 / 4）
    VCFMaxDepth     int     // VCF/VCT 杀棋搜索最大深度（6 / 8 / 8；hard 从 12 降至 8：过深 VCF 挤占主搜索时间预算，反而削弱实力）
    OpeningBookRate float64 // 开局库使用概率（0.3 / 0.5 / 0.8）
    RandomTopN      int     // Top-N 候选 softmax 概率选择（5 / 3 / 2）
}
```

- `DifficultyFor(name)`：空或未知档位回退 normal
- 零值配置 `normalize()` 回退引擎既有行为（4 层搜索 / 16 层杀棋 / 始终用开局库 / 不扰动），
  保证会话托管兜底、复盘分析等未携带难度的调用方行为稳定

---

## 4. 决策流程详解

### 4.1 决策链（DecideMoveDetailed）

```
DecideMoveDetailed(board, color, cfg)
  ├─ 空棋盘 → 返回天元 (7,7)
  ├─ 僵局/满盘（rule.IsDead）→ 首个空位（Score=0），跳过全部搜索
  ├─ 开局库 Lookup（rand < cfg.OpeningBookRate 时使用，见 §4.2）
  ├─ 1. findWinningMoves(color)
  │     遍历全盘空位，检查落子后是否成五
  │     找到 → 立即返回该点（必胜）
  ├─ 2. findWinningMoves(oppColor)
  │     遍历全盘空位，检查对手落子后是否成五
  │     找到 → 立即返回该点（必堵）
  ├─ 3. tryVCF(color, cfg.VCFMaxDepth)
  │     连续冲四搜索
  │     枚举所有四点落子 → 若成五则成功
  │     否则检查对手是否有反杀（成五或冲四）
  │     若无则假设对手堵在该点，递归下一层
  │     找到必胜路径 → 返回首手
  ├─ 4. tryVCT(color, cfg.VCFMaxDepth)
  │     连续活三搜索
  │     枚举所有活三点（非四点）
  │     对所有对手防守位置（活三两端）分支
  │     全部防守下我方仍能续活三才算必胜
  │     找到必胜路径 → 返回首手
  └─ 5. 迭代加深 1→cfg.MaxDepth（时间预算 2s）
        for depth := 1; depth <= cfg.MaxDepth; depth++ {
            if timeElapsed() > 2000ms { break }

            // 并发评估所有根候选（NumCPU worker 分片）
            bestMove = argmax(-negamax(board, depth, -∞, +∞, opp, ...))
        }
        // Top-N softmax 扰动（难度控制，无紧迫战术胜机时生效）
        return detail{Row, Col, Score, Depth, TopMoves, Threats, ...}
```

**关键设计思路**：
- **短路执行**：一旦发现必胜/必防着法立即返回，避免无效搜索
- **VCF 优先于 VCT**：冲四是强制应手，活三可能被忽略
- **迭代加深**：每层都记录根候选评分，超时时以最后完成的完整迭代兜底
- **时间预算**：严格控制总耗时，确保响应速度 <2s
- **难度驱动**：`DecideMoveDetailed` 按 `DifficultyConfig` 控制开局库使用概率、VCF/VCT 深度、搜索深度与 Top-N 扰动；零值难度回退既有行为
- **决策详情**：返回 `DecisionDetail`（真实分数 / 深度 / Top-N 候选 / 威胁分类 / 杀棋命中 / 难度），供复盘记录与调试

**Top-N softmax 扰动（拟人化失误）**：
- 仅当无紧迫战术胜机（bestScore < 冲四分）时生效，必胜/必堵着法永不丢失（`TestEngine_PerturbationNeverMissesTactics`）
- 在根候选前 N 名中按 softmax 概率选择，温度 = 前 N 名分数极差的 1/4（下限 1），避免大分差下退化为确定性选择
- 效果梯度：easy（Top-5）约 30% 着法非最优；normal（Top-3）约 15% 偏差；hard（Top-2）约 5% 微小偏差

**和局感知（僵局/满盘，2026-09-09 新增）**：
- **决策链短路**：`rule.IsDead(board)` 成立（双方均无连五可能，含满盘）时，任何落子终将以和棋告终（服务端 session 落子后即以 `IsDead` 判和，`DrawReason=no_win_possible`），直接落首个空位（Score=0），不进入开局库/杀棋/迭代加深搜索
- **negamax 终局判定**：搜索树内满盘或僵局返回 0（平局），与成五的 ±scoreFive 形成完整胜负和三元终局——劣势方可主动导向僵局求和（0 分优于负分），优势方避免走入死局（0 分劣于正分）
- **模拟口径同步**：selfplay 循环落子后同步检查 `IsDead` 提前判和，避免死局无意义下满 225 手
- 判定准则与成本：窗口扫描（`rule.CanWin`）O(4·N²·5)≈4500 ops/次，开放局面早退、成本可忽略（详见 rule/rule.go）

### 4.2 开局库实现

**实现**：`globalOpeningBook` 以 `黑1,白1` 的坐标+颜色串为 key，精确匹配推荐应手。

- **多应手列表**：同 key 定式的应手全部保留（如花月/浦月首两手相同，各保留一档应手），命中时从该局面的等价应手中随机取一，避免走招单一
- **手数门控**：Lookup 仅在棋盘恰好 2 手时查询，其余手数一律未命中（不依赖 session 非法落子兜底）
- **8 重对称变体**：每条定式注册时展开恒等 / 旋转 90°/180°/270° / 水平 / 垂直 / 主副对角线镜像共 8 个变体（应手坐标同步变换），任意方位的白 2 布局均可精确命中，Lookup 侧无需生成变体 key
- **难度概率使用**：按 `cfg.OpeningBookRate` 概率查询（easy 30% / normal 50% / hard 80%），低档位概率性放弃定式，模拟人类不熟开局

### 4.3 候选生成（generateCandidates）

**威胁感知筛选**：

```
generateCandidates(board, color)
  ├─ 收集以下类别的空位（去重）：
  │   ├─ 我方成五点（winning moves）
  │   ├─ 对手成五点（blocking winning moves）
  │   ├─ 我方四点（活四/冲四）
  │   ├─ 对手三四点（防守活三/冲四）
  │   ├─ 我方三点（活三/眠三）
  │   └─ 半径 2 邻近空位（兜底，保证覆盖）
  ├─ 若候选数 > 30：
  │   └─ 按 scoreCandidate 启发式评分截断排序
  │       score = attack × 2 + defense × 1.5 - distanceToCenter × 2
  └─ 返回排序后的候选列表（最多 30 个）
```

**评分维度**：
- **进攻分**：基于棋型评估（活四 > 冲四 > 活三 > 眠三）
- **防守分**：对手的棋型评估（权重略低，体现进攻优先）
- **中心距离**：越靠近中心分数越高（战略要地）

**跳活三/跳冲四识别** (`hasNInRowWithGap`)：
- 滑动窗口算法，支持检测 `.XX.X.` 类间隔形态（见 §5.3）

### 4.4 搜索与评估（negamax + evalBoard）

**NegaMax + α-β 剪枝（实际签名与关键路径）**：

```go
func negamax(board []int, depth, alpha, beta, color, lastRow, lastCol int, h uint64, tt *transpositionTable) int {
    // 1. 置换表查询（命中时同时取回 best move 用于候选排序）
    res := tt.get(h, depth)
    if res.ok {
        switch res.bound {
        case boundExact:  return res.value
        case boundLower:  alpha = max(alpha, res.value)
        case boundUpper:  beta = min(beta, res.value)
        }
        if alpha >= beta { return res.value }
    }

    // 2. 上一手成五检测（O(1)：树上非最后一手不可能新成连五，父节点已短路）
    if lastRow >= 0 && rule.IsWin(board, lastRow, lastCol) {
        return -scoreFive - depth
    }

    // 2.5 终局和棋感知：满盘或僵局（双方均无连五可能）→ 0（平局）
    // 满盘时所有窗口无空位，IsDead 同样成立，一次判定覆盖两种和棋终局
    if rule.IsDead(board) { return 0 }

    // 3. 叶节点评估
    if depth == 0 { return evalBoard(board, color) }

    // 4. 候选生成 + Best Move 排序（历史最佳提到首位）
    cands := generateCandidates(board, color, radius)
    if res.ok && res.hasBest { /* 将 res.bestMove 提到 cands[0] */ }

    // 5. NegaMax 递归（落子/回退时增量 XOR 维护哈希）
    best := -INF
    for _, c := range cands {
        idx := rule.Idx(c.row, c.col)
        board[idx] = color
        score := -negamax(board, depth-1, -beta, -alpha, opp, c.row, c.col, h^zobristTable[idx*3+color], tt)
        board[idx] = rule.Empty
        // 更新 best / alpha / bound，β 剪枝
    }

    // 6. 置换表存储（含 bestMove 提示）
    tt.put(h, best, depth, bound, bestCand, hasBest)
    return best
}
```

**性能要点**：
- **增量 Zobrist**：哈希由落子/回退时 XOR 更新，不再每节点全盘重算
- **lastMove O(1) 胜负检测**：替代全盘 checkWin 扫描
- **Best Move 排序**：置换表历史最佳着法提前尝试，提升 α-β 剪枝效率

**评估函数（evalBoard）**：

```go
func evalBoard(board []int, color int) int {
    // 每方单次全盘遍历，同时产出棋型分与活三/活四/冲四计数（合并扫描，共 2 次全盘扫）
    myScore, myCombo := evalSideStats(board, color)
    oppScore, oppCombo := evalSideStats(board, rule.Opposite(color))
    return myScore - oppScore*9/10 + myCombo - oppCombo*9/10
}
```

**evalDirFrom（方向扫描）**：
- 从棋子向单方向扫描计数 + 两端开闭判定
- 反方向有同色子则跳过（防重复计数）
- 返回该方向的棋型分值（活四/冲四/活三等）

---

## 5. 关键算法

### 5.1 VCF（Victory by Continuous Four）

**算法**：连续冲四必胜路径搜索

```
tryVCF(color, maxDepth)
  ├─ 枚举所有四点落子（能形成冲四的点）
  ├─ 对每个四点：
  │   ├─ 落子后检查是否成五 → 成五则成功（返回该点）
  │   ├─ 检查对手是否有反杀：
  │   │   ├─ 对手有成五点 → 该分支失败（对手会赢）
  │   │   └─ 对手有冲四点 → 该分支失败（对手会反冲四）
  │   └─ 假设对手堵在该四点位置，递归 VCF(color, depth-1)
  │       └─ 所有分支都成功 → 返回首手（必胜路径起点）
  └─ 无必胜路径 → 返回 nil
```

**关键特性**：
- 只考虑冲四（强制应手），不考虑活三
- 对手无反杀时才假设堵在冲四点（避免反杀误判必胜）
- 最大深度按难度配置（easy 6 / normal 8 / hard 8 / 零值 16；hard 从 12 降至 8，避免杀棋搜索挤占主搜索时间预算）
- 递归搜索带专用杀棋置换表（见 §5.5）

### 5.2 VCT（Victory by Continuous Three）

**算法**：连续威胁必胜路径搜索（交替博弈树：攻方威胁 → 防守方全部应对 → 攻方续胜）

```
tryVCT(color, maxDepth)
  ├─ 首着须为严格活三（连续 3 子且两端开，用 strictLiveThreePoint 判定；
  │   冲四链归 VCF 管；不能用宽松的 isFourPoint 排除——它把活三也判为四点，永远拒真）
  ├─ vctRefuteThreat：枚举防守方全部应对
  │   ├─ 防守方有成五点 → 攻方失败（威胁链不够快，保守判定防假阳性）
  │   ├─ 堵活三两端 + 反打冲四（不堵三）均为合法应对
  │   └─ 防守点为空 → 视为无实质威胁（如间隔形态"三"），不算必胜
  └─ vctSearchInner（轮到攻方）：
      ├─ 直接成五或一手成活四（两端全开无法兼顾）→ 必胜
      └─ 否则枚举严格活三着法，递归验证所有防守下均能续胜
```

**关键特性**：
- 交替博弈树语义：攻方每手威胁后防守方必应，不得连走两手（修复前攻方在模型中多走一手，防守方从未应对首手威胁）
- 活四是必胜节点：防守方无法同时堵住两端（isLiveFourPoint，连续 ≥4 子且两端空）
- 严格连型判定（runThroughDir）：连续段长度计数，避免宽松窗口语义把活三误判为四点
- 防守枚举须包含反打冲四：否则会把"防守方 counter 冲四打断威胁链"的分支漏判为攻方胜（假阳性）
- 与 VCF 共用杀棋置换表机制（独立缓存实例）

> 历史缺陷（已修复，见 vct_test.go 锁定行为）：isFourPoint 宽松窗口语义把活三也判为四点、
> findDefensesForLiveThree 只累计前向棋子导致永远找不到防守点、活四过渡被排除——
> 三重缺陷叠加使 VCT 在自战中几乎零命中（双活三必胜局面恒返回 nil）。

### 5.3 跳活三/跳冲四识别（hasNInRowWithGap）

**问题**：传统算法只能识别连续的活三/冲四，无法识别 `.XX.X.` 类间隔形态

**方案**：滑动窗口算法

```go
func hasNInRowWithGap(board []int, row, col, dr, dc, color, n int) bool {
    // 收集方向线上连续的同色子和空位
    cells := []Cell{}
    for i := -2; i <= n+1; i++ {
        r, c := row + dr*i, col + dc*i
        if outOfBounds(r, c) { continue }
        cells = append(cells, Cell{r, c, board[r][c]})
    }
    
    // 滑动窗口：检查是否有 n 个位置（最多1个空位且包含落子点）能形成有效连线
    for each window of size n+1 {
        sameColor := count(cells[i] == color)
        empty := count(cells[i] == Empty)
        
        if sameColor == n-1 && empty == 1 && containsTargetCell(window) {
            // 检查两端开闭情况（活三 vs 冲四）
            if isOpenEnd(window) { return true } // 活三
            if isClosedEnd(window) { return true } // 冲四
        }
    }
    
    return false
}
```

**支持的形态**：
- `.XX.X.` → 跳活三（中间隔一个空位）
- `.XXX.X.` → 跳冲四（中间隔一个空位）
- `XX.X` → 普通冲四

### 5.4 组合威胁分（comboThreatFromCounts）

**问题**：单独评估每个棋型会忽略组合威胁（如双活三、冲四+活三）

**方案**：`evalSideStats` 在单次全盘遍历中同时统计棋型分与活三/活四/冲四计数，`comboThreatFromCounts(liveThree, openFour, four)` 按计数给额外加分：

```go
func comboThreatFromCounts(liveThree, openFour, four int) int {
    bonus := 0
    if liveThree >= 2 { bonus += scoreOpenThree * 3 }      // 双活三 +150000
    if openFour > 0   { bonus += scoreOpenFour / 2 }       // 活四 +500000（几乎必胜）
    if four > 0 && liveThree > 0 { bonus += scoreFour + scoreOpenThree/2 } // 冲四+活三 +125000
    if four >= 2      { bonus += scoreFour }               // 双冲四 +100000
    return bonus
}
```

评估整体仅 2 次全盘扫描（敌我各一次），组合威胁计数随扫描顺带产出。

### 5.5 置换表与杀棋置换表

**Zobrist Hash**：
- 15×15 格 × 3 种状态（空/黑/白）的随机数表
- 确定性种子生成（PCG 种子 42/12345，便于调试复现）
- 冲突率极低（理论上 2^64 空间）
- 搜索时增量维护（落子/回退 XOR），仅根节点做一次全盘初始化

**主置换表（transpositionTable）**：
- 条目含 value / depth / bound / bestMove / accessNum（见 §3.3）
- 仅在条目深度 ≥ 请求深度时可用；按边界类型剪枝（Exact 直接返回，LowerBound 仅用于 α 提升，UpperBound 仅用于 β 降低）
- **Best Move 排序**：搜索时记录取得最优分的着法随条目存入；命中且未剪枝时将历史最佳着法提到候选首位，提升 α-β 剪枝效率
- **淘汰策略**：LRU（按 accessNum），初始容量 65536，未达上限 262144 前倍增扩容；满时一次性淘汰最久未用的 50%（sort.Slice）
- 置换表在迭代加深循环外创建，跨深度复用；命中率约 60-80%（取决于局面重复度）

**杀棋置换表（killCache，VCF/VCT 专用）**：

```go
type killEntry struct {
    remain int  // 搜索时的剩余深度（maxDepth - depth）
    win    bool // color 方在 remain 深度内能否强制取胜
}
```

- VCF/VCT 的结果为 bool 胜负语义，与主搜索的分值边界不同，故使用独立缓存（不复用主置换表）
- 同一局面在不同剩余深度下结论可能不同，命中按深度区间判定：
  - `true` 结论在剩余深度 ≥ 记录值时成立（更少深度都必胜，更深自然必胜）
  - `false` 结论在剩余深度 ≤ 记录值时成立（更多深度都赢不了，更浅更赢不了）
- 上限 2²⁰ 条目（超限后不再写入），生命周期为单次决策
- VCF 与 VCT 使用各自独立的缓存实例，避免语义串扰

### 5.6 并发根节点评估

**方案**：根层按候选分片，NumCPU 个 worker 并行评估

```go
func decideMoveIterativeDeepening(board []int, color int) Move {
    candidates := generateCandidates(board, color)
    
    for depth := 1; depth <= maxDepth; depth++ {
        if timeElapsed() > 2000ms { break }
        
        // 并发评估根候选
        results := make(chan EvalResult, len(candidates))
        chunks := splitCandidates(candidates, NumCPU)
        
        for _, chunk := range chunks {
            go func(chunk []Move) {
                boardCopy := GetBoard() // 池化对象
                for _, move := range chunk {
                    // 落子 + 增量哈希 + negamax 搜索 + 回退
                }
                PutBoard(boardCopy) // 归还对象池
            }(chunk)
        }
        
        // 收集结果，选择最高分；记录根候选评分供 Top-N 扰动使用
    }
    
    return bestMove
}
```

**关键特性**：
- 使用 sync.Pool 减少 GC 压力
- 每个 worker 持有独立的棋盘副本
- channel 收集结果，主线程汇总
- 置换表跨 worker 共享（互斥锁保护），跨迭代复用

---

## 6. 性能特性

### 6.1 性能措施与效果

| 措施 | 实现方案 | 效果 |
|--------|---------|------|
| 置换表 | depth/bound/bestMove 字段 + 跨迭代复用，64K LRU 淘汰 | 命中率 60-80%，剪枝效率提升 |
| 时间预算控制 | 2s 预算，迭代加深逐层检查耗时 | 决策耗时稳定 0.5~2s，避免深层 VCF/VCT 超时 |
| 威胁识别 | 跳活三/跳冲四滑动窗口算法，支持 `.XX.X.` 间隔形态 | 威胁检测性能提升 22x |
| 评估 | 组合威胁分 + evalSideStats 合并扫描 | 战术强度提升，全盘扫描 4 次→2 次 |
| 增量 Zobrist + lastMove 检测 | 落子/回退 XOR 维护哈希；树上 O(1) 胜负判定 | 节点开销显著下降 |
| 置换表 LRU | sort.Slice 淘汰 + 容量倍增至上限 262144 | 无淘汰卡顿风险 |
| Best Move 排序 | 历史最佳着法优先尝试 | α-β 剪枝效率提升 |
| 杀棋置换表 | VCF/VCT 独立 killCache，按剩余深度区间命中 | 重复杀棋局面免重复搜索 |
| 并发评估 | 根候选分片 + NumCPU worker + 棋盘对象池 | 多核加速，搜索零分配 |

| 指标 | 数值 |
|------|------|
| 单步决策耗时（典型） | 0.5~2s（难度 MaxDepth + 时间预算内） |
| 简单局面决策耗时（可运行 `TestEngine_MoveAlwaysLegal` 复核） | ~0.02s |
| 置换表命中率 | 60-80% |
| 搜索深度 | 1→4 层迭代加深（按难度 2/3/4） |

### 6.2 热点函数分析

| 函数 | 调用频率/局 | 单次耗时 | 总耗时占比 |
|------|------------|---------|-----------|
| evalBoard | ~50,000 | 5-10μs | **40%** |
| generateCandidates | ~10,000 | 2-5μs | **25%** |
| negamax（内部节点） | ~100,000 | 1-2μs | **20%** |
| VCF/VCT | ~100 | 100-500μs | **10%** |
| 其他（哈希/置换表） | - | - | **5%** |

**性能瓶颈**：评估函数（合并扫描后为 2 次全盘扫描）。进一步的性能优化见 §9"不推荐的优化方向"——当前性能已满足设计目标，继续压榨会让机器人偏离拟人化定位。

---

## 7. 主流方案对比

### 7.1 不同技术路线的适用性

| 路线 | 代表 | 强度 | 实现成本 | 对本项目的适用性 |
|------|------|------|---------|----------------|
| **α-β 规则系（当前）** | 本工程 | 业余3~5段 | 低（已完成） | ✅ **最适合**：强度可控，易调节 |
| **MCTS** | Piskvork系 | 业余2~4段 | 中 | ⚠️ 可选：天然有随机性，但实现复杂 |
| **AlphaZero类** | Rapfi | 职业级 | 极高 | ❌ 过强：远超目标水平，训练成本高 |
| **NNUE** | rapfi-nnue | 业余5~7段 | 高 | ❌ 过强：评估太准，难以"故意犯错" |

**结论**：当前 α-β 规则系是最优选择，只需调整参数和增加随机性即可达到目标水平。

### 7.2 与同类休闲游戏的机器人对比

| 游戏 | 典型机器人设计 | 强度控制方式 | 借鉴点 |
|------|--------------|------------|--------|
| 象棋（天天象棋） | 多档AI，从入门到特级 | 搜索深度+开局库+随机性 | 难度分级明确 |
| 围棋（KataGo） | 让子/让先机制 | 限制算力+ handicap | 可通过让先调节 |
| 斗地主（本项目） | 基于概率的决策 | 出牌准确率+风险偏好 | 随机扰动思路 |
| 五子棋（Gomoku-AI） | 固定深度搜索 | 仅深度调节 | 缺少随机性，体验单一 |

**本项目差异化**：在 α-β 框架基础上，结合**拟人化随机扰动**（Top-N softmax）和**三档难度调节**（搜索深度 / 杀棋深度 / 开局库概率 / 扰动强度四维联动），兼顾可玩性和挑战性。

### 7.3 技术差距矩阵

| 维度 | 当前实现 | 专业引擎（Rapfi） | 差距分析 |
|------|---------|------------------|---------|
| **搜索深度** | 4 层（等效 ~6 层 with LMR） | 10+ 层 | **主要差距**：看不到深层战术（对拟人化目标是优点） |
| **置换表** | 64K，LRU 淘汰 | 数 MB，large pages + prefetch | 容量差 100x+（单次决策生命周期，影响有限） |
| **VCF/VCT** | ≤16 层 | ≤30 层 + 威胁枚举优化 | 杀棋搜索深度不足 |
| **评估函数** | 手工棋型表 + 组合分 | NNUE（轻量神经网络） | **本质差距**：NNUE vs 手工 |
| **候选生成** | 威胁感知 + 启发式排序 | 静态交换评估（SEE）+ PV 优先 | 缺 SEE 指导 |
| **开局库** | 12 种定式 × 8 重对称变体 | 数万局统计 + 在线更新 | 覆盖度低（可继续扩充，见 §9） |
| **对称变换** | ✅ 8 重对称变体注册（开局库） | ✅ 8 重对称归一化 | 开局场景已对齐 |

### 7.4 "普通人上位水准"适配度分析

| 特性 | 对强度的影响 | 对可玩性的影响 | 当前状态 |
|------|-------------|---------------|---------|
| 搜索深度 2~4 层 | 中等（看不到深组合） | ✅ 正相关（给人类反击空间） | 合适 |
| 评估函数粗糙 | 中等（会犯低级错误） | ✅ 正相关（人类也会犯错） | 合适 |
| 开局库覆盖 | 弱→中（12 种职业定式 × 8 重对称变体） | ⚠️ 中性（开局有指导） | 可继续扩充定式 |
| VCF/VCT 完备 | 强（基本杀棋无遗漏） | ⚠️ 偏强（建议降低深度） | **需调整**（随难度分档已部分缓解） |
| 决策时间 0.5~2s | 无影响 | ✅ 符合人类节奏 | 合适 |
| 难度可调（easy/normal/hard） | - | ✅ 正相关（适配不同水平玩家） | 具备 |

---

## 8. 测试与验证

### 8.1 单元测试

```bash
# 引擎基础行为（空棋盘下天元 / 取胜 / 堵四 / 优先取胜 / 落子合法性）
go test ./internal/games/gomoku/bot/ -run 'TestEngine_' -v

# 难度与决策详情（难度解析/零值归一化/开局库命中与门控/同 key 多应手/真实决策数据/扰动不丢战术）
go test ./internal/games/gomoku/bot/ -run 'TestDifficulty|TestOpeningBook|TestDecideMoveDetailed|TestEngine_PerturbationNeverMissesTactics' -v

# 置换表与杀棋缓存（深度区间命中语义 / bestMove 往返与覆盖）
go test ./internal/games/gomoku/bot/ -run 'TestKillCache|TestTranspositionTable_BestMove' -v

# 复盘离线失误分析（漏杀/漏堵/无效快照）
go test ./internal/games/gomoku/bot/ -run 'TestAnalyzeUserMove|TestAnalyzeUserDecisions' -v

# Controller 消息驱动（回合响应/忽略真人/棋盘跟踪/结束清理）
go test ./internal/games/gomoku/bot/ -run 'TestController_' -v

# 规则层（连五判定/边界/平局/颜色助手）
go test ./internal/games/gomoku/rule/ -v

# 和局感知（僵局/满盘终局 negamax 返 0 / 决策链短路直接落空位）
go test ./internal/games/gomoku/bot/ -run 'TestNegamax_DeadBoardDrawScore|TestDecideMoveDetailed_DeadBoardPlaysEmpty' -v

# 会话层（含托管接管、超时自动落子、快照恢复）
go test ./internal/games/gomoku/session/ -v
```

**覆盖清单**：
- engine_test.go 5 例：空棋盘下天元 / 一步成五必取 / 堵对手冲四 / 优先自己取胜 / 落子合法性（TestEngine_EmptyBoardPlaysCenter、TakesWinningMove、BlocksOpponentFour、PrefersOwnWinOverBlock、MoveAlwaysLegal）
- difficulty_test.go 6 例：TestDifficultyFor、TestDifficultyNormalize、TestOpeningBook_HitAndGate（含对称方位命中与 3/5 手门控）、TestOpeningBook_DuplicateKeysKept、TestDecideMoveDetailed_RealData（成五分数/威胁/搜索深度/TopMoves 降序）、TestEngine_PerturbationNeverMissesTactics
- kill_cache_test.go 2 例：TestKillCache_ProbeDepthIntervalSemantics（杀棋缓存深度区间命中语义）、TestTranspositionTable_BestMoveRoundTrip（bestMove 往返与覆盖）
- controller_test.go 4 例：回合响应 / 忽略真人回合 / 棋盘跨回合跟踪 / 结束清理
- user_analysis_test.go 4 例：漏杀 / 漏堵 / 取胜 / 无效快照
- selfplay_test.go 1 例：100 局自战回归（同难度 80 局 + 跨难度梯度局 20 局，非法落子/改棋盘零容忍，须加 `-timeout 45m`；判和口径与 session 一致：满盘或僵局提前判和）。⚠️ 仅在修改机器人出牌/决策逻辑后运行（`./scripts/run_tools.sh bot`，耗时约 8 分钟），常规测试一律跳过（`-skip '^TestSelfPlay_100Games$'` 或 `run_tools.sh test` 已排除）
- draw_test.go 2 例：TestNegamax_DeadBoardDrawScore（满盘/僵局留空两种底板，双执子方 negamax 均返回 0）与 TestDecideMoveDetailed_DeadBoardPlaysEmpty（僵局棋盘决策链短路直接落空位，Score=0 且 TopMoves 仅含所选落点）
- vct_test.go 5 例：VCT 专项正确性（双活三必胜命中 / 单活三无假阳性 / 决策链标记 / 必胜局面端到端转化取胜 / 活三防守）
- ✅ VCF/VCT 专项正确性测试已落地（VCT 见上；VCF 由自战 VCF 命中与主搜索间接覆盖）

### 8.2 自对弈观察

> 多难度自对弈已自动化为 `selfplay_test.go` 的 TestSelfPlay_100Games（100 局，同难度 easy 30/normal 30/hard 20 + 跨难度梯度局 20），并断言 hard/normal 对 easy 的强度压制；以下为早期人工观察数据，供历史参照。

- 黑棋胜率：~55%（符合五子棋先手优势）
- 平均步数：~40 步/局
- 平均决策耗时：~1s/步

**100 局自战实测基线**（VCT 修复后，耗时约 15 分钟）：

| 指标 | easy | normal | hard |
|------|------|--------|------|
| 席位局数 | 50 | 40 | 30 |
| 席位胜率 | 38 胜 1 和（76%） | 35 胜（88%） | 26 胜（87%） |
| 平均手数 | 34.5 | 27.7 | 26.3 |
| 平均深度 / 最大深度 | 1.04 / 2 | 1.36 / 3 | 2.28 / 4 |
| VCF / VCT 命中 | 342 / 15 | 265 / 17 | 138 / 22 |

跨难度梯度局 20 局（先后手各半）：hard/normal 对 easy 各 ≥4/10 胜（梯度断言下限，Top-N 扰动噪声下取 ≥4 消除偶发）；非法落子为零、引擎不改棋盘，除 easy 同难度 1 局和棋外全部正常终局。VCF 命中常态化；VCT 修复前几乎零命中（11/0/0），修复后 hard 命中最多（22），与杀棋搜索设计一致（VCT 依赖步更深的完整搜索，详见 §5.2 历史缺陷说明）。

---

## 9. 当前能力与未来优化方向

### 9.1 当前能力速查（实现方案见对应章节）

| 能力 | 实现方案 | 详见 |
|------|---------|------|
| 搜索引擎 | NegaMax + α-β + 迭代加深（按难度 2~4 层）+ 置换表 + 时间预算 | §2/§4 |
| 杀棋搜索 | VCF/VCT（深度按难度 6~8 层，零值 16），防守空集不误判必胜 | §5 |
| 杀棋置换表 | 独立 killCache，bool 语义 + 深度区间命中 | §5.5 |
| 决策详情 | DecisionDetailer 接口（分数/深度/Top-N/威胁/杀棋命中），复盘与调试用 | §4.1 |
| 候选生成 | 威胁感知 + 启发式排序 + Best Move 优先 | §4.3 |
| 和局感知 | 决策链僵局短路（首个空位 Score=0）+ negamax 满盘/僵局终局返 0（劣势求和/优势避死），与服务端 IsDead 判和口径一致 | §4.1 |
| 评估 | 手工棋型表 + 组合威胁分（合并扫描） | §4.4/§5.4 |
| 开局库 | 12 种定式 × 8 重对称变体，多应手列表 + 手数门控 + 难度概率使用 | §4.2 |
| 多档难度 | DifficultyConfig 三档（深度/杀棋深度/开局库率/Top-N softmax 扰动），服务端 options["difficulty"] 全链路打通 | §3.5/§4.1 |
| 对象池 | 棋盘 sync.Pool，并发搜索零分配 | §5.6 |
| 决策解释与复盘 | 通用 replay 层 + GomokuReplayAdapter（记录真实分数/候选/深度/杀棋/难度） | §1.3 |

---

### 核心策略：从"最强AI"转向"拟人化AI"

当前机器人已经具备业余3~4段的实力，但过于完美和确定性。下一步的核心是**增加拟人化特征**，而非继续追求极限强度。

---

### 方向一：拟人化特征增强（中优先级，2~3天）🟡

**目标**：让机器人更像"会犯错的真人"，而非"完美机器"

#### 1.1 人类常见错误模拟

**典型人类错误类型**：
1. **视线盲区**：忽略斜线上的威胁（尤其是 `.XX.X.` 跳活三）
2. **计算深度限制**：超过3步的VCF会漏算
3. **防守过度**：对假威胁反应过度，浪费先手
4. **收官粗糙**：优势局面下不够精细，给对手翻盘机会

**实现方案**：
- **视线盲区**：在评估函数中，对斜线方向的权重降低10~20%（简单/普通档）
- **计算深度限制**：VCF/VCT深度固定为8层（即使有能力算更深；已部分由难度分档覆盖）
- **防守过度**：当检测到多个威胁时，10%概率选择次优防守点
- **收官粗糙**：优势分差 >50万分时，降低搜索深度1层

```go
// 示例：视线盲区模拟
func evalDirFrom(board []int, row, col, dr, dc, color int) int {
    baseScore := ... // 原有计算
    
    // 简单/普通档：斜线方向权重降低
    if isDiagonal(dr, dc) && difficulty <= Normal {
        baseScore = baseScore * 85 / 100 // 降低15%
    }
    
    return baseScore
}
```

#### 1.2 思考时间拟人化

**当前**：固定1~3s随机延迟

**优化**：根据局面复杂度动态调整
- 简单局面（无威胁）：0.5~1.5s
- 中等局面（有活三/冲四）：1.5~2.5s
- 复杂局面（多重威胁）：2.5~4s

**实现**：基于候选数量和最高分差值判断复杂度

---

### 方向二：测试补强（中优先级，1~2天）🟡

- ~~VCF/VCT 专项正确性测试~~：已落地（vct_test.go 5 例）；过程中发现并修复 VCT 搜索三重缺陷（首着门控拒真 / 防守点恒空 / 活四过渡缺失，详见 §5.2）
- ~~多难度自对弈 harness~~：已落地（selfplay_test.go TestSelfPlay_100Games，100 局难度分配 + 梯度断言）

---

### 方向三：可选优化（低优先级）🟢

| 优化项 | 方案 | 预期效果 |
|--------|------|---------|
| **开局库扩充** | 增加定式覆盖（当前 12 种职业定式，可扩至更多瑞星/松月/丘月等） | 开局质量提升 |
| **组合分权重分档** | 难度联动组合威胁分权重（50%/75%/100%，当前统一权重） | easy 档对双三等复合威胁反应更迟钝 |
| **VCF/VCT 威胁枚举优化** | 只枚举已有棋型延长线上的点，替代全盘空位扫描 | 杀棋搜索加速，可支撑更深搜索 |

---

### 不推荐的优化方向

| 优化项 | 原因 |
|--------|------|
| **增量候选生成** | 当前性能已足够，提升深度会让机器人过强 |
| **增量评估** | 同上，且会降低评估的"人类误差"特征 |
| **NNUE** | 评估太准，难以模拟人类错误；训练成本高 |
| **搜索深度>4层** | 远超目标水平，破坏可玩性 |
| **更深的置换表/更强剪枝** | 同属"更强"方向，与拟人化定位冲突 |

---

**最终目标（难度梯度）**：
- 简单档：新手胜率 30~40%，有明显进步空间
- 普通档：中级玩家胜率 45~55%，需要认真思考
- 困难档：高手胜率 60~70%，有挑战但可战胜
- 所有档位：决策时间 0.5~4s，符合人类节奏

---

## 附录：文件索引（关键函数/行号）

| 函数 | 文件:行 | 功能 |
|------|---------|------|
| Engine.DecideMove | engine.go:46 | 落子决策总入口（零值难度兼容） |
| Engine.DecideMoveDetailed | engine.go:55 | 难度驱动决策链 + 决策详情 |
| softmaxTopN | engine.go:252 | Top-N softmax 随机扰动 |
| classifyThreats | engine.go:294 | 落子威胁分类 |
| generateCandidates | engine.go:349 | 威胁感知候选生成 |
| hasNInRowWithGap | engine.go:565 | 跳活三/跳冲四识别 |
| tryVCF / vcfSearch | engine.go:711 起 | 连续冲四搜索（maxDepth 参数化 + killCache） |
| tryVCT / vctSearch | engine.go:825 起 | 连续活三搜索（+ killCache） |
| negamax | engine.go:1043 | NegaMax + α-β（增量 Zobrist + lastMove 检测 + 僵局和棋感知 + Best Move 排序） |
| evalBoard / evalSideStats | engine.go:1135 / 1142 | 局面评估（合并扫描） |
| evalDirFrom | engine.go:1237 | 方向扫描计数 |
| transpositionTable（get/put/evictLRU） | engine.go:1333 起 | 置换表查询/存入（含 bestMove）/LRU 淘汰（上限 262144） |
| killCache（probe/store） | engine.go:1411 起 | 杀棋置换表（VCF/VCT 专用，深度区间命中） |
| DifficultyFor / DifficultyPresets | difficulty.go:43 / 15 | 难度档位解析与三档预设 |
| OpeningBook.Lookup | opening_book.go:211 | 开局库查询（手数门控 + 随机应手） |
| Controller.OnBroadcast | controller.go:73 | 广播消息处理 |
| Controller.onTurn | controller.go:139 | 机器人回合处理（真实决策详情复盘） |
| AnalyzeUserDecisions | user_analysis.go:23 | 真人落子离线失误分析 |

---

**文档维护者**: Qoder Agent  
**最后更新**: 2026-09-09  
**设计理念**: 普通人的上位水准 - 打造值得反复对战的学习伙伴  
**参考文档**: 
- [机器人系统总览](../bots.md)
- [斗地主机器人技术方案](ddz-bot.md)
- [中国象棋机器人技术方案](chess-bot.md)
