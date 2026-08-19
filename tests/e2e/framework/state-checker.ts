import { expect } from '@playwright/test';
import { GameHelper } from './game-helper';

/**
 * StateChecker - UI/后端状态一致性校验器
 *
 * 在每个关键阶段验证前端 store 状态与后端状态的一致性，
 * 确保场景切换、数据同步、游戏流程的正确性。
 */
export class StateChecker {
  constructor(private helper: GameHelper) {}

  /**
   * 检查当前 URL hash 与活跃场景匹配。
   *
   * 场景 → URL hash 映射：
   * - Lobby  → #lobby
   * - Game   → #game
   * - Settings → #settings
   */
  async checkUrlSceneMatch(): Promise<void> {
    const scene = await this.helper.getActiveScene();
    const hash = await this.helper.getUrlHash();

    const expectedMap: Record<string, string> = {
      Lobby: '#/lobby',
      Game: '#/game',
      Settings: '#/settings',
      Room: '#/room',
    };

    const expectedHash = expectedMap[scene];
    if (expectedHash) {
      expect(hash).toBe(expectedHash);
    }
    // 其他场景（Boot/Preload）不做 URL 检查
  }

  /**
   * 检查 store 中的玩家数量正确。
   * 人机练习应有 3 个玩家。
   */
  async checkPlayerCount(expected: number = 3): Promise<void> {
    const store = await this.helper.getStore();
    expect(store.players).toHaveLength(expected);
  }

  /**
   * 检查手牌数量一致性。
   * 农民 17 张，地主 20 张（地主确定后）。
   */
  async checkHandConsistency(): Promise<void> {
    const store = await this.helper.getStore();
    const hand = store.hand;

    if (store.landlordID) {
      // 地主已确定
      const isLandlord = store.landlordID === store.playerID;
      if (isLandlord) {
        expect(hand.length).toBe(20);
      } else {
        expect(hand.length).toBe(17);
      }
    } else {
      // 地主未确定，应为 17 张
      expect(hand.length).toBe(17);
    }
  }

  /**
   * 检查地主确定后 landlordID 已设置。
   * 注意：地主确定后先进入加倍阶段（3 名玩家依次响应），再进入出牌阶段，
   * 不能立即断言 playing，需等待加倍流程走完。
   */
  async checkLandlordSet(): Promise<void> {
    const store = await this.helper.getStore();
    expect(store.landlordID).toBeTruthy();
    await this.helper.waitForStoreField('phase', 'playing', 30_000);
    const after = await this.helper.getStore();
    expect(after.phase).toBe('playing');
  }

  /**
   * 检查游戏结束后 store 已重置。
   */
  async checkPostGameReset(): Promise<void> {
    const store = await this.helper.getStore();
    expect(store.inGame).toBe(false);
    expect(store.gameEnded).toBe(false);
    expect(store.phase).toBe('');
    expect(store.hand).toHaveLength(0);
    expect(store.roomCode).toBe('');
  }

  /**
   * 通用断言：等待 store 字段达到期望值。
   */
  async assertStoreField(field: string, expected: any): Promise<void> {
    await this.helper.waitForStoreField(field, expected);
    const store = await this.helper.getStore();
    expect(store[field]).toBe(expected);
  }

  /**
   * 通用断言：场景状态一致性。
   *
   * 验证 store.inGame / store.roomCode / store.phase
   * 与当前场景 key 交叉一致。
   */
  async assertSceneStateConsistent(): Promise<void> {
    const store = await this.helper.getStore();
    const scene = await this.helper.getActiveScene();

    if (scene === 'Game') {
      // GameScene 应该 inGame=true 或有 phase
      expect(store.inGame || store.phase !== '').toBe(true);
    } else if (scene === 'Lobby') {
      // LobbyScene 应该 inGame=false
      expect(store.inGame).toBe(false);
      expect(store.phase).toBe('');
    }
  }

  /**
   * 检查 store.stateReceived 已标记。
   * 表示客户端已接收到服务端的状态消息。
   */
  async checkStateReceived(): Promise<void> {
    const store = await this.helper.getStore();
    expect(store.stateReceived).toBe(true);
  }

  /**
   * 检查玩家身份已设置。
   */
  async checkPlayerIdentity(): Promise<void> {
    const store = await this.helper.getStore();
    expect(store.playerID).toBeTruthy();
    expect(store.playerName).toBeTruthy();
  }

  /**
   * 综合检查：大厅状态完整性。
   */
  async checkLobbyState(): Promise<void> {
    await this.checkPlayerIdentity();
    await this.checkUrlSceneMatch();
    await this.assertSceneStateConsistent();
  }

  /**
   * 综合检查：游戏中状态完整性。
   */
  async checkGameState(): Promise<void> {
    await this.checkPlayerIdentity();
    await this.checkUrlSceneMatch();
    await this.assertSceneStateConsistent();
    await this.checkStateReceived();
  }
}
