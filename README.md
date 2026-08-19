# XGames

> **本项目由 AI 辅助编写**，包括服务端代码、前端界面、游戏机器人算法及技术文档。

单机一键启动的棋牌对战应用：**Go 全栈服务端 + TypeScript/Phaser 3 前端**，支持 4 款棋牌游戏的人机/真人联网对战，内置 7 款经典单机小游戏。

## 功能特性

### 对战游戏（人机 + 真人房间）

| 游戏 | 玩家数 | AI 引擎 |
|------|--------|---------|
| 🃏 斗地主 | 3 | 规则启发式 + MCTS 残局增强 |
| ⚫⚪ 五子棋 | 2 | NegaMax + α-β + VCF 连续冲四杀棋 |
| ♟️ 中国象棋 | 2 | NegaMax + 历史/杀手启发 + 位置估值 |
| 🀄 麻将（推倒胡） | 4 | 向听数计算 + 牌效率打分（禁止吃牌） |

### 单机小游戏（纯前端）

贪吃蛇 · 连连看 · 2048 · 华容道 · 扫雷 · 蜘蛛纸牌 · 俄罗斯方块

### 平台能力

- **单房间运行**：服务端同时仅支持一个房间，减少非预期性能开支
- **服务端权威**：牌型解析、回合流转、结算全部服务端裁决
- **断线重连**：Token 机制 + 房间快照，重连后完整还原牌桌
- **崩溃恢复**：SQLite 快照自动恢复 Waiting 房间
- **三档难度系统**：四款对战游戏全链路落地（前端选择 → 服务端白名单转发 → 引擎参数 → 复盘记录真实档位，人机练习时可选）
  - 斗地主：人格化参数 + 记牌完整度差异（关键牌 → 全量记牌）+ 故意犯错率（12%/3%/0%）
  - 麻将：防守权重/向听精度/ukeire 模式/现物兜底阈值差异（进攻优先 → 攻守平衡 → 防守优先）
  - 五子棋：搜索深度差异（2层 → 3层 → 4层，VCF/VCT 深度与 Top-N 扰动联动）
  - 中国象棋：搜索深度差异（2层 → 3层 → 4层，开局库概率与 Top-N 扰动联动）
- **决策解释与复盘**：以真人玩家为中心的失误分析报告（四游戏），对局结束自动生成，含失误节点、更优出法与改进建议；纯真人房间同样产出报告

## 技术栈

| 层 | 选型 |
|----|------|
| 服务端 | Go（六边形架构 + 房间 Actor 模型，select+channel 无锁状态机） |
| 通信 | WebSocket（`coder/websocket`），JSON 信封协议，TS 类型从 Go structs 自动生成 |
| 存储 | SQLite（`modernc.org/sqlite` 纯 Go 驱动，WAL 模式） |
| 前端 | TypeScript 5 + Vite + Phaser 3 |
| 桌面壳 | Wails v2（可选，`-no-window` 即纯 HTTP/WS 服务） |
| AI | 纯 Go 原生博弈算法（无大模型、无训练权重） |

## 快速开始

### 环境要求

- Go 1.26+
- Node.js 18+ / npm
- 桌面构建（可选）：[Wails CLI](https://wails.io)

### 开发模式

一条命令同时搞定前后端：

```bash
./scripts/run_tools.sh dev     # 等价 go run . -dev -no-open
# 访问 http://localhost:3030
```

### 构建与运行

```bash
./scripts/run_tools.sh build   # 跨平台构建：macOS DMG / Linux ZIP / Windows ZIP
./scripts/run_tools.sh run     # 构建并前台运行（Ctrl+C 关闭）
```

**自动化发布**：`scripts/release.sh` 读取 `VERSION` 创建 git tag，推送后触发 GitHub Actions 三平台并行构建并上传到 GitHub Release。

直接运行二进制：

```bash
go build -o xgames .
./xgames                # Wails 桌面窗口模式
./xgames -no-window     # 无头服务模式（自动打开浏览器）
```

### 测试

```bash
./scripts/run_tools.sh test    # go test -race ./...（自动跳过机器人自战测试）
./scripts/run_tools.sh bot     # 机器人自战测试（四游戏各 100 局，需 XGAMES_SELFPLAY=1）
```

自战测试通过环境变量 `XGAMES_SELFPLAY=1` 保护，防止 agent 或常规测试误触发（耗时 30-60 分钟）。仅 `run_tools.sh bot` 会设置该变量。

## 目录结构

```
xgames/
├── main.go                    # 入口: Wails / -no-window / -dev
├── data/config.yaml           # 运行配置
├── VERSION                    # 版本号
├── internal/
│   ├── games/                 # ★ 游戏层（4 款对战游戏，统一四模块）
│   │   ├── ddz/               #   斗地主: card/rule/session/bot/msg
│   │   ├── gomoku/            #   五子棋: rule/session/bot/msg
│   │   ├── chess/             #   中国象棋: rule/session/bot/msg
│   │   ├── mahjong/           #   麻将: rule/session/bot/msg
│   │   └── registry.go        #   游戏注册表
│   ├── platform/              # 平台层
│   │   ├── room/              #   RoomManager（单房间约束）
│   │   ├── match/             #   快速匹配
│   │   ├── protocol/          #   WS 协议 + TS 生成器
│   │   └── server/            #   Hub: WS 分发 + HTTP 路由
│   └── infra/                 # 基础设施（ws/store/config）
├── web/                       # 前端 Phaser 3（embed 进 Go 二进制）
│   └── src/
│       ├── platform/          #   平台场景/网络/状态机
│       └── games/             #   11 款游戏场景
├── scripts/                   # 构建/运行脚本
│   ├── run_tools.sh           #   build/run/dev/test 统一入口（跨平台）
│   └── release.sh             #   创建 git tag 触发三平台发布
├── tests/                     # 测试
│   ├── e2e/                   #   Playwright 浏览器 E2E
│   ├── flow/                  #   斗地主人机练习全流程脚本
│   └── run_game_test.sh       #   统一测试入口
├── .github/workflows/         # CI/CD（三平台构建发布）
└── docs/                      # 技术文档
```

## 配置参考

`data/config.yaml` 主要配置项：

| 配置段 | 关键项 | 默认值 | 说明 |
|--------|--------|--------|------|
| `server` | `port` | 3030 | HTTP/WS 监听端口 |
| `game` | `turn_timeout` | 30 | 出牌回合超时（秒） |
| `game` | `room_timeout` | 10 | 房间等待超时（分钟） |
| `game` | `offline_wait_timeout` | 30 | 掉线座位保留时间（秒） |
| `security` | `rate_limit` | 10/s, 60/min | 连接限速 |
| `security` | `message_limit` | 20/s | 消息限速 |

## 技术文档

- [整体架构](docs/architecture.md) — 项目总体架构、技术栈、房间管理、前端性能与体验专项、麻将画音同步专项设计、优化路线
- [机器人系统](docs/bots.md) — 四款对战游戏机器人：算法、策略、优化历程
  - [斗地主机器人](docs/bots/ddz-bot.md)
  - [五子棋机器人](docs/bots/gomoku-bot.md)
  - [麻将机器人](docs/bots/mahjong-bot.md)
  - [中国象棋机器人](docs/bots/chess-bot.md)

## License

本项目采用 [MIT License](LICENSE) 开源。

Copyright (c) 2026 XGames Contributors
