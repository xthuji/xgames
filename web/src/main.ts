import Phaser from 'phaser';
import { BootScene } from './platform/scenes/BootScene';
import { LobbyScene } from './platform/scenes/LobbyScene';
import { RoomScene } from './platform/scenes/RoomScene';
import { SettingsScene } from './platform/scenes/SettingsScene';
import { net } from './platform/net/ws';
import { store } from './platform/state/store';
import { setupUserDataClearedListener } from './platform/utils/sceneSync';
import { gameSceneClasses, currentGameSceneKey, listGames, getGame } from './platform/registry';

// 全局暴露（便于浏览器自动化测试与调试）
declare global {
  interface Window {
    game: Phaser.Game;
    net: typeof net;
    store: typeof store;
    /** 已注册游戏清单（外壳侧边栏读取的轻量视图，不含场景类） */
    gameList: Array<{ id: string; icon: string; name: string; singlePlayer?: boolean }>;
  }
}
window.net = net;
window.store = store;
window.gameList = listGames().map((g) => ({ id: g.id, icon: g.icon, name: g.name, singlePlayer: g.singlePlayer }));
// 首启时 localStorage 无记录：回写生效的默认游戏，触发外壳侧边栏高亮同步
store.setCurrentGame(store.currentGameID);

const game = new Phaser.Game({
  type: Phaser.WEBGL, // 强制使用 WebGL，性能远优于 Canvas 2D
  parent: 'game',
  width: 1280,
  height: 720,
  backgroundColor: '#182d3b',
  fps: {
    target: 30, // 限制帧率为 30 FPS，降低 CPU 占用
    forceSetTimeOut: true, // 强制使用setTimeout，更稳定的帧率控制
  },
  scale: {
    mode: Phaser.Scale.FIT,
    autoCenter: Phaser.Scale.CENTER_BOTH,
  },
  dom: {
    createContainer: true,
  },
  // WebGL 渲染优化
  render: {
    antialias: false, // 关闭抗锯齿，提升性能
    pixelArt: false,
    powerPreference: 'high-performance', // 请求高性能模式
    clearBeforeRender: false, // 不清除上一帧，减少GPU操作（适用于静态场景）
  },
  // 禁用不必要的物理引擎
  physics: {
    default: 'false',
  },
  // 音频后端：Web Audio（默认）。
  // 不能用 HTML5 Audio（disableWebAudio: true）：WKWebView/Safari 对无手势的
  // audio.play() 有严格限制（transient activation 短暂过期即拒绝），WS 消息回调中
  // 触发的走子音效会被静默拒绝（play 返回 true 但 promise 被拒被吞）→ 偶发/经常无声。
  // Web Audio 的 AudioContext 只需一次手势 resume（sticky），之后所有编程播放均可靠。
  // 注：历史上 WKWebView 的音频问题是 Ogg 解码不支持，与 Web Audio 无关；
  // 音频资源已统一 MP3，decodeAudioData 在 Safari/WKWebView 完全支持。
  // 场景表 = 平台场景 + 注册表里全部游戏的对局场景（平台不感知具体游戏）
  scene: [BootScene, LobbyScene, RoomScene, SettingsScene, ...gameSceneClasses()],
});
window.game = game;

// URL hash 路由：监听 hash 变化自动切换场景（便于浏览器自动化与直接访问）
window.addEventListener('hashchange', () => {
  const hash = location.hash.replace('#', '');
  // 同步外壳页面（主窗口）的 hash：场景运行在 iframe 内，
  // 外壳 URL 需保持一致以支持书签/分享与外层显示（同源，安全）
  try {
    if (window.parent && window.parent !== window && window.parent.location.hash !== location.hash) {
      window.parent.location.hash = location.hash;
    }
  } catch { /* 跨域环境忽略 */ }
  const sceneMap: Record<string, string> = {
    '/lobby': 'Lobby',
    '/room': 'Room',
    '/game': currentGameSceneKey(), // 按当前选择的游戏分发到其对局场景
    '/settings': 'Settings',
  };
  let target = sceneMap[hash];
  // 单机游戏直达路由 #/game/<id>：单机游戏共用 '/game' 会导致互切时
  // hash 不变、不触发 hashchange，因此每款单机游戏使用独立路由
  if (!target) {
    const m = /^\/game\/(.+)$/.exec(hash);
    const g = m && getGame(m[1]);
    if (g?.singlePlayer) {
      store.setCurrentGame(g.id);
      target = g.sceneKey;
    }
  }
  if (target && game.scene.scenes.length > 0) {
    // ── 防止重复启动/打断：目标场景已活跃或正在 create() 中则跳过 ──
    // 关键：场景 create() 执行中状态为 CREATING，isActive() 返回 false，
    // 而 create() 内部设置 location.hash 会触发本监听器，
    // 若只判 isActive 会误把自己正在创建的场景停掉再重启（消息流被打断）
    const targetScene = game.scene.getScene(target);
    const status = targetScene?.sys?.settings?.status;
    if (status === Phaser.Scenes.RUNNING || status === Phaser.Scenes.CREATING) return;
    // ── 外部（hash/外壳/自动化测试）触发的切换不在任何场景的事件循环内，
    // scene.start() 只会排队启动新场景而不会停止旧场景，
    // 必须显式停止所有活跃场景，否则会出现双场景同时运行（旧场景盖住新场景）
    for (const sc of game.scene.getScenes(true)) {
      sc.scene.stop();
    }
    console.log(`[URL路由] ${hash} → ${target}`);
    game.scene.start(target);
  }
});

// 外壳侧边栏切换多人游戏但 hash 未变化（如已在大厅时切到另一款多人游戏）：
// hashchange 不会触发，需在此重启大厅，按新 game_id 刷新房间列表/排行榜/统计
window.addEventListener('xgames:switch-game', () => {
  const lobby = game.scene.getScene('Lobby');
  if (lobby?.scene.isActive()) {
    // 大厅活跃时 start 即重启（同步 shutdown+start）。
    // 不能先 scene.stop() 再 start：stop 只是入队，会在下一帧把刚重启的大厅再停掉
    game.scene.start('Lobby');
    return;
  }
  // 兜底（此事件仅在大厅 hash 下派发，正常不会走到这里）：停掉其他活跃场景再进大厅
  for (const sc of game.scene.getScenes(true)) {
    sc.scene.stop();
  }
  game.scene.start('Lobby');
});

// 网络层连接（统一逻辑）
// 优先向服务端请求真实 WS 地址（/app-config.json）：
//   - Wails 生产模式：页面由 AssetServer 提供（无端口），且实际端口可能因占用而变化
//   - 无头/开发模式：同源请求同样命中该端点
// 获取失败时回退到同源/默认端口策略。
async function resolveWsUrl(): Promise<string> {
  try {
    const res = await fetch('/app-config.json');
    if (res.ok) {
      const cfg = (await res.json()) as { ws_url?: string };
      if (cfg.ws_url) return cfg.ws_url;
    }
  } catch {
    /* 回退到下方默认策略 */
  }
  const wsHost = location.port ? location.host : 'localhost:3030';
  return `ws://${wsHost}/ws`;
}
resolveWsUrl().then((url) => net.connect(url));
// 重连遮罩（技术设计 9.3）
const mask = document.getElementById('reconnect-mask')!;
net.onReconnecting = () => {
  mask.style.display = 'flex';
};
net.onOpen = () => {
  mask.style.display = 'none';
};
// 初始化场景状态同步监听器（只注册一次，避免重连时重复注册导致 handler 泄漏）
setupUserDataClearedListener();

export default game;
