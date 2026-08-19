# 机器人系统文档

> 合并自原 `bot-engines.md`、`bot-implementation-strategy.md`、`bot-technical-design-v2.md`、`bot-optimization-phase1~4-summary.md` 与开源方案调研；2026-09 二次合并：architecture.md 原第 9 章（设计理念与难度系统）、第 10 章（决策解释与复盘系统）、第 13 章机器人相关优化记录均已并入本文。内容以当前代码实现为准（2026-09）。

## 文档索引（按游戏分文档）

各游戏的详细技术方案（当前实现详解 + 主流方案对比 + 差异分析与优化路线）：

| 游戏 | 分文档 | 一句话定位 |
|------|--------|-----------|
| 斗地主 | [bots/ddz-bot.md](bots/ddz-bot.md) | 规则启发式 + MCTS 残局（两级决策） |
| 麻将（推倒胡） | [bots/mahjong-bot.md](bots/mahjong-bot.md) | 向听数优先 + 牌效率 + 防守安全度 + TingGuard |
| 五子棋 | [bots/gomoku-bot.md](bots/gomoku-bot.md) | NegaMax + α-β + VCF/VCT + 置换表 |
| 中国象棋 | [bots/chess-bot.md](bots/chess-bot.md) | NegaMax + Move Ordering + 位置估值表 |

本篇保留全景综述与统一设计模式说明，细节以分文档为准。

## 1. 概述

四款对战游戏的 AI 均为**纯 Go 原生实现**：无大模型、无训练权重、无 GPU 依赖，编译即部署。各引擎在经典博弈算法之上叠加了高级特性与局势化策略。

| 游戏 | 核心算法 | 关键增强 | 策略水平 |
|------|---------|---------|---------|
| 五子棋 | NegaMax + α-β + 迭代加深 + VCF/VCT | 威胁感知候选、并发搜索、LRU 置换表 | 业余初段~3段 |
| 中国象棋 | NegaMax + α-β + Move Ordering + 位置估值 | MVV-LVA、90 格估值表、并发搜索、开局库 | 业余初段~3段 |
| 斗地主 | 规则启发式（记牌→分析→局势化策略）+ MCTS 残局 | 贝叶斯推断、局势化出牌规则、MCTS 真实胜率观测 | 中等偏上（60-70%休闲玩家） |
| 麻将（推倒胡） | 向听数计算 + 牌效率打分 + 碰杠权衡 | 防守安全度、对手读牌、听牌拆牌保护 | 中等偏上（50-60%休闲玩家） |

### 1.1 设计理念

**核心定位：普通人的上位水准**

所有对战游戏的机器人都遵循统一的设计理念：
- **有可玩性**：不会因碾压而沮丧，也不会因太弱而无趣
- **有思考空间**：决策逻辑透明可理解，玩家能从中学习战术技巧
- **有提升路径**：通过观察机器人的决策，玩家能改进自己的技术

**设计原则**：
1. **可解释优先**：所有决策规则透明，玩家能理解"为什么这样出/走"
2. **适度失误**：不追求完美最优解，允许次优但合理的决策
3. **攻守平衡**：根据局势动态调整进攻/防守权重，模拟人类思维
4. **冷启动即用**：无需训练数据，部署即可提供有意义的对战体验

### 1.2 统一接口

```go
type DecisionEngine interface {
    DecideBidScore(ctx, botName, hand, highBid) int     // 叫分（斗地主）
    DecideDouble(ctx, botName, hand) bool               // 加倍（斗地主）
    DecidePlay(ctx, botName, gctx) []card.Card          // 出牌（斗地主）
    DecideDiscard(ctx, botName, hand, melds) int        // 出牌（麻将）
    DecideAction(ctx, botName, hand, tile) (pong, win)  // 碰/胡（麻将）
    DecideMove(ctx, botName, board, color/camp) (...)   // 落子/走子（棋类）
}
```

引擎由 `games.Get(gameID).NewBot(logger)` 构造，Session Actor 在机器人回合调用。

### 1.3 共同设计模式

- **消息驱动 Controller**：Room 转发对局消息 → Controller 跟踪状态（每游戏独立 `gameTrack`）→ 机器人回合经随机延迟后决策 → `BotResponder` 提交动作。
- **拟人延迟**：斗地主/五子棋/象棋 1~3s，麻将 2~2.5s（画音同步预算下限约束见 [architecture.md §10.3.5](architecture.md)）。
- **零外部依赖**：规则透明、日志完整、可调试性强。
- **难度系统**：四游戏三档难度已全链路落地（2026-09），详见第 2 章。
- **决策解释与复盘系统**：统一的三层架构（核心层/适配层/应用层）记录AI决策过程并生成复盘报告，详见第 8 章。

---

## 2. 难度系统（2026-09 全链路落地）

前端四款对战游戏均提供简单/普通/困难三档难度选择，服务端与引擎侧已全部落地：

| 游戏 | 简单 | 普通 | 困难 | 服务端落地状态 |
|------|------|------|------|----------------|
| **斗地主** | 记关键牌（A/2/王），12% 失误率 | 全量记牌，3% 失误率 | 完美记牌+MCTS 残局搜索 | ✅ `DefaultPersonalities` 三档人格化参数 + 记牌完整度差异，复盘记录真实档位 |
| **麻将** | 进攻优先（防守权重 0.3，10% 失误率） | 攻守平衡（0.6，5%） | 防守优先（0.9，1%，现物兜底提前） | ✅ `DifficultyConfig` 三档预设（防守权重/失误率/向听精度/ukeire 模式/碰牌阈值/对手听牌推断） |
| **五子棋** | 2 层搜索 + VCF 6 + Top-5 扰动 | 3 层 + VCF 8 + Top-3 | 4 层 + VCF 8 + Top-2 | ✅ `DifficultyConfig` 三档预设（深度/杀棋深度/开局库概率/Top-N softmax 扰动） |
| **中国象棋** | 2 层 + 开局库 0.3 + Top-5 | 3 层 + 0.5 + Top-3 | 4 层 + 0.8 + Top-2 | ✅ `DifficultyConfig` 三档预设 + 时间预算 2s |

统一链路：前端难度选择 → 服务端 `validBotDifficulty` 白名单（easy/normal/hard）转发 `options["difficulty"]` → 各游戏 Controller 消费（空/未知档位回退 normal）→ 引擎难度参数生效 → 复盘报告记录真实档位。零值配置归一化保证托管兑底等未携带难度的调用方保持既有最强行为。

各游戏难度参数细节：
- 斗地主：`DefaultPersonalities` 三档人格化参数（激进/风险/配合/失误率）+ 记牌范围差异（normal 仅关键牌，hard 全量）
- 麻将/五子棋/象棋：统一沿用 `DifficultyConfig` + 三档预设 + `DifficultyFor`（空/未知回退 normal）+ 零值归一化蓝本
- 设计理念：打造"值得反复对战的学习伙伴"，而非"不可战胜的 AI"

详见各游戏专属文档的难度分级章节。

---

## 3. 斗地主（internal/games/ddz/bot/）

### 3.1 架构

| 模块 | 文件 | 职责 |
|------|------|------|
| 控制器 | controller.go | 消息驱动，跟踪对局状态；每回合新建 `handCountCache` 注入 GameContext |
| 记牌 | tracker.go | 手牌计数 + 未知牌池 + 过牌推理；normal 仅跟踪关键牌（王/2/A），hard 全量 15 点数 |
| 分析 | handcount.go | DFS + 记忆化计算最少出牌手数；支持 per-turn 缓存复用 |
| 策略 | strategy.go / situation.go | 统一领出/跟牌 + 局势化出牌规则 |
| 残局 | mcts.go | UCT 树搜索 + 并行 rollout（batchSize=100，goroutine pool） |
| 叫分 | bid.go | 手牌评分阈值决策：炸弹+3、大王+2、小王+1.5、2+1、A+0.5；结构加分（MinHandCount 节省手数×0.3 + 顺子潜力 ≥5/+1.0、≥10/+1.5）；孤张惩罚（2/-0.5、单王无2/-0.3，手牌<5 不应用）；≥3.5/5.5/7.5 对应叫 1/2/3 分，≥4.5 加倍 |

### 3.2 出牌策略

**领出（unifiedLead 决策链）**：

```
一手带牌恰好清空手牌（三带一/三带二/四带二/飞机带翅）→ finishingLead 直接打出（规则 5）
对手最小余牌 ≤ 5 → pickCautiousLead 谨慎控牌：只出大批量牌型或 K/A/2 压制性单/对（规则 4）
正常局势：
  ├─ 地主，或农民且下家是队友
  │    → pickFastShed(allowSmall=true)：优先大批量（连对/顺子/飞机/三带/四带二），其次最小单/对
  └─ 农民且下家是地主，对手最大余牌 > 10
       → pickFastShed(allowSmall=false)：快速跑牌，限制 <6 小单/小对防下家跟跑（规则 3）
其余 → 通用启发式（残局死封、农民角色分工、手数最小化、控制牌优先、炸弹保护）
```

**跟牌（unifiedFollow）**：
- 不压队友大牌（除非能直接走完）；队友出小牌必接应；
- **掌权保护**：非控场/危急/谨慎状态，且目标 KeyRank ≤ 10 时，不用 Q/K/A/2 压小牌，留作后手收权；
- **拆牌重计划**：连续 ≥2 次让牌（无法管住对手，`UnbeatenStreak`）或对手余牌很少时，原出牌计划失效——放开手数过滤与 Q/K/A 掌权保护（2/王仍保留），允许拆散对子/三条/顺子压住对手夺回牌权（炸弹/王炸仍不拆）；
- 残局强封（对手 ≤2 张无条件死封）；非危急不出增手数的牌；仅危急/可冲刺时用炸。

**拦牌（pickBlockCard）**：中段单张（J→7）→ 最大单张（≤A）→ 中段对子（7~J）→ 最大对子（≤A），优先消耗中段牌。

**带牌小牌化**（`rule/hints.go`，领出/跟牌/MCTS 共用）：三带一/三带二/四带二/飞机带翅的带牌按"现成单张/对子 > 拆三条 > 拆炸弹、同级点数升序"主选；**若首选含掌权王牌（Q/K/A/2/王），改按点数升序从最小非炸弹组拆出小牌**——带走小牌、大牌留作夺权；炸弹绝不拆。兜底 `fallbackKickers` 同策略。

### 3.3 MCTS 残局增强（UCT + 并行）

自己 ≤5 张或任一对手 ≤3 张时启用 UCT 树搜索：构建根节点子树（每个候选一个子节点），批量并行执行 Selection→Simulation→Backpropagation（batchSize=100，goroutine pool + sync.WaitGroup），用 UCB1 公式平衡探索与利用（C=√2）；对手手牌按贝叶斯推断加权抽样（已出牌越少的点数权重越高，农民刚出过的点数 ×1.5）；rollout 策略为"领出最小、跟牌最小能压"；农民配合与死封场景不接管；返回最优候选的真实模拟胜率（记入日志观测）。非残局走规则引擎。原置信度门控（confidence_gate.go）已随 2026-09 P0 简化移除，现为"残局 MCTS、其余规则"两级决策。

### 3.4 局势化辅助（situation.go）

`isPowerRank` / `powerCardCount`（掌权牌统计）、`powerLeadCost`（非胜出领出的掌权消耗 3 倍计罚）、`splitPenaltyEx`（拆掌权对子/三条加罚）、`minOpponentCount` / `maxOpponentCount`（局势分段）、`nextSeatIsEnemy` / `nextSeatIsTeammate`。

---

## 4. 麻将（internal/games/mahjong/）

推倒胡规则：禁止吃牌，仅碰、杠、胡。

### 4.1 核心决策：向听数优先

```
score = 打出后向听数 × 1000 + 牌效率损失 × 进攻权重 + 危险度 × 防守权重 → 选最小
牌效率损失（discardLoss）：
  ├─ 已组好的牌型尽量不拆：刻子 +36 / 对子 +24 / 完整顺子成员 +30
  ├─ 部分搭子（顺子缺首尾或缺中间）按记牌器进张余量评估成组概率
  └─ 无望组织好（进张余量 0，或等待 >5 巡且余量 ≤1）→ 按散牌处理优先打出
防守权重随巡数线性上升（15 巡封顶），向听数始终主导；记牌器视图由
bot Controller（全局牌河 + 自己面子/手牌）与 session 托管（各家牌河/面子）分别构建。
```

### 4.2 碰杠决策

能胡永远胡；碰后进听必碰；否则碰前碰后向听数对比，不恶化才碰；杠后补牌可能胡则杠。

### 4.3 防守与读牌

- **防守安全度**（engine.go）：危险牌评估——对手鸣牌后舍牌的现物/筋牌安全度分级，后期避铳，不再随意打危险牌；
- **对手读牌**（opponent_reading.go）：结合筋牌理论、壁牌理论、染手识别的多维概率推断。

### 4.4 听牌拆牌保护

- `rule/ting.go`：`TenpaiWaits`（听牌集合）、`IsTenpaiWithMelds`（含面子听牌判定）、`TenpaiOuts`（结合可见牌的剩余张数，评估成胡可能性）；
- **TingGuard**（bot/engine.go + session/state.go）：托管/机器人处于听牌状态时延迟拆牌——成胡可能性高至少等 7 轮、低等 3 轮，期间只打安全牌保持听牌；`tenpaiRounds` 随回合累计，超时后恢复普通牌效。

### 4.5 关键技术点

手牌镜像维护（MsgGameState/MsgMjDraw 定向消息）、碰牌消耗同步、镜像为空时跳过代打交由会话超时兜底。

---

## 5. 五子棋（internal/games/gomoku/bot/engine.go）

### 5.1 决策流程

```
1. 立即胜利检测 → 2. 对手立即胜利堵四 → 3. VCF/VCT 杀棋检测
→ 4. 迭代加深（1→4）α-β 搜索，候选点评分取最优
```

### 5.2 评估与搜索

- 棋型分值：连五 10⁷ / 活四 10⁶ / 冲四 10⁵ / 活三 5×10⁴ / 眠三 10³ / 活二 10² / 眠二 10；总分 = 我方 − 对方 × 0.9；
- 候选生成：仅考虑已有棋子半径 2 内空位 + 威胁感知筛选 + 智能评分排序；
- VCF（连续冲四）与 VCT（连续活三）必胜路径检测；
- 置换表：Zobrist 哈希 + LRU 淘汰；并发 NegaMax 与开局库预热。

---

## 6. 中国象棋（internal/games/chess/bot/engine.go）

### 6.1 决策流程

迭代加深（深度 1→4）+ Move Ordering + NegaMax 递归 + α-β 剪枝。

### 6.2 Move Ordering（三重增强）

| 策略 | 说明 |
|------|------|
| MVV-LVA | 吃子优先：受害者价值高、攻击者价值低者排前 |
| 杀手启发 | 上一深度导致剪枝的着法同深度优先（+50000） |
| 历史启发 | 被截断好着累计历史得分排序 |

### 6.3 局面评估

子力基础值（将 10000 / 车 600 / 马 270 / 炮 285 / 象 120 / 士 110 / 兵 30）+ **90 格 × 6 兵种位置估值表**（黑方镜像）+ 将军动态（+50 / 被将军 −100）。

### 6.4 性能特性

LRU 置换表（4096 容量）、历史/杀手启发表扁平数组、并发搜索、开局库缓存预热。

---

## 7. 当前能力总表与技术债务

各引擎当前已落地的关键能力（实现方案与代码位置见各分文档对应章节）：

| 游戏 | 关键能力 |
|------|---------|
| 斗地主 | 三档人格化难度（`intentionalImperfection` 领出犯错）；UCT 树 MCTS（UCB1 + 并行 rollout + 贝叶斯发牌）；记牌与过牌推理（PassInfo/CumPasses）；拆牌重计划（`UnbeatenStreak`）；跑牌方积极跟牌（`pickShedFollow`）；带牌小牌化；叫分结构因子；收官决策（一次走完优先 / 两步走完夺权牌）；决策解释与复盘 |
| 麻将 | 精确向听数 0-8（搭子分解 DP）+ 全局缓存；全量记牌器 `Seen`；`OpponentReader` 对手读牌；`discardLoss` 成型牌保护；TingGuard 听牌拆牌保护；Ukeire 裁剪 + HandBitset + 并行决策；人性化（动态延迟/三维性格/情绪/玩家适应）；决策解释与复盘 |
| 五子棋 | VCF/VCT 杀棋搜索；威胁感知候选生成；Zobrist 置换表（LRU）；并发根节点评估；12 种职业定式开局库（8 重对称变体 + 手数门控 + 难度概率使用）；组合威胁分；DifficultyConfig 三档难度；决策解释与复盘 |
| 中国象棋 | 迭代加深 NegaMax + α-β；quiescence 静态搜索（Delta 剪枝）；NMP（R=2）；LMR；Move Ordering 五重打分；三次重复判和 + 求和倾向（contempt）；开局库（62 条线路 / 150+ 局面条目，加权随机，精确 positionKey 编码）；DifficultyConfig 三档难度；真人复盘离线分析 |

验证方式：常规验证为单元测试（`./scripts/run_tools.sh test`，已自动跳过自战测试）+ 人机对战人工验证；四游戏各 100 局 bot 自战模拟（TestSelfPlay_100Games，按难度合理分配：斗地主/麻将座位难度轮转，五子棋/象棋同难度 80 局 + 跨难度梯度局 20 局，须加 `-timeout 45m`）**仅在修改机器人出牌/决策逻辑后运行**（`./scripts/run_tools.sh bot`，耗时 30-60 分钟），用于验证难度梯度与胜率，常规测试一律跳过。

### 7.1 已知技术债务（2026-09 核对）

- 斗地主 OpponentModel 尚未构建（过牌推理已有，但无风格画像/历史行为建模）——当前 P0
- 斗地主 MCTS 无超时保护（超时回退规则引擎的兑底缺失）
- 麻将对手读牌为二值疑似判断，未概率化（`InferWaitRange` 雏形未接入危险度评估）
- 复盘报告为纯文本格式，无结构化数据层；加载无 gzip/分段渲染
- 麻将状态哈希脏检测未引入（前端性能项，见 [architecture.md §9.3](architecture.md)）

---

## 8. 决策解释与复盘系统

### 8.1 设计目标

为所有对战类游戏（斗地主、象棋、五子棋、麻将）实现了统一的决策解释与复盘系统，能够：
- **实时记录AI的决策过程**：每次AI决策时自动记录候选着法、评分、推理说明
- **生成详细复盘报告**：对局结束后输出关键决策点分析、玩家风格总结、改进建议
- **提升可解释性**：将复杂的AI决策转化为自然语言解释，帮助玩家理解思考逻辑
- **提供教学价值**：通过复盘报告帮助玩家学习策略，发现自身失误

### 8.2 三层架构与核心数据结构

采用三层架构实现统一抽象与可扩展性：

```
┌─────────────────────────────────────────────────────────┐
│                   应用层 (Application)                    │
│  - 各游戏的 Controller 调用 Adapter.RecordDecision()     │
│  - 对局结束后调用 GenerateReport() 生成复盘报告           │
└───────────────────────┬─────────────────────────────────┘
                        │
┌───────────────────────▼─────────────────────────────────┐
│                   适配层 (Adapter)                        │
│  - DDZReplayAdapter (斗地主)                             │
│  - GomokuReplayAdapter (五子棋)                          │
│  - MahjongReplayAdapter (麻将)                           │
│  （象棋适配器已随重构移除，走真人离线分析链路）             │
│                                                          │
│  职责：将游戏特定数据转换为通用格式                         │
└───────────────────────┬─────────────────────────────────┘
                        │
┌───────────────────────▼─────────────────────────────────┐
│                   核心层 (Core)                           │
│  - DecisionLog: 单次决策日志                              │
│  - ReplayReport: 复盘报告                                │
│  - DecisionLogger: 日志收集器                             │
│  - FormatDecisionLog: 格式化决策日志                      │
│  - FormatReplayReport: 格式化复盘报告                     │
└─────────────────────────────────────────────────────────┘
```

**核心数据结构**：

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

### 8.3 各游戏适配器特色

#### 斗地主适配器 (`internal/games/ddz/bot/replay_adapter.go`)

**关键特性**：
- **过牌推理**：检测连续让牌次数（`UnbeatenStreak ≥ 2`），识别拆牌夺权时机
- **农民配合分析**：判断队友接近走完时是否送大牌帮助收尾
- **炸弹检测**：自动标记炸弹/王炸使用时刻
- **角色感知**：区分地主/地主上家（顶牌位）/地主下家（跑牌位）的不同策略

**示例输出**：
```
[第12轮] 【地主】机器人A 出牌: ♠K ♥K
原因:
  - 领出回合，需要主动出击
  - 作为地主，需要快速跑牌
  - 对手可能只剩单张（过牌推理：连续 3 轮未压 J/Q）
  - 保留 ♠A 作为后手收权（掌权保护）
```

#### 象棋（`internal/games/chess/bot/user_analysis.go`，决策日志适配器已移除）

象棋复盘走真人离线分析链路（无 bot 决策日志适配器）：

- **失误分级**：引擎求"当时最优走法"对比实际选择——Critical（分差 ≥150 或错失绝杀）/ Major（≥60）/ Minor
- **绝杀检测**：引擎可绝杀而实际未绝杀 → Critical
- **亮点标记**：与引擎一致且收益 >150 分
- **丢子换算**：按子力价值描述损失（车/马炮/士象/兵卒）

详细链路见 [chess-bot.md §8.2](bots/chess-bot.md)。

#### 五子棋适配器 (`internal/games/gomoku/bot/replay_adapter.go`)

**关键特性**：
- **VCF/VCT检测**：标记连续冲四/连续活三的必胜路径发现
- **威胁分析**：识别活四/冲四形成的关键时刻
- **中心位置评估**：解释靠近棋盘中心的战略价值
- **必胜着法标记**：高亮直接获胜或堵四的紧急决策

**示例输出**：
```
[第23步] 机器人A: (7, 8) 形成活三
原因:
  - 发现VCT杀棋路径（连续活三攻击）
  - 形成双活三威胁，对手无法同时防守
  - 靠近中心位置，控制力更强
```

#### 麻将适配器 (`internal/games/mahjong/bot/replay_adapter.go`)

**关键特性**：
- **向听数/进张分析**：解释打出某张牌后的进张余量变化
- **危险度评估**：结合对手读牌和筋牌理论，标记高风险出牌（>70）
- **碰/杠决策**：分析碰后进听、杠后补牌可能胡的关键时刻
- **TingGuard集成**：听牌状态下的拆牌保护延迟说明

**示例输出**：
```
[第18巡] 机器人C 打出发财
原因:
  - 当前向听数: 2，打出发财后仍保持2向听
  - 危险度: 45（中期，对手未明显染手）
  - 进张余量: 发财已见3张，剩余1张，优先处理散牌
  - 听牌保护: 尚未听牌，无需TingGuard
```

### 8.4 关键决策检测、玩家风格与改进建议

#### 智能关键决策检测

各适配器根据游戏规则自动识别重要决策点：

| 游戏 | 关键决策类型 | 检测规则 |
|------|-------------|---------|
| **斗地主** | 炸弹/王炸、拆牌夺权、农民配合、该压不压 | `UnbeatenStreak ≥ 2`、对手余牌 ≤ 5、炸弹使用、最少手数差 |
| **象棋** | 失误分级、绝杀错失、亮点 | `AnalyzeUserDecisions`：评估分差 / 绝杀检测 / 丢子换算 |
| **五子棋** | 漏杀/漏堵、VCF/VCT、必胜 | 漏杀漏堵成五点、`detectThreats()`、`isWinningMove()` |
| **麻将** | 碰/杠、听牌、高危出牌 | `actionType == "pong/kong"`、`IsTenpaiWithMelds()`、`DangerLevel > 70` |

#### 玩家风格分析算法

基于历史决策数据统计学习：

```go
func analyzePlayerStyle(decisions []DecisionLog) string {
    aggressive := countAggressiveMoves(decisions)  // 进攻性着法（吃子、将军、炸弹等）
    defensive := countDefensiveMoves(decisions)    // 防守性着法（堵四、拆牌保护等）
    risky := countRiskyMoves(decisions)            // 风险着法（次优选择、高危出牌）
    total := len(decisions)

    if float64(aggressive)/total > 0.4 {
        return "激进型（偏好进攻）"
    }
    if float64(defensive)/total > 0.4 {
        return "保守型（偏好防守）"
    }
    if float64(risky)/total > 0.2 {
        return "冒险型（愿意承担风险）"
    }
    return "均衡型（攻守平衡）"
}
```

#### 改进建议生成逻辑

根据对局数据自动生成个性化建议：

1. **关键失误检测**：`StrengthDiff < -2.0` 表示选择了明显次优解
2. **游戏特定场景**：
   - 斗地主：农民未配合送大牌、地主未快速跑牌
   - 麻将：晚巡打危险牌、未保护听牌状态
   - 象棋：错失吃子机会、未防守将军
   - 五子棋：未发现VCF/VCT路径、未堵四
3. **统计异常**：某类决策频率显著偏离正常范围

### 8.5 实现文件与集成方式

**实现文件**：

| 层级 | 文件路径 | 职责 |
|------|---------|------|
| **核心层** | `internal/games/bot/replay/types.go` | 核心类型定义、日志收集器、格式化输出 |
| | `internal/games/bot/replay/user_report.go` | 真人玩家复盘报告（PlayerReplayLogger/BuildUserReport/FormatUserReplayReport/SaveUserReport） |
| **适配层** | `internal/games/ddz/bot/replay_adapter.go` | 斗地主适配器（过牌推理、农民配合分析；bot 决策日志记录） |
| | `internal/games/gomoku/bot/replay_adapter.go` | 五子棋适配器（真实分数/深度/杀棋命中；bot 决策日志记录） |
| | `internal/games/mahjong/bot/replay_adapter.go` | 麻将适配器（向听数/进张/危险度分析；bot 决策日志记录） |
| **离线失误分析** | `internal/games/{ddz,mahjong,chess,gomoku}/bot/user_analysis.go` | 真人快照 vs 引擎最优解对比，失误分级（Critical/Major/Minor）+ 亮点标记（四游戏） |
| **快照记录** | `internal/games/*/session/`（`recordUserMove`） | 真人出牌/走子前记录决策快照（bot/托管跳过） |

**后端集成**（2026-09 重构后，以真人玩家为中心）：

1. 各游戏 session 在真人出牌/走子入口记录决策快照（`recordUserMove`，bot/托管跳过）
2. 对局结束 `endGame` 后异步为每个真人玩家生成报告：`bot.AnalyzeUserDecisions`（用引擎求"当时最优出法"，与玩家实际选择对比，判定失误分级与亮点）→ `replay.BuildUserReport` → `replay.SaveUserReport`
3. 报告落盘 `data/replays/{game}/{roomCode}_{playerID}_{timestamp}.txt`，纯真人房间同样产出
4. bot 决策日志保留（ddz/mahjong/gomoku 适配器 `RecordDecision`）用于决策解释；Controller 不再生成 bot 报告（象棋适配器已随重构移除）

**前端集成**：

- 对局结束弹窗"📊 复盘"按钮 → `/api/replays?playerId={playerId}`（`replayApi.ts` 内置 4 次×800ms 重试应对异步落盘竞态）
- 模态对话框展示格式化后的复盘报告，支持滚轮/拖拽滚动

**集成状态**：

| 游戏 | 适配器实现 | Controller集成 | 前端集成 | 真人失误分析 |
|------|-----------|---------------|---------|-------------|
| **斗地主** | ✅ 决策日志 | ✅（不再生成报告） | ✅ | ✅ user_analysis |
| **麻将** | ✅ 决策日志 | ✅（不再生成报告） | ✅ | ✅ user_analysis |
| **五子棋** | ✅ 决策日志 | ✅（不再生成报告） | ✅ | ✅ user_analysis |
| **象棋** | ➖ 已移除 | ➖ | ✅ | ✅ user_analysis |

**基础设施（2026-09 完成修复）**：
- 后端 `/api/replays` 支持 `?game=` 子目录定位与 `playerId` 过滤（`resolveDir` 白名单校验防目录穿越）
- 前端共享模块 `replayApi.ts`：拉取最新报告带 4 次×800ms 重试，应对报告异步落盘竞态
- 四游戏 Scene 的复盘弹窗为薄封装；toast 深度提升至 300，不再被结算弹窗遮挡
- 报告以真人玩家为中心：session 记录快照 → 异步生成 → `SaveUserReport`（文件名 `{roomCode}_{playerID}_{ts}.txt`），报告 Difficulty 字段记录真实档位（原硬编码 normal 已修复）

### 8.6 性能与扩展性

- **内存占用**：每条约 2-5 KB（取决于候选数量）
- **CPU开销**：< 1%（仅在记录时轻微开销）
- **延迟影响**：可忽略不计（异步记录，对局结束后批量生成报告）
- **异步生成**：`onGameOver` 中使用 goroutine 批量生成报告，避免阻塞
- **可配置开关**：通过 `EnableReplay(false)` 完全禁用
- **目录注入**：报告目录开发模式在项目根，打包运行在用户持久化目录，由 main 启动时解析注入，写入与读取共用同一目录

**扩展性**：新增游戏只需实现对应 user_analysis 分析器并在 session 接入快照记录即可。

### 8.7 使用示例

**决策日志记录用法**（bot 决策解释，适配器 API 保持不变）：

```go
// 1. 创建适配器
adapter := bot.NewDDZReplayAdapter("game-123")

// 2. 在决策时记录
adapter.RecordDecision(round, playerName, isLandlord, hand, ctx,
                       candidates, chosen, reasoning)

// 3. 真人玩家报告由 session 生成：
//    AnalyzeUserDecisions → BuildUserReport → SaveUserReport
fmt.Println(replay.FormatReplayReport(report))
```

**真人玩家报告输出要点**（各游戏报告以玩家视角展开）：失误节点（含回合/手牌快照）、引擎推荐出法与实际选择对比、失误分级（Critical/Major/Minor）、亮点标记、改进建议。

**输出示例**：

```
=== 斗地主对局复盘报告 ===

对局 ID: game-123
时长: 12m45s
参与玩家: 机器人A, 玩家B, 玩家C
获胜者: 机器人A

--- 关键决策点 ---
[第12轮] 【地主】机器人A 出牌: ♠K ♥K
原因:
  - 领出回合，需要主动出击
  - 作为地主，需要快速跑牌
  - 对手可能只剩单张（过牌推理：连续 3 轮未压 J/Q）
  - 保留 ♠A 作为后手收权（掌权保护）
评估分: 850.00

--- 错过的机会 ---
1. 第8轮: 有更优选择（评分 920 vs 750）

--- 玩家风格分析 ---
机器人A: 激进型（偏好进攻）
玩家B: 保守型（偏好小牌逐步推进）
玩家C: 均衡型（攻守平衡）

--- 改进建议 ---
1. 玩家B 在第8轮出现明显失误，可考虑更优选择
2. 玩家C 在队友接近走完时应优先送大牌帮助收尾
```

---

## 9. 已完成优化记录（2026-09，并入自 architecture.md）

### 9.1 机器人策略优化

| 游戏 | 优化 | 落点 |
|------|------|------|
| 斗地主 | 拆牌重计划：连续 ≥2 次让牌/对手告急时放开手数过滤与掌权保护，拆牌夺回牌权（`UnbeatenStreak`） | `ddz/bot/strategy.go` |
| 斗地主 | 跑牌方积极跟牌：地主/地主下家对 ≤J 目标跟出最小可压牌，避免小牌滞留（`pickShedFollow`） | `ddz/bot/strategy.go` |
| 斗地主 | 带牌小牌化：三带/四带二/飞机带翅优先带走小牌，掌权王牌留作夺权 | `ddz/rule/hints.go` |
| 斗地主 | 两级决策简化：置信度门控移除，MCTS 升级为 UCT 树搜索（UCB1 + 并行 rollout + 贝叶斯发牌） | `ddz/bot/mcts.go` |
| 斗地主 | 决策修复：一次走完优先、两步走完+夺权牌、顺子/连对/飞机 KeyRank 语义修正 | `ddz/rule/validator.go` 等 |
| 麻将 | 精确向听数（搭子分解 DP，0-8 级）+ 全局缓存（RWMutex + LRU） | `mahjong/rule/rule.go` |
| 麻将 | discardLoss 重写：成型牌保护、无望搭子判断、部分搭子进张余量评估（记牌器 `Seen`） | `mahjong/bot/engine.go` |
| 麻将 | TingGuard 听牌拆牌保护（成胡可能性定级延迟拆牌）+ 零和计分 | `mahjong/session/play.go` |
| 麻将 | OpponentReader 对手读牌接入 Controller（舍牌序列/染手识别/疑似听牌判断） | `mahjong/bot/controller.go` |
| 中国象棋 | 开局库重写为"90 格精确局面 + 执子方"编码，修复中盘误命中缺陷 | `chess/bot/opening_book.go` |
| 中国象棋 | 静态搜索（Quiescence）+ LMR + 杀手/历史启发补齐 | `chess/bot/engine.go` |
| 五子棋 | 开局库 12 种职业定式（直指/斜指），带颜色 key 匹配 | `gomoku/bot/opening_book.go` |

### 9.2 难度系统落地要点

- 服务端 `validBotDifficulty` 白名单（easy/normal/hard）统一转发 `options["difficulty"]`，四游戏 Controller 消费（空/未知回退 normal）
- 斗地主：`DefaultPersonalities` 三档人格化参数 + 记牌完整度差异
- 麻将：`DifficultyConfig`（防守权重/失误率/向听精度/ukeire 模式/碰牌阈值/对手听牌推断/现物兑底阈值），自博弈梯度校准验证
- 五子棋/象棋：`DifficultyConfig`（搜索深度/杀棋深度/开局库概率/Top-N softmax 扰动），零值归一化回退既有行为
- 复盘报告记录真实难度档位（原硬编码 normal 已修复）

详见第 2 章。

### 9.3 复盘系统重构要点

- session 记录真人决策快照，对局结束异步生成玩家失误分析报告（引擎求"当时最优出法"对比，失误分级 + 亮点标记 + 改进建议）；纯真人房间同样产出
- 报告落盘 `data/replays/{game}/{roomCode}_{playerID}_{ts}.txt`，后端 `/api/replays` 支持按 game 子目录定位、playerId 过滤、列表/详情
- 前端 `replayApi.ts` 共享模块（含 4 次×800ms 重试应对异步落盘）
- bot 决策日志保留（ddz/mahjong/gomoku 适配器），Controller 不再生成 bot 报告（象棋适配器已移除）

详见第 8 章。

---

## 10. 下一步优化方向（2026-09 更新，难度系统已落地移出）

| 优先级 | 方向 | 内容 |
|--------|------|------|
| **P0** | 对手建模增强 | 斗地主 EWMA 风格画像融入 MCTS 贝叶斯发牌；麻将听牌范围概率（`InferWaitRange`）驱动危险度评估 |
| **P1** | 教学场景专项 | 练习模式框架 + 实时错误检测 + 策略知识库；复盘报告结构化升级 |
| **P2** | 稳定性与性能 | 斗地主 MCTS 超时保护；五子棋开局库扩充；象棋开局库扩容与长将/长捉判负；复盘加载优化 |

明确放弃：DouZero/NNUE/AlphaZero 类黑盒强 AI（违背"普通人上位、可解释、冷启动"定位）；麻将深度学习辅助与日麻扩展（长期可选）。

各游戏细节见分文档"下一步优化路线"章节。

---

## 11. 参考资料

### 开源方案选型（纯程序实现，非大模型）

| 游戏 | 首选参考 | 备选 |
|------|---------|------|
| 五子棋 | Gomoku-AI（Rust/Wasm 高性能） | minimax-gomoku（Python 极简） |
| 中国象棋 | ElephantEye 象眼引擎（工业级） | Chinese-Chess-AI（Wasm 移植） |
| 斗地主 | doudizhu（MCTS + 规则引擎） | DouZero（强化学习，非大模型） |
| 麻将 | mahjong-ai（纯策略引擎） | riichi-mahjong（可定制框架） |

其他：DouZero 论文、天凤 AI、ElephantBase、Cyclone 引擎、Renju 规则。
