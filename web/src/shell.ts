/**
 * 外壳页面脚本（主窗口）
 *
 * 布局：侧边栏（纯 DOM）+ 游戏 iframe（game.html，Phaser 画布独占剩余空间）。
 * 两者是物理隔离的兄弟区域，侧边栏永远不会遮挡游戏元素；
 * 场景内的所有坐标也不再需要为侧边栏预留空间。
 */

const sidebar = document.getElementById('sidebar')!;
const frame = document.getElementById('game-frame') as HTMLIFrameElement;
const toastEl = document.getElementById('shell-toast')!;

// ── Toast 提示 ──
let toastTimer: number | undefined;
function toast(msg: string) {
  toastEl.textContent = msg;
  toastEl.style.display = 'block';
  if (toastTimer) window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    toastEl.style.display = 'none';
  }, 2000);
}

// ── 侧边栏内容（视觉与原 Phaser buildSidebar 保持一致）──
const title = document.createElement('div');
title.className = 'title';
title.textContent = '游戏';
sidebar.appendChild(title);

// 游戏列表滚动容器
const gameList = document.createElement('div');
gameList.className = 'game-list';
sidebar.appendChild(gameList);

// 游戏清单来自游戏端注册表（iframe 内 window.gameList），外壳不感知具体游戏；
// 多人游戏（斗地主/五子棋/象棋/麻将）点击后进入大厅，单机游戏直接进入对局。
type GameItem = { id: string; icon: string; name: string; singlePlayer?: boolean };
const CURRENT_GAME_KEY = 'xgames_current_game';

function makeGameItem(g: GameItem): HTMLDivElement {
  const item = document.createElement('div');
  item.className = 'game-item';
  item.dataset.gameId = g.id;
  if (localStorage.getItem(CURRENT_GAME_KEY) === g.id) item.classList.add('current');

  const iconEl = document.createElement('div');
  iconEl.className = 'icon';
  iconEl.textContent = g.icon;
  const labelEl = document.createElement('div');
  labelEl.className = 'label';
  labelEl.textContent = g.name;
  item.appendChild(iconEl);
  item.appendChild(labelEl);

  item.addEventListener('click', () => {
    const store = (frame.contentWindow as any)?.store;
    if (!store) return;
    store.setCurrentGame(g.id); // 同步游戏端状态与 localStorage（同一 key）
    for (const el of sidebar.querySelectorAll('.game-item')) {
      el.classList.toggle('current', (el as HTMLElement).dataset.gameId === g.id);
    }
    // 单机游戏直接进入对局场景（不经大厅/房间）；
    // 多人游戏切换后回到大厅（房间/对局均属原游戏，不能继续停留）
    const target = g.singlePlayer ? `/game/${g.id}` : '/lobby';
    try {
      const win = frame.contentWindow;
      if (win) {
        if (win.location.hash !== `#${target}`) {
          win.location.hash = target;
        } else if (!g.singlePlayer) {
          // hash 未变化（如已在大厅时切换多人游戏）不会触发 hashchange，
          // 通知游戏端重启大厅，按新游戏刷新房间列表/排行榜/统计
          win.dispatchEvent(new CustomEvent('xgames:switch-game', { detail: { id: g.id } }));
        }
      }
    } catch { /* iframe 尚未加载完成时忽略 */ }
  });
  return item;
}

// iframe 加载完成后渲染游戏清单（外壳脚本先于游戏端执行，需等待）
let gameItemsRendered = false;
function renderGameItems() {
  if (gameItemsRendered) return;
  const list = (frame.contentWindow as any)?.gameList as GameItem[] | undefined;
  if (!list) {
    setTimeout(renderGameItems, 50);
    return;
  }
  gameItemsRendered = true;
  for (const g of list) gameList.appendChild(makeGameItem(g));
  syncCurrentHighlight();
}
// 侧边栏高亮同步：直达路由（#/game/<id>）初次加载时，游戏端会在侧边栏
// 渲染之后才写 localStorage 的当前游戏。用 storage 事件监听（同源 localStorage
// 跨文档触发），覆盖 iframe hash 未变化、不会触发 hashchange 的情况。
function syncCurrentHighlight() {
  const cur = localStorage.getItem(CURRENT_GAME_KEY);
  for (const el of sidebar.querySelectorAll('.game-item')) {
    el.classList.toggle('current', (el as HTMLElement).dataset.gameId === cur);
  }
}
window.addEventListener('storage', (e) => {
  if (e.key === CURRENT_GAME_KEY) syncCurrentHighlight();
});
frame.addEventListener('load', () => {
  renderGameItems();
  try {
    frame.contentWindow?.addEventListener('hashchange', syncCurrentHighlight);
  } catch { /* iframe 尚未就绪时忽略 */ }
});
renderGameItems(); // iframe 已缓存/先加载完成时直接尝试

// ── 底部按钮区（固定不滚动）──
const btnBar = document.createElement('div');
btnBar.className = 'sidebar-btns';
sidebar.appendChild(btnBar);

// "在浏览器打开"按钮（原位于大厅右上角，随侧边栏迁至外壳，全场景可用）──
const browserBtn = document.createElement('button');
browserBtn.className = 'sidebar-btn browser-btn';
browserBtn.textContent = '🌐';
browserBtn.title = '在浏览器打开';
browserBtn.addEventListener('click', async () => {
  // Wails 运行时绑定（生成的绑定位于 window.go.main.XGamesApp，主窗口可用）
  const bound = (window as any).go?.main?.XGamesApp?.OpenInBrowser;
  if (typeof bound === 'function') {
    try {
      bound();
      toast('已在浏览器中打开');
      return;
    } catch {
      /* fallthrough to fetch */
    }
  }
  // 无头/浏览器模式：由服务端按当前访问地址打开浏览器
  try {
    await fetch('/api/open-browser');
    toast('已在浏览器中打开');
  } catch {
    toast('打开浏览器失败');
  }
});
btnBar.appendChild(browserBtn);

// 设置按钮：进入/退出设置页（通过 iframe 的 hash 路由驱动场景切换）──
const settingsBtn = document.createElement('button');
settingsBtn.className = 'sidebar-btn';
settingsBtn.textContent = '⚙️';
settingsBtn.title = '设置';
settingsBtn.addEventListener('click', () => {
  const win = frame.contentWindow;
  if (!win) return;
  try {
    if (win.location.hash === '#/settings') {
      win.location.hash = '/lobby';
    } else {
      win.location.hash = '/settings';
    }
  } catch {
    /* iframe 尚未加载完成时忽略 */
  }
});
btnBar.appendChild(settingsBtn);

// ── E2E / 调试兼容：把游戏 iframe 的全局对象镜像到外壳 window ──
// 同源 iframe，用活 getter 透传（iframe 重载后依然有效），
// 现有基于 page.evaluate(window.game / window.store / window.net) 的
// Playwright 测试无需任何改造即可继续使用。
for (const key of ['game', 'store', 'net'] as const) {
  Object.defineProperty(window, key, {
    configurable: true,
    enumerable: true,
    get: () => (frame.contentWindow as any)?.[key],
  });
}

export {};
