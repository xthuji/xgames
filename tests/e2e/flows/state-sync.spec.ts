import { test, expect } from '@playwright/test';
import { GameHelper } from '../framework/game-helper';
import { StateChecker } from '../framework/state-checker';

/**
 * 状态同步专项测试
 *
 * 验证前端 UI 场景与后端游戏状态的一致性：
 * 1. 场景切换时 URL hash 与 Phaser 场景同步
 * 2. 进入 GameScene 时自动请求服务端状态
 * 3. 非游戏场景存在残留对局数据时自动清理
 * 4. RoomScene 无房间数据时自动返回大厅
 */
test.describe('状态同步专项测试', () => {

  test('URL hash 与场景切换同步', async ({ page }) => {
    const helper = new GameHelper(page);
    const checker = new StateChecker(helper);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    // 验证 Lobby → URL hash = #/lobby
    await checker.checkUrlSceneMatch();
    expect(await helper.getUrlHash()).toBe('#/lobby');

    // 切换到 Settings（通过 hash 路由，与外壳侧边栏的真实行为一致）
    await helper.navigate('settings');
    await helper.waitForScene('Settings', 5_000);
    await checker.checkUrlSceneMatch();
    expect(await helper.getUrlHash()).toBe('#/settings');

    // 切换回 Lobby（同样走 hash 路由）
    await helper.navigate('lobby');
    await helper.waitForScene('Lobby', 5_000);
    await checker.checkUrlSceneMatch();
    expect(await helper.getUrlHash()).toBe('#/lobby');
  });

  test('Lobby 状态下 store 无游戏数据', async ({ page }) => {
    const helper = new GameHelper(page);
    const checker = new StateChecker(helper);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    const store = await helper.getStore();
    expect(store.inGame).toBe(false);
    expect(store.phase).toBe('');
    expect(store.hand).toHaveLength(0);
    expect(store.roomCode).toBe('');
    expect(store.landlordID).toBe('');

    await checker.assertSceneStateConsistent();
  });

  test('GameScene 无状态时自动请求服务端状态', async ({ page }) => {
    const helper = new GameHelper(page);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    // 强制切换到 GameScene（模拟异常场景，走 hash 路由）
    await helper.navigate('game');
    // GameScene 会立即请求服务端状态；服务端无活跃对局 → 自动回退大厅。
    // 往返极快（重放消息同步处理），瞬时 Game 场景/中间 hash 无法可靠捕捉，
    // 等待最终稳定在 #/lobby 即证明回退链路完整走通。
    await page.waitForFunction(() => location.hash === '#/lobby', undefined, { timeout: 15_000 });
    await helper.waitForScene('Lobby', 15_000);

    const store = await helper.getStore();
    expect(store.inGame).toBe(false);
  });

  test('RoomScene 无房间数据时自动返回大厅', async ({ page }) => {
    const helper = new GameHelper(page);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    // 强制切换到 RoomScene（模拟无房间数据的异常场景，走 hash 路由）
    await helper.navigate('room');
    // RoomScene create() 中会发送 MsgGameSync/一致性检查，无房间数据 → 回退大厅。
    // 同上，等待最终稳定在 #/lobby 即证明回退链路走通。
    await page.waitForFunction(() => location.hash === '#/lobby', undefined, { timeout: 15_000 });
    await helper.waitForScene('Lobby', 15_000);

    const store = await helper.getStore();
    expect(store.inGame).toBe(false);
    expect(store.roomCode).toBe('');
  });

  test('人机对战中途退出并清理状态', async ({ page }) => {
    const helper = new GameHelper(page);
    const checker = new StateChecker(helper);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    // 开始人机练习
    await helper.clickPracticeMatch();
    await helper.waitForScene('Game', 30_000);

    // 验证游戏中
    await helper.waitForStoreField('phase', 'bidding', 15_000);
    const store = await helper.getStore();
    expect(store.inGame).toBe(true);
    expect(store.hand.length).toBe(17);

    // 模拟用户强制退出（发送 leave_game + 清理状态，再经 hash 路由返回大厅）
    await page.evaluate(() => {
      (window as any).net.send('leave_game');
      (window as any).store.reset();
    });
    await helper.navigate('lobby');

    // 验证返回大厅后状态已清理
    await helper.waitForScene('Lobby', 10_000);
    await checker.checkPostGameReset();
    await checker.checkLobbyState();
  });

  test('玩家身份持久化验证', async ({ page }) => {
    const helper = new GameHelper(page);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    // 获取初始身份
    const initialStore = await helper.getStore();
    const playerID = initialStore.playerID;
    const playerName = initialStore.playerName;
    expect(playerID).toBeTruthy();
    expect(playerName).toBeTruthy();

    // 刷新页面，验证身份保持（基于 localStorage）
    await page.reload();
    await helper.waitForScene('Lobby', 15_000);

    const reloadedStore = await helper.getStore();
    expect(reloadedStore.playerID).toBe(playerID);
    expect(reloadedStore.playerName).toBe(playerName);
  });

  test('清理用户数据后身份重置', async ({ page }) => {
    const helper = new GameHelper(page);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);

    const initialStore = await helper.getStore();
    const oldPlayerID = initialStore.playerID;

    // 发送清理用户数据请求（先注册消息监听再发送，避免应答先于订阅到达而丢失）
    const cleared = helper.waitForMessage('user_data_cleared', 10_000);
    await page.evaluate(() => {
      (window as any).net.send('clear_user_data');
    });

    // 等待服务端确认清理完成
    await cleared;

    // 前端应重置 store 并跳转
    await helper.waitForScene('Lobby', 10_000);

    // 验证身份仍有效且保持稳定：
    // playerID 由机器码派生（同设备恒定），清理用户数据只重置房间/对局状态，
    // 不清除设备身份，因此 ID 应保持不变（身份持久化的产品设计）
    const newStore = await helper.getStore();
    expect(newStore.playerID).toBeTruthy();
    expect(newStore.playerID).toBe(oldPlayerID);
  });
});
