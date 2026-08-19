import { Page } from '@playwright/test';

/**
 * GameHelper - Phaser 运行时操作封装
 *
 * 游戏运行在 Phaser canvas 中，无法通过 DOM 选择器点击游戏元素。
 * 所有操作通过 page.evaluate() 调用 window.game / window.store / window.net 实现。
 */
export class GameHelper {
  constructor(public readonly page: Page) {}

  // ─── 等待与轮询 ───────────────────────────────────────────

  /** 等待指定场景成为活跃场景 */
  async waitForScene(sceneName: string, timeout = 15_000): Promise<void> {
    await this.page.waitForFunction(
      (name: string) => {
        const g = (window as any).game;
        if (!g?.scene) return false;
        const active = g.scene.getScenes(true);
        return active.length > 0 && active[0].scene.key === name;
      },
      sceneName,
      { timeout },
    );
  }

  /** 等待 store 字段达到期望值 */
  async waitForStoreField(field: string, expected: any, timeout = 15_000): Promise<void> {
    await this.page.waitForFunction(
      ({ field, expected }: { field: string; expected: any }) => {
        const s = (window as any).store;
        return s && s[field] === expected;
      },
      { field, expected },
      { timeout },
    );
  }

  /** 等待 store 字段变为 truthy */
  async waitForStoreFieldTruthy(field: string, timeout = 15_000): Promise<void> {
    await this.page.waitForFunction(
      (field: string) => {
        const s = (window as any).store;
        return s && !!s[field];
      },
      field,
      { timeout },
    );
  }

  /** 等待收到指定类型的 WebSocket 消息（通过注入拦截器） */
  async waitForMessage(msgType: string, timeout = 15_000): Promise<any> {
    return this.page.evaluate(
      ({ msgType, timeout }: { msgType: string; timeout: number }) => {
        return new Promise((resolve, reject) => {
          const timer = setTimeout(() => {
            reject(new Error(`等待消息 ${msgType} 超时 (${timeout}ms)`));
          }, timeout);

          const net = (window as any).net;
          const unsub = net.on(msgType, (payload: any) => {
            clearTimeout(timer);
            unsub();
            resolve(payload);
          });
        });
      },
      { msgType, timeout },
    );
  }

  /**
   * 通过 URL hash 路由导航到指定场景（与外壳侧边栏的真实行为一致）。
   * 注意：不能直接调 game.scene.start()——SceneManager 层的 start()
   * 不会停止当前场景，会导致双场景同时运行。
   */
  async navigate(scene: 'lobby' | 'room' | 'game' | 'settings'): Promise<void> {
    await this.page.evaluate((s: string) => {
      const f = document.getElementById('game-frame') as HTMLIFrameElement;
      f.contentWindow!.location.hash = '/' + s;
    }, scene);
  }

  // ─── 大厅操作 ────────────────────────────────────────────

  /** 开始人机练习 */
  async clickPracticeMatch(difficulty = 'normal'): Promise<void> {
    await this.page.evaluate(
      (difficulty: string) => {
        (window as any).net.send('practice_match', { difficulty });
      },
      difficulty,
    );
  }

  // ─── 叫分/加倍 ───────────────────────────────────────────

  /** 叫分 (0=不叫, 1/2/3=叫分) */
  async bid(score: number): Promise<void> {
    await this.page.evaluate(
      (score: number) => {
        (window as any).net.send('bid', { score });
      },
      score,
    );
  }

  /** 加倍 */
  async double(yes: boolean): Promise<void> {
    await this.page.evaluate(
      (yes: boolean) => {
        (window as any).net.send('double', { double: yes });
      },
      yes,
    );
  }

  // ─── 出牌 ───────────────────────────────────────────────

  /** 出牌（按手牌索引） */
  async playCardsByIndices(indices: number[]): Promise<void> {
    await this.page.evaluate(
      (indices: number[]) => {
        const store = (window as any).store;
        const cards = indices.map((i: number) => store.hand[i]);
        (window as any).net.send('play_cards', { cards });
      },
      indices,
    );
  }

  /** 出牌（直接传牌数据） */
  async playCards(cards: any[]): Promise<void> {
    await this.page.evaluate((cards: any[]) => {
      (window as any).net.send('play_cards', { cards });
    }, cards);
  }

  /** 不出（pass） */
  async pass(): Promise<void> {
    await this.page.evaluate(() => {
      (window as any).net.send('pass', {});
    });
  }

  // ─── 状态读取 ───────────────────────────────────────────

  /** 获取 store 快照（序列化 Map/Set） */
  async getStore(): Promise<any> {
    return this.page.evaluate(() => {
      const s = (window as any).store;
      return {
        playerID: s.playerID,
        playerName: s.playerName,
        score: s.score,
        rank: s.rank,
        roomCode: s.roomCode,
        players: s.players,
        inGame: s.inGame,
        gameEnded: s.gameEnded,
        stateReceived: s.stateReceived,
        phase: s.phase,
        hand: s.hand,
        bottomCards: s.bottomCards,
        landlordID: s.landlordID,
        currentTurn: s.currentTurn,
        lastPlayed: s.lastPlayed,
        lastPlayerID: s.lastPlayerID,
        mustPlay: s.mustPlay,
        canBeat: s.canBeat,
        multiplier: s.multiplier,
        sessionScores: Object.fromEntries(s.sessionScores),
        afkPlayers: [...s.afkPlayers],
      };
    });
  }

  /** 获取当前活跃场景名称 */
  async getActiveScene(): Promise<string> {
    return this.page.evaluate(() => {
      const g = (window as any).game;
      const active = g?.scene?.getScenes(true);
      return active?.[0]?.scene?.key ?? 'unknown';
    });
  }

  /** 获取手牌 */
  async getHandCards(): Promise<any[]> {
    return this.page.evaluate(() => (window as any).store.hand);
  }

  /** 获取当前阶段 */
  async getCurrentPhase(): Promise<string> {
    return this.page.evaluate(() => (window as any).store.phase);
  }

  /** 获取玩家 ID */
  async getPlayerID(): Promise<string> {
    return this.page.evaluate(() => (window as any).store.playerID);
  }

  /** 获取当前 URL hash */
  async getUrlHash(): Promise<string> {
    return this.page.evaluate(() => location.hash);
  }

  // ─── 自动操作注入 ───────────────────────────────────────

  /**
   * 注入自动叫分策略到浏览器。
   * 监听 bid_turn 消息，轮到自己时自动应答。
   * 策略：叫分阶段叫 1 分（或 0 分不叫），加倍阶段一律不加倍。
   */
  async injectAutoBid(): Promise<void> {
    await this.page.evaluate(() => {
      const net = (window as any).net;
      const store = (window as any).store;

      // 标记已注入，避免重复
      if ((window as any).__e2e_auto_bid) return;
      (window as any).__e2e_auto_bid = true;

      net.on('bid_turn', (payload: any) => {
        if (payload.player_id !== store.playerID) return;

        if (payload.phase === 'double') {
          // 加倍阶段：一律不加倍
          net.send('double', { double: false });
        } else {
          // 叫分阶段：简单策略 - 叫 1 分
          const score = (payload.high_bid ?? 0) < 1 ? 1 : 0;
          net.send('bid', { score });
        }
      });
    });
  }

  /**
   * 注入自动出牌策略到浏览器。
   * 监听 play_turn 消息，轮到自己时自动选牌。
   * 策略：领出时出最小单张，跟牌时 pass。
   */
  async injectAutoPlay(): Promise<void> {
    await this.page.evaluate(() => {
      const net = (window as any).net;
      const store = (window as any).store;

      if ((window as any).__e2e_auto_play) return;
      (window as any).__e2e_auto_play = true;

      net.on('play_turn', (payload: any) => {
        if (payload.player_id !== store.playerID) return;

        const hand = store.hand;
        if (hand.length === 0) return;

        if (payload.must_play) {
          // 领出：出最小单张
          const sorted = [...hand].sort((a: any, b: any) => a.rank - b.rank);
          net.send('play_cards', { cards: [sorted[0]] });
        } else if (!payload.can_beat || !payload.last_played?.length) {
          // 无法跟牌：pass
          net.send('pass', {});
        } else {
          // 跟牌策略：简单 pass（不压）
          net.send('pass', {});
        }
      });
    });
  }

  /**
   * 注入完整的自动游戏策略（叫分 + 出牌）。
   * 一次性注入，适合完整对局流程测试。
   */
  async injectAutoPlayAll(): Promise<void> {
    await this.injectAutoBid();
    await this.injectAutoPlay();
  }

  // ─── 截图 ───────────────────────────────────────────────

  /** 截图并返回路径 */
  async screenshot(name: string): Promise<string> {
    const path = `test-results/screenshots/${name}.png`;
    await this.page.screenshot({ path });
    return path;
  }

  // ─── 工具方法 ────────────────────────────────────────────

  /** 等待指定毫秒 */
  async sleep(ms: number): Promise<void> {
    await this.page.waitForTimeout(ms);
  }

  /** 打印日志到浏览器控制台（便于调试） */
  async log(msg: string): Promise<void> {
    await this.page.evaluate((msg: string) => console.log(`[E2E] ${msg}`), msg);
  }
}
