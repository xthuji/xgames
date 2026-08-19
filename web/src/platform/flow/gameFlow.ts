/**
 * GameFlow 客户端游戏流程状态机
 *
 * 跟踪客户端从大厅到对局再到结算的完整生命周期，
 * 为场景切换提供统一的状态转换入口，避免分散的 store.inGame 判断。
 *
 * 状态转换图：
 *   IDLE → LOBBY → ROOM → GAME → GAME_OVER → ROOM → GAME → ...
 *                     ↘ LOBBY（同步检测失败 / 主动回退）
 *
 * 消息生命周期管理：
 *   退出 GAME / GAME_OVER 时自动触发 clearPending 回调，
 *   防止 stale 游戏消息泄漏到下一局（由 ws 层注册回调）。
 */

export enum FlowState {
  /** 初始状态 / 连接断开 */
  IDLE = 'IDLE',
  /** 大厅场景 */
  LOBBY = 'LOBBY',
  /** 房间等待场景 */
  ROOM = 'ROOM',
  /** 对局进行中（GameScene 已创建且对局未结束） */
  GAME = 'GAME',
  /** 对局已结束（结算弹窗显示中 / 等待返回房间） */
  GAME_OVER = 'GAME_OVER',
}

let currentState: FlowState = FlowState.IDLE;

/** 清理回调类型（退出对局流时由 ws 层注册，用于清理 pending 缓冲消息） */
type CleanupCallback = () => void;
const cleanupFns: CleanupCallback[] = [];

/** 注册退出对局流时的清理回调（ws 层调用，支持多个注册者） */
export function registerFlowCleanup(fn: CleanupCallback): void {
  cleanupFns.push(fn);
}

/** 设置流程状态（场景 create / 关键转换点调用） */
export function setFlowState(state: FlowState): void {
  if (currentState === state) return;
  const prev = currentState;
  currentState = state;
  console.log(`[GameFlow] ${prev} → ${state}`);
  // 退出对局流时自动清理缓冲消息（防止 stale 游戏消息泄漏到下一局）
  if ((prev === FlowState.GAME || prev === FlowState.GAME_OVER)
      && state !== FlowState.GAME && state !== FlowState.GAME_OVER) {
    cleanupFns.forEach(fn => fn());
  }
}

/** 获取当前流程状态 */
export function getFlowState(): FlowState {
  return currentState;
}

/** 是否处于对局中（GAME 或 GAME_OVER） */
export function isInGameFlow(): boolean {
  return currentState === FlowState.GAME || currentState === FlowState.GAME_OVER;
}
