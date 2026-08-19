import type Phaser from 'phaser';
import { GameScene } from '../games/ddz/GameScene';
import { SpiderScene } from '../games/spider/SpiderScene';
import { MinesweeperScene } from '../games/minesweeper/MinesweeperScene';
import { TetrisScene } from '../games/tetris/TetrisScene';
import { SnakeScene } from '../games/snake/SnakeScene';
import { LinkLinkScene } from '../games/linklink/LinkLinkScene';
import { G2048Scene } from '../games/g2048/G2048Scene';
import { KlotskiScene } from '../games/klotski/KlotskiScene';
import { GomokuScene } from '../games/gomoku/GomokuScene';
import { gkState } from '../games/gomoku/state';
import { ChessScene } from '../games/chess/ChessScene';
import { ccState } from '../games/chess/state';
import { MahjongScene } from '../games/mahjong/MahjongScene';
import { mjState } from '../games/mahjong/state';
import { MsgTypes, type GameStateDTO, type GkGameStateDTO, type CcGameStateDTO, type MjGameStateDTO, type MessageType } from './protocol';
import { registerReplayable } from './net/ws';
import { store } from './state/store';

/**
 * 前端游戏注册表：每个游戏在此登记描述符与场景工厂，
 * 大厅/房间/路由/外壳侧边栏均从这里读取，平台代码不感知具体游戏。
 */
export interface GameDescriptor {
  /** 游戏 ID（与后端 games.Game.ID() 一致） */
  id: string;
  /** 展示名 */
  name: string;
  /** 侧边栏图标 */
  icon: string;
  /** 房间席位约束 */
  minPlayers: number;
  maxPlayers: number;
  /** 是否支持机器人（人机练习 / 添加机器人按钮的显隐） */
  supportsBots: boolean;
  /** 单机游戏：不走服务端房间/匹配流程，侧边栏点击直接进入对局场景 */
  singlePlayer?: boolean;
  /** 对局场景 key（Phaser 场景） */
  sceneKey: string;
  /** 场景构造函数（注册进 Phaser 场景表） */
  scene: new () => Phaser.Scene;
  /** 房间选项渲染（如斗地主机器人难度）；缺省无选项 */
  hasDifficulty?: boolean;
  /** 重连/状态恢复：把服务端下发的游戏私有状态还原进客户端状态 */
  restoreState?: (raw: unknown) => void;
  /** 该游戏需要在断线/场景切换间隙缓冲重放的关键消息类型 */
  replayable?: string[];
}

const registry = new Map<string, GameDescriptor>();

/** 注册游戏描述符（重复注册抛错，尽早暴露配置冲突） */
export function registerGame(desc: GameDescriptor): void {
  if (registry.has(desc.id)) {
    throw new Error(`游戏重复注册: ${desc.id}`);
  }
  registry.set(desc.id, desc);
}

export function getGame(id: string): GameDescriptor | undefined {
  return registry.get(id);
}

/** 全部已注册游戏（按注册顺序） */
export function listGames(): GameDescriptor[] {
  return [...registry.values()];
}

/** 当前选择游戏的描述符（未注册时回退第一个，保证平台总有可用游戏） */
export function currentGame(): GameDescriptor {
  const list = listGames();
  return getGame(currentGameID()) ?? list[0];
}

/** 当前游戏的对局场景 key */
export function currentGameSceneKey(): string {
  return currentGame().sceneKey;
}

/** 用当前游戏的方式还原私有对局状态（重连/状态恢复入口） */
export function restoreCurrentGameState(raw: unknown): void {
  const g = currentGame();
  if (g.restoreState) {
    g.restoreState(raw);
    return;
  }
  store.restoreGameState(raw as GameStateDTO);
}

/** 场景 key 是否为某个游戏的对局场景（场景路由/状态同步用） */
export function isGameScene(sceneKey: string): boolean {
  return listGames().some((g) => g.sceneKey === sceneKey);
}

/** 全部游戏场景构造函数（供 Phaser 场景表拼装） */
export function gameSceneClasses(): Array<new () => Phaser.Scene> {
  return listGames().map((g) => g.scene);
}

// ── 当前游戏选择（外壳侧边栏驱动，localStorage 持久化） ──

const CURRENT_GAME_KEY = 'xgames_current_game';

export function currentGameID(): string {
  return localStorage.getItem(CURRENT_GAME_KEY) ?? 'ddz';
}

export function setCurrentGameID(id: string): void {
  if (!registry.has(id)) return;
  localStorage.setItem(CURRENT_GAME_KEY, id);
}

// ── 游戏登记 ──

registerGame({
  id: 'ddz',
  name: '斗地主',
  icon: '♠️',
  minPlayers: 3,
  maxPlayers: 3,
  supportsBots: true,
  hasDifficulty: true,
  sceneKey: 'Game',
  scene: GameScene,
  restoreState: (raw) => store.restoreGameState(raw as GameStateDTO),
  replayable: [
    MsgTypes.MsgDealCards,
    MsgTypes.MsgBidTurn,
    MsgTypes.MsgBidResult,
    MsgTypes.MsgLandlord,
    MsgTypes.MsgPlayTurn,
    MsgTypes.MsgCardPlayed,
    MsgTypes.MsgPlayerPass,
  ],
});

registerGame({
  id: 'gomoku',
  name: '五子棋',
  icon: '⚫',
  minPlayers: 2,
  maxPlayers: 2,
  supportsBots: true,
  hasDifficulty: true,
  sceneKey: 'GomokuGame',
  scene: GomokuScene,
  restoreState: (raw) => gkState.restore(raw as GkGameStateDTO),
  replayable: [
    MsgTypes.MsgGkTurn,
    MsgTypes.MsgGkMoveMade,
    MsgTypes.MsgGkAfkChanged,
  ],
});

registerGame({
  id: 'chess',
  name: '中国象棋',
  icon: '車',
  minPlayers: 2,
  maxPlayers: 2,
  supportsBots: true,
  hasDifficulty: true,
  sceneKey: 'ChessGame',
  scene: ChessScene,
  restoreState: (raw) => ccState.restore(raw as CcGameStateDTO),
  replayable: [
    MsgTypes.MsgCcTurn,
    MsgTypes.MsgCcMoveMade,
    MsgTypes.MsgCcAfkChanged,
  ],
});

registerGame({
  id: 'mahjong',
  name: '麻将',
  icon: '🀄',
  minPlayers: 2,
  maxPlayers: 4,
  supportsBots: true,
  hasDifficulty: true,
  sceneKey: 'MahjongGame',
  scene: MahjongScene,
  restoreState: (raw) => mjState.restore(raw as MjGameStateDTO),
  replayable: [
    MsgTypes.MsgMjTurn,
    MsgTypes.MsgMjDiscarded,
    MsgTypes.MsgMjPongMade,
    MsgTypes.MsgMjAfkChanged,
  ],
});

// ── 单机游戏（纯前端，不走服务端房间/匹配流程） ──

registerGame({
  id: 'spider',
  name: '蜘蛛纸牌',
  icon: '🕷️',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'SpiderGame',
  scene: SpiderScene,
});

registerGame({
  id: 'mines',
  name: '扫雷',
  icon: '💣',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'MinesGame',
  scene: MinesweeperScene,
});

registerGame({
  id: 'tetris',
  name: '俄罗斯方块',
  icon: '🧱',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'TetrisGame',
  scene: TetrisScene,
});

registerGame({
  id: 'snake',
  name: '贪吃蛇',
  icon: '🐍',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'SnakeGame',
  scene: SnakeScene,
});

registerGame({
  id: 'link',
  name: '连连看',
  icon: '🔗',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'LinkGame',
  scene: LinkLinkScene,
});

registerGame({
  id: 'g2048',
  name: '2048',
  icon: '🔢',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'G2048Game',
  scene: G2048Scene,
});

registerGame({
  id: 'klotski',
  name: '华容道',
  icon: '🧩',
  minPlayers: 1,
  maxPlayers: 1,
  supportsBots: false,
  singlePlayer: true,
  sceneKey: 'KlotskiGame',
  scene: KlotskiScene,
});

// 各游戏的可重放关键消息登记进网络层白名单（模块加载即生效）
for (const g of listGames()) {
  if (g.replayable) registerReplayable(g.replayable as MessageType[]);
}
