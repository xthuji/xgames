# XGames 技术架构文档

> **文档索引**：
> - [architecture.md](architecture.md) — 项目总体架构（本文），含前端性能专项与麻将画音同步专项设计
> - [bots.md](bots.md) — 机器人系统全景：设计理念、难度系统、决策解释与复盘系统、各游戏引擎总览、优化记录与路线
> - [bots/](bots/) — 四份分游戏机器人技术方案（ddz/mahjong/gomoku/chess）

## 1. 项目概述

XGames 是一款单机启动的棋牌对战应用：**Go 全栈服务端 + TypeScript/Phaser 3 前端**，支持 4 款棋牌游戏的人机/真人联网对战，前端同时内置 7 款经典单机小游戏。

### 1.1 支持的游戏

| 类型 | 游戏 | 对战模式 | 玩家数 |
|------|------|---------|--------|
| 对战 | 斗地主 | 人机 + 真人房间 | 3 |
| 对战 | 五子棋 | 人机 + 真人房间 | 2 |
| 对战 | 中国象棋 | 人机 + 真人房间 | 2 |
| 对战 | 麻将（推倒胡） | 人机 + 真人房间 | 4 |
| 单机 | 贪吃蛇、连连看、2048、华容道、扫雷、蜘蛛纸牌、俄罗斯方块 | 纯前端 | 1 |

### 1.2 核心特性

- **单房间运行约束**：服务端同时仅允许存在一个房间，减少非预期性能开支
- **服务端权威**：牌型解析、回合流转、结算全部由服务端裁决，客户端只做展示与输入
- **断线重连**：Token 机制 + 房间快照，重连后自动还原牌桌完整状态
- **崩溃恢复**：SQLite 房间快照，进程重启自动恢复 Waiting 房间
- **机器人系统**：每个游戏内置纯 Go 原生实现的 AI 引擎（无大模型、无训练权重依赖），详见 [bots.md](bots.md)
- **难度系统**：四款对战游戏三档难度（简单/普通/困难）全链路落地：前端选择 → 服务端白名单转发 → 各游戏引擎难度参数 → 复盘记录真实档位（详见 [bots.md](bots.md) 第 2 章）
- **决策解释与复盘系统**：统一架构记录AI决策过程，对局结束后生成详细复盘报告，帮助玩家理解AI思考逻辑并提升技术水平（四游戏已全部集成，详见 [bots.md](bots.md) 第 8 章）
- **房间记录持久化**：每间房的创建/参与人/状态落库（rooms 表），大厅实时展示跨游戏房间列表，支持单请求关旧建新（一键重建）

---

## 2. 技术栈

| 层 | 选型 | 说明 |
|----|------|------|
| 服务端语言 | Go 1.26+ | 六边形架构 + 房间 Actor 模型 |
| WebSocket | `github.com/coder/websocket` | context 友好，JSON 信封协议 |
| 存储 | SQLite + `modernc.org/sqlite` | 纯 Go 驱动，WAL 模式 |
| 前端 | TypeScript 5 + Vite + Phaser 3 | 协议类型从 Go structs 自动生成 |
| 桌面壳 | Wails v2（可选） | `-no-window` 切换为纯 HTTP/WS 服务 |
| 日志 | `log/slog` | 结构化日志 |
| AI | 纯 Go 原生算法 | NegaMax、α-β、MCTS、启发式搜索等 |

---

## 3. 总体架构

### 3.1 逻辑架构图

```
┌──────────────────────────────────────────────────────────────────┐
│                    Web 客户端 (TypeScript + Phaser 3)              │
│  场景: Boot / Lobby / Room / Game / Settings / 单机游戏            │
│  WS 客户端 (心跳 + 自动重连 + 消息缓冲)                            │
└──────────────────────────┬───────────────────────────────────────┘
                           │ WebSocket JSON 信封
┌──────────────────────────▼───────────────────────────────────────┐
│                      Go 服务端 (单进程)                            │
│                                                                    │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │                    接口层 (Interface)                         │  │
│  │  WebSocket Hub (连接管理/限流/鉴权)  │  HTTP (静态资源/health)│  │
│  └──────────────────────────┬──────────────────────────────────┘  │
│                             │                                     │
│  ┌──────────────────────────▼──────────────────────────────────┐  │
│  │                    平台层 (Platform)                          │  │
│  │  RoomManager (房间生命周期/单房间约束/快照持久化)              │  │
│  │  MatchQueue (快速匹配)  │  Stats/Leaderboard  │  Reconnect   │  │
│  └──────────────────────────┬──────────────────────────────────┘  │
│                             │                                     │
│  ┌──────────────────────────▼──────────────────────────────────┐  │
│  │                    游戏层 (Games)                             │  │
│  │  每款游戏统一四模块架构:                                       │  │
│  │  rule (规则引擎) │ session (Actor 对局) │ bot (AI 引擎) │ msg  │  │
│  │  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐                         │  │
│  │  │ ddz  │ │gomoku│ │chess │ │mahjng│                          │  │
│  │  └──────┘ └──────┘ └──────┘ └──────┘                         │  │
│  └──────────────────────────┬──────────────────────────────────┘  │
│                             │                                     │
│  ┌──────────────────────────▼──────────────────────────────────┐  │
│  │                    基础设施层 (Infra)                         │  │
│  │  SQLite Store │ Config │ WS Hub │ Logger │ Migrations       │  │
│  └─────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

### 3.2 架构风格

**六边形架构 + 房间 Actor 模型 + 事件驱动**：

- **领域层（games/*/rule）** 零外部依赖，只实现纯规则逻辑，可独立单测
- **会话层（games/*/session）** 为每个对局创建一个 Actor goroutine，事件循环串行处理所有回合，**无锁**
- **平台层（platform/*）** 编排房间生命周期、匹配、统计等跨游戏逻辑
- **基础设施层（infra/*）** 通过接口注入，可整体替换与 mock

### 3.3 统一四模块架构

每款游戏在 `internal/games/{game}/` 下按以下四模块组织：

| 模块 | 职责 | 典型内容 |
|------|------|---------|
| `rule/` | 纯规则引擎 | 牌型解析、合法性校验、胜负判定 |
| `session/` | 对局 Actor | 事件循环、状态机、回合流转、结算落库 |
| `bot/` | AI 引擎 | 决策接口实现、不同难度策略 |
| `msg/` | 游戏私有消息 | 游戏专属的 WS 消息类型与 payload |

通过 `games.Registry` 全局注册，平台层按 `gameID` 查找对应的描述符。

各游戏的 AI 引擎详见 [bots.md](bots.md) 及各游戏专属文档：
- [斗地主机器人](bots/ddz-bot.md)：规则启发式 + MCTS 残局增强
- [五子棋机器人](bots/gomoku-bot.md)：NegaMax + α-β + VCF/VCT 杀棋检测
- [中国象棋机器人](bots/chess-bot.md)：NegaMax + NMP/LMR + Quiescence Search
- [麻将机器人](bots/mahjong-bot.md)：向听数计算 + 牌效率打分 + 对手建模

---

## 4. 连接与协议

### 4.1 WebSocket 传输

- 单连接、JSON 信封：`{ "type": "play_cards", "payload": { ... } }`
- Hub 为每个连接维护读写 goroutine，读 goroutine 分发到处理器，写 goroutine 消费发送 channel 避免并发写
- 消息类型与 payload 由 `internal/platform/protocol/` 定义，TS 类型通过 `go run ./internal/platform/protocol/gen` 自动生成

### 4.2 安全措施

| 措施 | 实现 |
|------|------|
| Origin 白名单 | 握手校验 `security.allowed_origins` |
| 连接限速 | per-IP 令牌桶（10/s、60/min），超限封禁 |
| 消息限速 | per-conn 令牌桶 20 msg/s |
| 重连令牌 | 32 字节随机 hex 存 SQLite，带过期时间，重连后轮换 |

### 4.3 连接生命周期

```
客户端 ──WS 握手──▶ Hub
        ◀── connected {player_id, token}
        ──ping/pong (心跳 15s)──▶
        ──reconnect {token}──▶ Hub (校验+恢复房间)
        ◀── reconnected {room_code, game_state}
```

---

## 5. 房间与对局管理

### 5.1 RoomManager

维护 `map[roomCode]*Room`，6 位数字房号生成。核心约束：**全局同时仅允许存在一个房间**。

```go
// CreateRoom 入口的单房间约束
m.mu.Lock()
if len(m.rooms) > 0 {
    m.mu.Unlock()
    return nil, ErrRoomExists  // "服务端仅支持单房间运行，请等待当前对局结束后再创建"
}
```

### 5.2 房间记录持久化（rooms 表）

房间生命周期内的关键事件同步写入 `rooms` 表（`infra/store/rooms.go`）：

| 操作 | 时机 | 说明 |
|------|------|------|
| `UpsertRoom` | 房间创建、状态防抖刷盘（每秒） | `ON CONFLICT(room_code) DO UPDATE`，不覆盖 created_at；快速创建即销毁的房间也保证留有记录 |
| `CloseRoomRecord` | `destroyRoom` 时 | `status: open → closed` 并写入 closed_at |

记录内容：房号、归属游戏、创建人（ID+昵称）、参与人（JSON）、状态（open/closed）、创建/关闭时间。

### 5.3 大厅房间列表与一键重建

- **实时列表**：`get_room_list` 空 gameID 时跨游戏返回全部房间，列表项含游戏名、创建人、状态（waiting/playing，对局中不可加入）；大厅打开即拉取并每 5 秒刷新
- **一键重建**：建房/人机遇已存在房间（不论归属游戏）时，前端弹窗确认后发送单条 `recreate_room` 请求，服务端 `RoomManager.RecreateRoom` 原子完成：定位创建者旧房 → 广播房间已关闭 → `destroyRoom`（清理机器人/玩家会话、游戏上下文、快照与记录）→ 验证已关闭 → 创建人机或等待房间，返回最终结果；旧的"先关闭再创建"两步重试已废弃

### 5.4 房间生命周期

```
Waiting → Playing → Ended
```

- **Waiting**：创建后，玩家加入/准备，全员就绪自动开局
- **Playing**：对局进行中，房间状态变更时（防抖 1s）序列化写入 SQLite
- **Ended**：结算后房间复位，等待下一局或超时解散

### 5.5 清理机制

| 机制 | 策略 |
|------|------|
| 等待超时 | 10 分钟未开局自动解散 |
| 全员离线 | 所有玩家离线后销毁房间 |
| 启动清理 | 服务重启时清理无活跃创建者的残留房间 |
| 崩溃恢复 | 启动时回放 `room_snapshots` 恢复 Waiting 房间 |
| 销毁落库 | `destroyRoom` 先补写 UpsertRoom 再删快照、关 rooms 记录 |

### 5.6 对局 Actor

每个开局的房间创建一个 Session goroutine，事件循环：

```go
type Event int
const (
    EvPlayerAction Event = iota // 玩家操作（来自 WS）
    EvBotTurn                   // 机器人决策完成
    EvTimeout                   // 回合超时
    EvPlayerOffline             // 掉线
    EvPlayerOnline              // 重连
    EvShutdown                  // 优雅关闭
)

// 所有对局状态只在 Actor 内读写 → 无锁
for {
    select {
    case ev := <-eventCh: ...
    case <-timeout: ...
    case <-ctx.Done(): return
    }
}
```

---

## 6. 数据层

### 6.1 SQLite 表结构

| 表 | 用途 |
|----|------|
| `players` | 玩家身份（ULID + 昵称） |
| `reconnect_tokens` | 重连令牌（轮换制） |
| `matches` / `match_players` | 对局记录（结算时写入） |
| `player_stats` | 累计统计（物化表，结算事务内同步更新） |
| `daily_scores` | 日榜滚动分（周榜按 ISO 周聚合查询） |
| `room_snapshots` | 房间快照（崩溃恢复） |
| `rooms` | 房间记录（房号/归属游戏/创建人/参与人/状态，大厅列表与一键重建依据） |

### 6.2 使用策略

- 驱动：`modernc.org/sqlite`（纯 Go，零 CGO）
- WAL 模式 + `busy_timeout(5000)`
- 单写者：所有写操作串行执行，规避 SQLite 写锁竞争
- 高频房间快照写入做 1s 防抖

---

## 7. 项目目录结构

```
xgames/
├── main.go                              # 入口: Wails 窗口 / -no-window 无头 / -dev 开发
├── data/config.yaml                     # 运行配置
├── VERSION                              # 版本号（构建时 ldflags 注入）
├── wails.json                           # Wails 桌面配置
│
├── internal/
│   ├── games/                           # ★ 游戏层（4 款对战游戏，统一四模块）
│   │   ├── ddz/                         #   斗地主
│   │   │   ├── card/                    #     牌堆/洗牌
│   │   │   ├── rule/                    #     14 种牌型解析/压牌/找牌
│   │   │   ├── session/                 #     对局 Actor (bid/play/afk/ai)
│   │   │   ├── bot/                     #     AI 引擎 (启发式 + 局势化规则 + MCTS 残局增强)
│   │   │   └── msg/                     #     游戏私有消息类型
│   │   ├── gomoku/                      #   五子棋
│   │   │   ├── rule/                    #     棋盘表示/胜负判定
│   │   │   ├── session/                 #     回合 Actor
│   │   │   ├── bot/                     #     NegaMax + α-β + VCF 杀棋
│   │   │   └── msg/
│   │   ├── chess/                       #   中国象棋
│   │   │   ├── rule/                    #     走法生成/将军检测
│   │   │   ├── session/
│   │   │   ├── bot/                     #     NegaMax + 历史/杀手启发
│   │   │   └── msg/
│   │   ├── mahjong/                     #   麻将（推倒胡，禁止吃牌）
│   │   │   ├── rule/                    #     向听数/牌效/胡牌判定/听牌检测（ting）
│   │   │   ├── session/                 #     对局 Actor（碰/杠/胡决策/零和计分）
│   │   │   ├── bot/                     #     向听数优先 + 牌效率打分 + 听牌拆牌保护
│   │   │   └── msg/                     #     游戏私有消息类型
│   │   └── registry.go                  #   游戏注册表（全局 Get/Register）
│   │
│   ├── platform/                        # 平台层
│   │   ├── room/                        #   RoomManager + Room（单房间约束/记录落库/一键重建）
│   │   ├── match/                       #   快速匹配队列
│   │   ├── protocol/                    #   WS 协议定义 + TS 生成器
│   │   ├── server/                      #   Hub: WS 分发 + HTTP 路由
│   │   └── nickname/                    #   随机昵称生成
│   │
│   ├── infra/                           # 基础设施
│   │   ├── ws/                          #   WebSocket Hub/Conn
│   │   ├── store/                       #   SQLite Store + 迁移
│   │   └── config/                      #   config.yaml 加载
│   │
│   └── migrations/                      # SQLite 建表脚本（embed）
│
├── web/                                 # 前端（Phaser 3）
│   ├── embed.go                         # go:embed all:dist
│   ├── src/
│   │   ├── platform/                    #   平台层（场景/网络/状态机）
│   │   ├── games/                       #   游戏场景（4 对战 + 7 单机）
│   │   └── protocol.ts                  #   ⚡ 自动生成
│   └── vite.config.ts
│
├── scripts/                             # 构建/运行脚本
│   ├── run_tools.sh                     #   build/run/dev/test 统一入口（跨平台）
│   └── release.sh                       #   创建 git tag 触发 GitHub Actions 三平台发布
│
├── tests/                               # 测试
│   ├── e2e/                             #   Playwright 浏览器 E2E
│   ├── flow/                            #   斗地主人机练习全流程脚本
│   ├── gotests/                         #   独立 Go 测试（如昵称生成）
│   └── run_game_test.sh                 #   统一测试入口
│
├── .github/workflows/                   # CI/CD
│   └── release.yml                      #   三平台构建发布（macOS DMG + Win/Linux ZIP）
│
└── docs/                                # ★ 技术文档（本目录）
```

---

## 8. 运行与运维

### 8.1 启动模式

```bash
./scripts/run_tools.sh build     # Wails 生产构建 → build/bin/XGames.app
./scripts/run_tools.sh run       # 构建 + 前台运行（实时日志）
./scripts/run_tools.sh dev       # 开发模式: Go 单进程托管，前端变更自动重编译
```

| 模式 | 启动命令 | 说明 |
|------|---------|------|
| 桌面窗口 | `./xgames` | Wails 内嵌 WebView |
| 无头服务 | `./xgames -no-window` | 纯 HTTP/WS 监听 :3030 |
| 开发 | `go run . -dev` | 前端文件监听器，1s debounce 重构建 |

### 8.2 运维能力

| 能力 | 设计 |
|------|------|
| 优雅关闭 | SIGTERM → 广播维护模式 → 等待对局结束（30min 超时）→ 关闭 |
| 健康检查 | `GET /healthz` (liveness) + `GET /readyz` (SQLite 可写) |
| 指标 | `GET /metrics`（在线数/房间数/对局数） |
| 日志 | `slog` JSON 输出，对局事件带结构化字段 |
| pprof | localhost 绑定，`/debug/pprof` |
| 维护模式 | 推送 + 拉取，维护期间阻止开局 |

### 8.3 构建发布流水线

**本地构建**（`run_tools.sh build`）：
- 自动检测当前平台（macOS/Linux/Windows）
- macOS：构建 universal DMG（`build/bin/XGames.app` → `release/XGames_v{ver}.dmg`）
- Linux/Windows：构建 ZIP 包（含二进制 + config.yaml + icon）

**自动化发布**（`scripts/release.sh` + `.github/workflows/release.yml`）：
1. 读取 `VERSION` 文件，创建 git tag `v{version}`
2. 推送 tag 到远程（默认 `backupstream`），触发 GitHub Actions
3. Actions 矩阵构建三平台并行任务：
   - macOS-latest → DMG
   - ubuntu-22.04 → ZIP（安装 WebKitGTK 等 Wails 依赖）
   - windows-latest → ZIP
4. 自动上传到 GitHub Release 页面

**自战测试保护**：`TestSelfPlay_100Games` 需环境变量 `XGAMES_SELFPLAY=1` 才执行，防止 agent 或常规测试误触发。唯一触发入口：`run_tools.sh bot`。

---

## 9. 前端性能与体验专项

> 本章说明影响前端性能的功能点如何处理、取舍依据；原独立文档 performance.md、av-sync.md 已并入本文（麻将画音同步详细设计见第 10 章）。

### 9.1 影响性能的功能点与处理方式

| 功能点 | 当前处理方式 | 取舍依据 |
|--------|--------------|----------|
| 帧率 | 锁定 30 FPS（`main.ts` `fps:{target:30}`） | 回合制/策略玩法无感知差异，渲染次数减半（最大头优化）；未来加入快节奏动作类游戏需按场景放开 |
| 渲染器 | 强制 WebGL + 关闭 antialias + `powerPreference:'high-performance'` | 牺牲 MSAA 抗锯齿换取 GPU 加速（较 Canvas 快 3~5×） |
| 心跳/同步 | 心跳 30s、对局状态同步 30s（各 Scene `delay`） | 回合制状态变更由消息推送驱动，轮询仅作兜底，延长间隔不影响实时性 |
| 单机计时器 | 蜘蛛/华容道/连连看/扫雷 1s → 5s | 时间显示精度 5s 可接受，回调次数 -80% |
| 重绘 | 状态哈希脏检测（`SpiderScene.computeStateHash`） | 仅牌局/选择/完成数变化时重绘 104 张牌，不必要重绘 -80%；麻将尚未引入（见 9.3） |
| 对象创建 | 牌精灵对象池（`getSprite`/`recycleAllSprites`） | 对象创建 -90%，监听器随回收清理防泄漏 |
| 语音播报 | ddz 队列上限 5 + 播放前 `synth.cancel()`；麻将画音双层方案（详见第 10 章）：服务端语音节奏预算（`minActionGap=1.5s`，托管/bot 自动动作距上一动作播报不足预算时挂起等待，许可移交等待播报）+ 客户端"语音追画面"（消息到达即应用画面，播报与牌同步入队；队列上限 3、溢出丢最旧、播前 `synth.cancel()` 打断，兜底时长按文本估算 ≤4s，去重按"文本+席位来源"） | 服务端管顺序与节奏（限定托管节拍不致短到频繁丢音），客户端管"语音永远贴近当前画面"：宁可少播一条旧播报，也不让语音念几拍前的牌 |

### 9.2 体验修复（2026-09 第四轮，性能与体验交叉项）

- 麻将 UI：结算后空文本残留灰块、通知遮挡按钮修复
- 麻将复盘按钮：报告按子目录定位、toast 深度提升、异步落盘重试（`fetchLatestReplayText`）
- ddz 延迟安全网：pending 消息排空后强制 UI 重渲染，防止卡在中间态

### 9.3 后续方向与边界

按收益排序：

1. **麻将状态哈希脏检测**：手牌/副露量大、重绘开销最高，移植蜘蛛纸牌同模式收益最大
2. **复盘报告加载优化**：分段渲染 + 后端 gzip（随复盘系统迭代同步做）
3. **Text 对象池**：多场景高频创建/销毁文本对象，池化降 GC 压力
4. **音频降采样**：语音 mp3 统一 16kHz 单声道，包体与解码开销双降

**非目标**：不为单机小游戏追求 60 FPS / GPU 粒子等游戏级画质，与"普通人的上位水准"整体定位一致。

---

## 10. 麻将画音同步专项设计：许可节奏预算（生产端控速）+ 消费端语音追画面

> 状态：已实施（本章为唯一实施方案，代码落地、参数调整与后续演进均以本章为准。原独立文档 av-sync.md 已并入本文。测试加速：无 UI 自动化测试经 `session.SetMinActionGapForTest(0)` 关闭节奏预算，in-package 单测直接调小 `minActionGap`）。

### 10.1 背景与问题

麻将托管/机器人对局中存在"画面进度与语音播报进度不一致"的画音不同步。早期表现为画面瞬间跳过好几轮、前几轮播报被静默丢弃；引入客户端同步门（画面等语音）后又出现新的失效模式：语音逐拍滞后持续累积，并在 flush 时点（结算、取消托管、碰杠询问）画面瞬间跳多拍，而语音仍在念好几拍之前的牌。

#### 10.1.1 现状架构（控制权位置）

| 层 | 真源位置 | 职责 | 现有实现 |
|----|---------|------|---------|
| 服务端 | 牌局状态、许可、计时 | 出牌顺序（许可环）、节奏（回合超时 + 语音节奏预算）、托管/bot 代打 | `internal/games/mahjong/session/`（单 Actor goroutine） |
| 客户端 | 本地 TTS 播报 | 画面即时反映服务端权威进度；语音按"只保最新"播报 | `web/src/games/mahjong/MahjongScene.ts` 的 `speechQueue` |

- **许可环 + 观察员已在服务端存在**：`currentTurn` 即许可（只有持有者可摸牌/出牌），`advanceToNext` 按环序移交许可，`resolveClaimsAfter` 作为观察员在每次弃牌后按"胡 > 杠 > 碰"扫描全部席位并将许可改写给响应者（`executePong/executeKong`）。
- **所有席位的语音进入本地单一 `speechQueue` 串行播报**：任何时刻只有一条在实际发声，排队中的旧条目可以安全丢弃——这是"只保最新"策略成立的前提。

#### 10.1.2 根因（已核实代码证据链）

1. **产速与消速无法精确匹配**：客户端 TTS 单条时长不可知（取决于文本长度、系统语音、设备性能），服务端拍间隔由节奏预算与思考延迟决定（托管约 3~4.3s/席位），两者的固定差会在整局内线性累积。
2. **"画面等语音"的门控放大了错位**：门控 hold 住画面等播报完成，一旦某拍超时（WebKit 的 `speechSynthesis.onend` 可能不触发，旧兜底为固定 5s）`pendingEvents` 就逐拍堆积；而 `flushPendingEvents` 只放行画面、不清语音队列 → 画面瞬间跳到最新一拍，语音却仍按队列念旧牌，积压的播报还在同一瞬间全部入队。这正是"语音说好几拍前的牌 + 结算瞬间跳拍"的直接成因。
3. 结论：**客户端既无法事前约束产速，也不该用挂起画面的方式追语音**——语音进度不是牌局真源，画面必须即时反映服务端状态；同步问题因此收敛为"让语音始终贴近当前画面"（斗地主已验证的取舍）。

### 10.2 方案结论（对比后采纳）

对四种候选（客户端门控单打 / 许可环+语音状态标记+客户端 ACK / 服务端控速单打 / 组合方案）的对比结论：

- **标记 + ACK 方案**：闭环管的是伪需求（语音串行已由本地队列天然保证），真正要管的"画面 vs 本地语音"不受标记约束；且感知不完整（单标记只代表 1/N 客户端视角）、有活性风险（客户端掉线/后台化/TTS 失败 → 标记悬空 → 环卡死 → 必须超时兜底退化为开环）。其可靠运行域是开环方案的子集。
- **客户端同步门（画面等语音）**：曾实施，实测失效——画音"同步"的代价是画面被本地播报拖住，与"画面反映服务端权威牌局"的定位相冲，且滞后会在 flush 点转为断崖跳拍（见 10.1.2）。**已放弃**。
- **服务端控速单打**：能压低累积速度，但消除不了客户端 TTS 时长不确定带来的错位。
- **组合方案（采纳）**：服务端把"许可移交时机"作为一等节奏参数（松弛同步：动作播报后至少间隔 T 再移交许可，决策计算在等待窗口内并发完成），负责限定托管节拍上限；客户端不挂起画面，语音"只保最新"（丢旧保新 + 播前打断）。两层与真源位置对齐：**服务端管顺序与节奏，客户端管语音贴近画面**。

### 10.3 服务端设计：许可移交节奏预算

#### 10.3.1 核心参数

```go
// minActionGap 语音节奏预算（松弛画音同步）：托管/bot 自动动作的播报
// 距上一动作播报的最小间隔。客户端采用"画面即时走、语音只保最新"策略（不挂起画面），
// 本预算用于限定托管节奏上限、使单拍间隔不致于短到频繁丢弃语音。
var minActionGap = 1500 * time.Millisecond // 松弛模式可调；未来服务端下发音频（时长可知）时升级为严格同步
```

- 锚点：Session 新增 `lastActionAt time.Time`（仅 Actor 内读写），在每次**带语音的动作广播**后更新：`MsgMjDiscarded`（handleDiscard）、`MsgMjPongMade`（executePong）、`MsgMjKongMade`（executeKong/executeAddKong）、`MsgGameOver`（executeWin）。
- 只约束 **bot/托管自动决策路径**；真人路径保持现状（真人回合超时远大于 1.5s，真人间快速碰杠的偶发连播由客户端队列丢旧保新兜住，不引入真人操作的额外延迟）。

#### 10.3.2 需要约束的节拍清单（改点）

| # | 节拍 | 现状间隔 | 位置 |
|---|------|---------|------|
| A | 弃牌 → bot/托管 碰/明杠/胡 | **0（立即）** | `resolveClaimsAfter` 三处自动决策 |
| B | 弃牌 → 下家摸牌 → bot/托管 暗杠/补杠/自摸胡 | **0（立即）** | `advanceToNext` 自动分支 |
| C | 杠 → 杠后摸牌 → bot/托管 杠上开花自摸胡 | **0（立即）** | `afterKongDraw` |
| D | 开托管 → 立即代打碰/杠/胡 | 接近 0（边界场景） | `resolvePendingClaimForAfk`（暂不改，见 10.3.6） |
| E | 回合开始 → bot 出牌 | 性格延迟 0.8~2.8s | **`personality.CalculateThinkDelay` 下限低于 1.5s，需上调**（见 10.3.5） |
| F | 回合开始 → 托管兜底代打 | afkActDelay 3s ≥ 1.5s | 无需修改 |

#### 10.3.3 执行机制：预算等待 + 代次失效 + 取消回退

不阻塞 Actor、不改动回合定时器（`gs.timer`）与快照逻辑，沿用 `startClaimTimer` 的"独立 goroutine 定时器 + 事件投递"模式：

```go
// autoActionEvent 语音节奏预算到期事件（gen 与当前代次不符则丢弃）
type autoActionEvent struct{ gen int }

// pendingAutoAction 挂起中的托管/bot 自动动作（语音节奏预算等待中）
type pendingAutoAction struct {
    gen       int
    playerIdx int
    exec      func() // 预算到期后在 Actor 内执行（闭包自带状态再校验）
    cancel    func() // 等待期间玩家关闭托管时，转回真人交互处理
}
```

Session 新增字段（仅 Actor 内读写）：`lastActionAt`、`actionGen int`、`pendingAuto *pendingAutoAction`。

```go
// scheduleAutoAction 托管/bot 自动动作统一入口：距上一动作播报不足 minActionGap 时
// 延迟执行（松弛画音同步——许可移交等待语音时间预算，决策已在等待窗口内并发完成）
func (gs *Session) scheduleAutoAction(playerIdx int, exec, cancel func())
// firePendingAuto(gen)  到期事件：代次匹配且 phase==PhasePlaying 才执行 exec
// cancelPendingAutoFor(idx) 玩家关闭托管：取消挂起动作、actionGen++ 使到期事件失效、执行 cancel()
```

**并发正确性要点**：

1. **等待窗口内的状态保护**：挂起期间 `currentTurn` 仍停留在上家（或已置为下家）、`waitingForClaim == false`，理论上允许脏操作。在 `handleDiscard/handlePong/handleKong/handleWin/handlePass` 顶部增加守卫：`gs.pendingAuto != nil` 时拒绝（窗口 ≤1.5s，期间无合法动作可达）。
2. **托管取消回退**：`handleAfk(afk=false)` 与 `handleOnline`（重连会自动解除托管，绕过 handleAfk）对挂起玩家调用 `cancelPendingAutoFor`，`cancel` 闭包把局面转回真人交互：
   - A 类（吃弃牌的碰/杠/胡）→ 按当时可行动作重新 `askClaim`（canWin/canKong/canPong 标志与调度时一致）；
   - B 类（摸牌后）→ `giveTurnTo(nextIdx, drawnTile)`：可自摸胡则 `askSelfDrawWin`，否则广播 `MsgMjTurn` + 启动 `timeouts.Turn` 回合定时器（暗杠/补杠仍可手动执行）；
   - C 类（杠后）→ `askSelfDrawWin(pidx, kongTile)`（currentTurn 已在 afterKongDraw 置位）。
3. **代次失效**：`actionGen` 在每次 `scheduleAutoAction` 与取消时 +1；迟到的到期事件按 gen 不匹配丢弃，杜绝过期动作误执行。
4. **exec 再校验**：闭包执行前重验 `phase == PhasePlaying` 与规则条件（手牌在窗口内不变，校验是防御性的）；校验失败仅记日志（正常流程不可达，可达路径都已先经 cancel）。
5. **崩溃恢复窗口**：挂起期（≤1.5s）进程崩溃时 `pendingAuto` 不在快照中，恢复局 `TimerRemaining` 不会被启动（`applySnapshot` 中 `gs.timer` 恒为 nil 的既有行为），局面停在"许可待移交"。**接受此极小概率窗口**（与恢复局本就不恢复回合定时器的现状一致），不做快照扩展；如后续要消除，再给 Snapshot 增加 pendingAuto 字段。

#### 10.3.4 各节拍的 exec/cancel 具体形态

- **A（resolveClaimsAfter）**：bot 决策（`botDecidePong/botDecideKong`/必胡）在调度时同步算好并捕获进 exec；exec 再验 `rule.CanXxx(gs.hands[pidx], gs.lastDiscard, ...)` 后调 `executePong/executeKong/executeWin`。
- **B（advanceToNext）**：整段自动分支合并为**一个** `scheduleAutoAction`：摸牌与 `MsgMjDraw` 定向发送照旧、`currentTurn` 先行置为 `nextIdx`，exec 内按"自摸胡 > 暗杠 > 补杠 > 无动作则广播 MsgMjTurn + startTurnTimer"的优先级重新评估执行。
- **C（afterKongDraw）**：exec 验 `CanSelfDrawWinWithMelds` 后 `executeWin(pidx, true)`；cancel 为 `askSelfDrawWin`。
- **真人分支不变**：`askClaim / askSelfDrawWin / 广播 Turn + startTurnTimer` 路径保持原样。

#### 10.3.5 E 拍：bot 出牌延迟下限上调

性格系统延迟下限低于预算（已核实 `personality.go`）：simple 0.8~1.2s、medium 1.4~2.0s、complex 2.2~2.8s。**不采用会话侧拒收 bot 出牌**的方案（会打断控制器节奏、依赖兜底定时器补打，复杂且浪费），直接上调下限，保证 ≥ `minActionGap`：

- simple：800-1200ms → **1500-1900ms**
- medium：1400-2000ms → **1600-2000ms**
- complex：2200-2800ms 不变

同步更新该函数注释中"各档上限必须低于 afkActDelay=3s"的约束说明（下限对齐 minActionGap）。测试注入 `SetDelay` 不受影响。`defaultThinkDelay`（3.5~4s）与 `afkActDelay`（3s）已 ≥ 预算，不动。

#### 10.3.6 D 拍（resolvePendingClaimForAfk）暂不约束

触发条件是"玩家在询问等待期手动开启托管"，距上一动作播报通常已远超 1.5s；且该函数缺少自摸胡询问牌值（tile 未持久化，cancel 回退需要它）。收益极小、改动有耦合，列为不做；若实测发现该拍可感知不同步，再扩展。

### 10.4 客户端设计：语音追画面（丢旧保新）

`web/src/games/mahjong/MahjongScene.ts`，四项配合：

#### 10.4.1 画面即时应用，不挂起

`MsgMjTurn` / `MsgMjDraw` / `MsgMjDiscarded` / `MsgMjPongMade` / `MsgMjKongMade` 到达即在处理器内更新 `mjState` 并 `render()`，播报与牌同步进入 `speechQueue`：

- 手动出牌在飞牌动画（300ms）结束后直接发 `MsgMjDiscard`，不再等上一条播报播完（旧 `pendingDiscard` 机制删除）；
- 取消托管、碰杠胡询问、结算都不再需要"追平"分支——画面从不滞后，`pendingEvents` / `applyWhenSpeechDone` / `flushPendingEvents` / `isSelfAfk` 已全部移除。

#### 10.4.2 语音队列：只保最新

| 项 | 值 / 做法 | 理由 |
|----|-----------|------|
| `MAX_SPEECH_QUEUE` | 3，溢出 `shift()` 丢弃最旧 | 语音最多落后画面 2 拍，绝不念更早的牌 |
| `SPEECH_GAP_MS` | 700 → 400ms | 压缩单拍消费，降低触顶丢音概率 |
| 播前 `synth.cancel()` | 保留 | "丢旧保新"的必要一环：新语音必须立即出声，不能被未播完的旧 utterance 堵住 |
| `speechEstimateMs` | `clamp(400 + 260×字数, 1200, 4000)` | `onend`/`onerror` 未触发时的兜底放行时长；上限必须低于服务端单拍间隔，否则语音消费慢于产速、永久追不上画面（旧的固定 5s 兜底正是滞后来源之一） |

#### 10.4.3 去重按"文本 + 席位来源"

`speakText(text, source)`，source 形如 `discard-<playerID>`、`pong-<playerID>`、`kong-<playerID>`、`draw-self`、`win-<playerID>`、`draw-game`，仅同席位同文本才判重。旧实现的全局 `lastSpeakText` 会在多席位轮转时把不同席位的相同播报误吞（斗地主同坑已修，见其 `lastSpeakSource`）。

#### 10.4.4 画面跳到最新时清音

三处调用 `clearSpeechQueue()`（清空队列 + `speechSynthesis.cancel()` 打断当前播报）：

- `MsgGameOver`：结算画面为最终牌局，"谁胡了"必须立即成为唯一在播内容；
- `onGameState`（重连 / 恢复局全量快照）与 `onGameStart`（新一局）：跨状态、跨局的旧播报已无意义。

### 10.5 组合后的时序保证

| 场景 | 保证 |
|------|------|
| 正常托管/bot 对局 | 画面即时等于服务端进度；语音与牌河最新一张相差 ≤ 2 拍，超出即丢弃旧语音 |
| 设备慢 / TTS 时长超预期 | 队列丢旧保新自动跳回最新一拍，画面不停顿 |
| 碰/杠连拍（A 拍间隔仅 1.5s） | 可能少播一条被顶掉的播报，属既定代价；画面与语音均不卡 |
| 玩家取消托管 | 无需追平（画面从不滞后），接管即时基于最新牌局 |
| 结算/胡牌 | 结算弹窗与胡牌播报同时到位，不追念积压播报 |
| 玩家掉线/后台化 | 服务端节奏不依赖任何客户端存活（无 ACK 依赖，无卡死路径） |

### 10.6 参数汇总

| 参数 | 值 | 位置 | 说明 |
|------|-----|------|------|
| `minActionGap` | 1.5s | `session/pacing.go` | 语音节奏预算；限定托管节拍上限，松弛模式可调 |
| 性格延迟 simple | 1500-1900ms | `bot/personality.go` | 原下限 0.8s 低于预算 |
| 性格延迟 medium | 1600-2000ms | `bot/personality.go` | 原下限 1.4s 低于预算 |
| `afkActDelay` | 3s | `session/state.go` | 托管兜底超时 ≥ 预算 |
| `MAX_SPEECH_QUEUE` | 3 | `MahjongScene.ts` | 语音只保最新，溢出丢最旧 |
| `SPEECH_GAP_MS` | 400ms | `MahjongScene.ts` | 播报间隔（原 700ms） |
| `speechEstimateMs` | 上限 4s | `MahjongScene.ts` | onend 兜底放行时长，按文本长度估算 |

客户端不再有 `MAX_PENDING_EVENTS` / `MAX_PENDING_EVENTS_AFK`——同步门已随策略反转一并移除。

### 10.7 涉及文件与测试计划

| 文件 | 改动 |
|------|------|
| `internal/games/mahjong/session/pacing.go` | minActionGap、lastActionAt/actionGen/pendingAuto 字段逻辑、scheduleAutoAction/firePendingAuto/cancelPendingAutoFor、autoActionEvent；注释按客户端策略校准 |
| `internal/games/mahjong/session/session.go` | Session 字段、handleEvent 增加 autoActionEvent 分支 |
| `internal/games/mahjong/session/play.go` | 各节拍接入 scheduleAutoAction（A/B/C）、handleDiscard 广播后 touchActionAt、pendingAuto 守卫 |
| `internal/games/mahjong/session/state.go` | handleAfk/handleOnline 接入 cancelPendingAutoFor |
| `internal/games/mahjong/bot/personality.go` | 延迟下限上调（simple/medium） |
| `web/src/games/mahjong/MahjongScene.ts` | §10.4 四项：移除同步门与 `pendingDiscard`、队列丢旧保新、席位级去重、清音时点 |
| `docs/architecture.md` | §9.1 语音播报行摘要 + 本章完整设计（原 av-sync.md 并入） |

**测试**：

1. Go：`go build ./... && go vet ./...`；session 单测覆盖——预算内节拍被延迟执行、托管取消回退到 askClaim/askSelfDrawWin、代次失效（取消后到期事件不执行）、等待窗口内提交出牌被拒。
2. 集成回归：`internal/platform/server` 麻将练习/托管用例（`SetDelay` 注入的加速路径不受影响）。
3. 前端：`npx tsc --noEmit`；手动验证托管对局——语音所述牌为牌河最新一张或其前 2 拍之内、不出现念几拍前的旧牌；胡牌瞬间只播"谁胡了"；自己手动出牌点击后即发（无 0.7s+ 延迟）。
4. 长局观测：bot 全自动对局 ≥30 分钟，确认画面不卡顿、语音不脱离当前牌局（偶发丢一条播报为既定代价）。

### 10.8 演进路径（严格同步）

当前为**松弛模式**（服务端锚定时间预算 T，不需感知播报结束），客户端取"画面即时 + 语音只保最新"。**逐拍严格对齐（画面等语音）为已放弃分支**：它依赖不可控的本地 TTS 时长，只有当语音改为服务端合成/预录音频下发（每条时长服务端自知识别）时才重新评估：

- `scheduleAutoAction` 的等待时长由 `minActionGap` 常数升级为"本动作语音精确时长"；
- 许可移交从"播报开始 + T"升级为"播报结束才移交"（严格模式）；
- 全程无需新增任何客户端 ACK 协议。

---

## 11. 优化记录与路线（索引）

2026-09 已完成（细节见对应章节）：

- **机器人策略、难度系统全链路、复盘系统重构**：详见 [bots.md](bots.md) 第 9 章「已完成优化记录」
- **前端三轮 CPU 优化 + 第四轮体验修复**：详见 §9

下一步路线：

- **机器人**：P0 对手建模增强 / P1 教学场景专项 / P2 稳定性与性能 / 明确放弃方向，详见 [bots.md](bots.md) 第 10 章
- **前端性能**：详见 §9.3
