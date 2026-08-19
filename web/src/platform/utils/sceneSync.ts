/**
 * 场景状态同步监控器
 * 
 * 功能：
 * 1. 检测当前 UI 场景与后端游戏状态是否一致
 * 2. 不一致时自动清理后端状态或切换到正确场景
 * 3. 防止场景残留导致的状态混乱
 */

import { store } from '../state/store';
import { net } from '../net/ws';
import { MsgTypes } from '../protocol';
import { setFlowState, getFlowState, FlowState } from '../flow/gameFlow';
import { isGameScene } from '../registry';

interface SceneStateCheck {
  currentScene: string;
  expectedState: 'none' | 'lobby' | 'room' | 'game';
  hasActiveRoom: boolean;
  hasActiveGame: boolean;
  stateReceived: boolean;
}

/**
 * 检查当前场景与后端状态是否匹配
 * 使用 GameFlow 状态机判断对局状态，确保与 UI 场景一致
 */
function checkSceneStateConsistency(currentScene: string): SceneStateCheck {
  const flow = getFlowState();
  return {
    currentScene,
    expectedState: determineExpectedState(currentScene),
    hasActiveRoom: !!store.roomCode && store.players.length > 0,
    hasActiveGame: flow === FlowState.GAME || flow === FlowState.GAME_OVER,
    stateReceived: store.stateReceived,
  };
}

/**
 * 根据场景名称推断期望的后端状态
 */
function determineExpectedState(scene: string): 'none' | 'lobby' | 'room' | 'game' {
  switch (scene) {
    case 'Lobby':
    case 'Settings':
      return 'lobby';
    case 'Room':
      return 'room';
    default:
      // 任意游戏的对局场景（由注册表登记，平台不感知具体游戏）
      return isGameScene(scene) ? 'game' : 'none';
  }
}

/**
 * 验证场景状态一致性，并在不一致时采取纠正措施
 */
export function validateAndFixSceneState(currentScene: string): void {
  const check = checkSceneStateConsistency(currentScene);
  
  console.log('[状态同步] 场景检查:', {
    scene: check.currentScene,
    expected: check.expectedState,
    hasRoom: check.hasActiveRoom,
    hasGame: check.hasActiveGame,
    stateReceived: check.stateReceived,
  });

  // 对局场景（任意游戏）但没有活跃对局或未收到状态 → 请求服务端状态
  if (isGameScene(currentScene) && (!check.hasActiveGame || !check.stateReceived)) {
    console.warn('[状态同步] 对局场景状态不完整，请求服务端状态...');
    net.send(MsgTypes.MsgRequestGameState);
    return;
  }

  // Lobby/Room/Settings 但有活跃对局 → 异常，强制清理
  if ((currentScene === 'Lobby' || currentScene === 'Room' || currentScene === 'Settings') && check.hasActiveGame) {
    console.error('[状态同步] 非游戏场景但存在活跃对局，强制清理...');
    clearUserDataAndRedirect('Lobby');
    return;
  }

  // RoomScene 但没有房间 → 返回大厅
  if (currentScene === 'Room' && !check.hasActiveRoom) {
    console.warn('[状态同步] RoomScene 无房间数据，返回大厅');
    redirectScene('Lobby');
    return;
  }

  console.log('[状态同步] ✅ 状态一致');
}

/**
 * 清理用户数据并重定向到指定场景
 * 等待服务端确认后跳转，超时 5s 兑底强制跳转避免永久卡住
 */
function clearUserDataAndRedirect(targetScene: string): void {
  console.log(`[状态同步] 清理用户数据并跳转到 ${targetScene}...`);
  
  let resolved = false;
  // 监听服务端清理完成消息（on 返回取消订阅函数）
  const unsub = net.on(MsgTypes.MsgUserDataCleared, () => {
    if (resolved) return;
    resolved = true;
    clearTimeout(timer);
    console.log('[状态同步] 服务端确认清理完成，执行跳转');
    store.reset();
    // ── 流程状态机：同步跳转时更新 flow 状态 ──
    setFlowState(flowOf(targetScene));
    redirectScene(targetScene);
    unsub(); // 取消订阅
  });
  
  // 超时兑底：5s 后强制跳转（避免 MsgUserDataCleared 永远不到达导致永久卡住）
  const timer = setTimeout(() => {
    if (resolved) return;
    resolved = true;
    console.warn('[状态同步] 等待 MsgUserDataCleared 超时，强制跳转');
    unsub();
    store.reset();
    setFlowState(flowOf(targetScene));
    redirectScene(targetScene);
  }, 5000);
  
  // 发送清理请求到服务端
  net.send(MsgTypes.MsgClearUserData);
}

/** 场景 key 对应的流程状态（对局场景按注册表泛化判断） */
function flowOf(scene: string): FlowState {
  const flowMap: Record<string, FlowState> = {
    'Lobby': FlowState.LOBBY, 'Room': FlowState.ROOM, 'Settings': FlowState.LOBBY,
  };
  if (flowMap[scene]) return flowMap[scene];
  return isGameScene(scene) ? FlowState.GAME : FlowState.LOBBY;
}

/**
 * 切换场景（带 URL hash 更新）
 * 停止所有其他活跃场景，避免多场景同时运行导致 UI 覆盖
 */
function redirectScene(scene: string): void {
  const sceneMap: Record<string, string> = {
    'Lobby': '/lobby',
    'Room': '/room',
    'Settings': '/settings',
  };
  // 任意游戏的对局场景统一走 /game 路由（由 main.ts 按当前游戏分发）
  const hash = sceneMap[scene] ?? (isGameScene(scene) ? '/game' : undefined);
  if (hash && location.hash !== `#${hash}`) {
    location.hash = hash;
  }
  
  // 触发 Phaser 场景切换：外部调用不在任何场景的事件循环内，
  // scene.start() 不会自动停止当前场景，必须先停止所有活跃场景，
  // 否则会出现双场景同时运行（如 Lobby 盖住 Settings）
  const game = (window as any).game;
  if (game?.scene) {
    const active = game.scene.getScenes(true);
    for (const sc of active) {
      if (sc.scene.key !== scene) {
        sc.scene.stop();
      }
    }
    game.scene.start(scene);
  }
}

/**
 * 监听服务端清理完成消息（全局初始化时调用）
 */
export function setupUserDataClearedListener(): void {
  net.on(MsgTypes.MsgUserDataCleared, () => {
    console.log('[状态同步] 服务端确认用户数据已清理');
  });
}
