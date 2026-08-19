import { test, expect } from '@playwright/test';
import { GameHelper } from '../framework/game-helper';
import { StateChecker } from '../framework/state-checker';

/**
 * 完整人机对战流程 E2E 测试
 *
 * 流程：大厅 → 人机匹配 → 叫分 → 加倍 → 出牌 → 结算 → 返回房间 → 退出到大厅
 *
 * 在每个关键阶段验证：
 * 1. 前端 store 状态正确
 * 2. URL hash 与场景匹配
 * 3. 场景状态一致性
 */
test.describe('人机对战完整流程', () => {
  test('完整人机对战流程 - 从大厅到游戏结束', async ({ page }) => {
    const helper = new GameHelper(page);
    const checker = new StateChecker(helper);

    // ── 1. 打开页面，等待大厅加载 ──
    await page.goto('/');
    await helper.waitForScene('Boot', 5_000).catch(() => {
      // Boot 场景可能很快完成，直接等 Lobby
    });
    await helper.waitForScene('Lobby', 15_000);
    await checker.checkLobbyState();
    await helper.log('大厅加载完成');

    // ── 2. 开始人机练习 ──
    await helper.clickPracticeMatch();
    await helper.log('已发送人机匹配请求');
    // 提前注入自动叫分策略：若等 phase='bidding' 后再注入，首个 bid_turn 可能
    // 已被 GameScene 消费，注入的 handler 收不到重放，导致真人不叫分、流局重发。
    await helper.injectAutoBid();

    // 等待进入 RoomScene（人机匹配会先创建房间）
    // 注意：人机流程中机器人自动准备极快，RoomScene 可能只是 <100ms 的过渡场景，
    // 轮询可能漏检；因此等待 Room 或 Game 任一活跃，再校验玩家数量。
    await page.waitForFunction(
      () => {
        const g = (window as any).game;
        const active = g?.scene?.getScenes(true) ?? [];
        return active.some((sc: any) => sc.scene.key === 'Room' || sc.scene.key === 'Game');
      },
      { timeout: 15_000 },
    );
    await checker.checkPlayerCount(3);
    await helper.log('进入房间/游戏，玩家数量正确');

    // ── 3. 等待游戏开始（RoomScene 会自动 ready） ──
    await helper.waitForScene('Game', 30_000);
    await checker.checkGameState();
    await helper.log('进入游戏场景');

    // ── 4. 验证发牌 ──
    await helper.waitForStoreField('phase', 'bidding', 15_000);
    const initialHand = await helper.getHandCards();
    // 自动叫分策略可能在读牌间隙已完成叫分：若真人成为地主则手牌为 20 张
    expect(initialHand.length === 17 || initialHand.length === 20).toBe(true);
    await checker.checkHandConsistency();
    await helper.log(`发牌完成，手牌 ${initialHand.length} 张`);

    // ── 5. 自动叫分策略已在开局前注入 ──
    await helper.log('自动叫分策略已就绪');

    // 等待叫分阶段结束（进入加倍或出牌阶段）
    await page.waitForFunction(
      () => {
        const s = (window as any).store;
        return s.phase === 'doubling' || s.phase === 'playing';
      },
      { timeout: 60_000 },
    );
    await helper.log(`叫分完成，当前阶段: ${await helper.getCurrentPhase()}`);

    // ── 6. 等待地主确定 ──
    await helper.waitForStoreFieldTruthy('landlordID', 15_000);
    await checker.checkLandlordSet();
    await checker.checkHandConsistency(); // 地主 20 张 / 农民 17 张
    const handAfterBid = await helper.getHandCards();
    await helper.log(`地主确定，手牌 ${handAfterBid.length} 张`);

    // ── 7. 注入自动出牌策略 ──
    await helper.injectAutoPlay();
    await helper.log('已注入自动出牌策略');

    // ── 8. 等待游戏结束 ──
    await helper.waitForStoreField('gameEnded', true, 180_000);
    await helper.log('游戏结束');

    // ── 9. 验证游戏结束状态 ──
    const finalStore = await helper.getStore();
    expect(finalStore.gameEnded).toBe(true);
    expect(finalStore.landlordID).toBeTruthy();
    expect(finalStore.phase).toBe('playing');
    await helper.screenshot('game-over');
    await helper.log('游戏结束状态验证通过');

    // ── 10. 点击"返回房间"按钮（通过 evaluate 模拟） ──
    await page.evaluate(() => {
      const store = (window as any).store;
      store.resetGame();
      const game = (window as any).game;
      const scene = game.scene.getScene('Game');
      scene.scene.start('Room');
    });

    // 等待进入 RoomScene
    await helper.waitForScene('Room', 10_000);
    await helper.log('返回房间');

    // ── 11. 退出房间到大厅 ──
    await page.evaluate(() => {
      (window as any).net.send('leave_room');
      (window as any).store.reset();
      const game = (window as any).game;
      const scene = game.scene.getScene('Room');
      scene.scene.start('Lobby');
    });

    // ── 12. 验证返回大厅后状态已重置 ──
    await helper.waitForScene('Lobby', 10_000);
    await checker.checkPostGameReset();
    await checker.checkLobbyState();
    await helper.log('返回大厅，状态已重置');

    await helper.screenshot('final-lobby');
    await helper.log('=== 完整人机对战流程测试通过 ===');
  });

  test('人机对战 - 验证各阶段 store 状态', async ({ page }) => {
    const helper = new GameHelper(page);
    const checker = new StateChecker(helper);

    // 打开页面
    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    // 验证初始状态
    let store = await helper.getStore();
    expect(store.playerID).toBeTruthy();
    expect(store.playerName).toBeTruthy();
    expect(store.inGame).toBe(false);
    expect(store.phase).toBe('');

    // 开始人机练习
    await helper.clickPracticeMatch();
    // 提前注入自动策略（叫分 + 出牌），避免首个 bid_turn 到达时 handler 尚未注册
    await helper.injectAutoPlayAll();
    await helper.waitForScene('Game', 30_000);

    // 验证游戏中状态
    await helper.waitForStoreField('phase', 'bidding', 15_000);
    store = await helper.getStore();
    expect(store.inGame).toBe(true);
    // 自动策略可能在读状态间隙已完成叫分：若真人成为地主则手牌为 20 张
    expect(store.hand.length === 17 || store.hand.length === 20).toBe(true);
    expect(store.players.length).toBe(3);

    // 等待地主确定
    await helper.waitForStoreFieldTruthy('landlordID', 30_000);
    // 地主确定后先进入加倍阶段（需 3 名玩家依次响应），再进入出牌阶段；
    // 不能立即断言 playing，需等待加倍流程走完。
    await helper.waitForStoreField('phase', 'playing', 30_000);
    store = await helper.getStore();
    expect(store.landlordID).toBeTruthy();
    expect(store.phase).toBe('playing');

    // 等待游戏结束
    await helper.waitForStoreField('gameEnded', true, 180_000);
    store = await helper.getStore();
    expect(store.gameEnded).toBe(true);

    await helper.screenshot('state-validation-complete');
  });
});
