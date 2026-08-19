import { test, expect } from '@playwright/test';
import { GameHelper } from '../framework/game-helper';

/**
 * 复盘按钮 E2E 验证：完整人机对局结束后，触发"复盘"逻辑，
 * 应创建复盘报告弹窗（Phaser 深度 1000 容器），而非失败 toast。
 */
test.describe('复盘报告', () => {
  test('对局结束后点击复盘按钮应展示报告', async ({ page }) => {
    const helper = new GameHelper(page);

    await page.goto('/');
    await helper.waitForScene('Lobby', 15_000);
    await helper.log('大厅加载完成');

    await helper.clickPracticeMatch();
    await helper.injectAutoPlayAll();
    await helper.waitForScene('Game', 30_000);
    await helper.log('进入游戏场景');

    await helper.waitForStoreField('gameEnded', true, 180_000);
    await helper.log('游戏结束');
    await page.waitForTimeout(1500); // 等结算弹窗渲染

    const frame = page.frames().find((f) => /game\.html/.test(f.url()));
    expect(frame, '应找到 game.html iframe').toBeTruthy();

    const outcome = await frame!.evaluate(async () => {
      const w = window as any;
      const s = w.store;
      const pre = { roomCode: s.roomCode, playerID: s.playerID };

      // 记录复盘 API 请求与响应状态
      const fetches: Array<{ url: string; status: number }> = [];
      const origFetch = w.fetch.bind(w);
      w.fetch = async (input: any, init?: any) => {
        const res = await origFetch(input, init);
        fetches.push({ url: String(input), status: res.status });
        return res;
      };

      const scene = w.game.scene.getScene('Game');
      (scene as any).showReplayDialog({});

      // 轮询 12s：递归遍历场景对象树（弹窗/toast 都在 Container 内），
      // 检查是否出现复盘报告弹窗（标题含"对局复盘"）或失败 toast
      const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
      const collectTexts = (objs: any[], out: string[], depth = 0) => {
        for (const o of objs ?? []) {
          if (typeof o?.text === 'string') out.push(o.text);
          if (depth < 5 && Array.isArray(o?.list)) collectTexts(o.list, out, depth + 1);
        }
      };
      for (let i = 0; i < 60; i++) {
        const texts: string[] = [];
        collectTexts(scene.children.getAll() as any[], texts);
        const modalHit = texts.find((t) => /对局复盘/.test(t));
        if (modalHit) return { ok: true, pre, fetches, text: modalHit };
        const toastHit = texts.find((t) => /加载复盘报告失败|暂无复盘报告/.test(t));
        if (toastHit) return { ok: false, pre, fetches, text: toastHit };
        await sleep(200);
      }
      return { ok: false, pre, fetches, text: '(12s 内无任何反馈)' };
    });

    await helper.log(`参数: ${JSON.stringify(outcome.pre)} fetches: ${JSON.stringify(outcome.fetches)}`);
    await helper.log(`复盘结果: ok=${outcome.ok} text=${outcome.text}`);
    expect(outcome.ok, `复盘报告应展示，实际: ${outcome.text}`).toBe(true);
  });
});
