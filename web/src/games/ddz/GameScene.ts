import Phaser from 'phaser';
import { net } from '../../platform/net/ws';
import { store } from '../../platform/state/store';
import { showBubble } from '../../platform/ui/bubble';
import { CardCounter } from './ui/counter';
import { safePlay } from '../../platform/ui/audio';
import { BACK_FRAME, cardFrame, handLayoutXs, removeCards, sortHandDesc } from './ui/cards';
import { hintAll } from './ui/rule';
import { setFlowState, FlowState } from '../../platform/flow/gameFlow';
import { fetchLatestReplayText, REPLAY_NOT_READY } from '../../platform/replay/replayApi';
import {
  MsgTypes,
  AfkChangedPayload,
  AfkPayload,
  BidPayload,
  BidTurnPayload,
  BidResultPayload,
  CardInfo,
  CardPlayedPayload,
  DealCardsPayload,
  DoublePayload,
  ErrorPayload,
  GameOverExtra,
  GameOverPayload,
  GameStartPayload,
  GameStateDTO,
  GameStatePayload,
  GameSyncResultPayload,
  LandlordPayload,
  LeaveGamePayload,
  MaintenancePayload,
  PlayCardsPayload,
  PlayerPassPayload,
  PlayTurnPayload,
  StatsResultPayload,
} from '../../platform/protocol';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;

/** 面板坐标：[自己, 左(上家), 右(下家)]
 *  侧边栏已迁至外壳页面（iframe 外），画布左侧无需预留避让空间 */
const PANEL_POS = [
  { x: 120, y: 560 },
  { x: 120, y: 200 },
  { x: 1160, y: 200 },
];
/** 出牌展示区 */
const PLAY_POS = [
  { x: 640, y: 450 },
  { x: 330, y: 300 },
  { x: 950, y: 300 },
];

/** GameScene 牌桌：手牌交互、叫分加倍、出牌动画、记牌器、结算弹窗 */
export class GameScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];
  private counter = new CardCounter();

  /** 视图下标：0=自己 1=上家 2=下家 */
  private viewOf = new Map<string, number>();
  private panelNames: Phaser.GameObjects.Text[] = [];
  private panelCounts: Phaser.GameObjects.Text[] = [];
  private panelScores: Phaser.GameObjects.Text[] = [];
  private panelIcons: Phaser.GameObjects.Image[] = [];
  private panelBidStatus: Phaser.GameObjects.Text[] = [];
  private playAreas: Phaser.GameObjects.Container[] = [];

  private handContainer!: Phaser.GameObjects.Container;
  private handSprites = new Map<string, Phaser.GameObjects.Sprite>();
  private selected = new Set<string>();
  private bottomSprites: Phaser.GameObjects.Sprite[] = [];

  private bidPanel!: Phaser.GameObjects.Container;
  private bidText!: Phaser.GameObjects.Text;
  private bidButtons: Phaser.GameObjects.Container[] = [];
  private playPanel!: Phaser.GameObjects.Container;
  private passBtn!: Phaser.GameObjects.Image;
  private shotBtn!: Phaser.GameObjects.Image;
  private reselectBtn!: Phaser.GameObjects.Container;
  private turnText!: Phaser.GameObjects.Text;
  private multiplierText!: Phaser.GameObjects.Text;
  private counterPanel!: Phaser.GameObjects.Container;
  private counterTexts = new Map<number, Phaser.GameObjects.Text>();
  private timerEvent?: Phaser.Time.TimerEvent;
  private maintenanceOverlay?: Phaser.GameObjects.Container;
  private soundToggle!: Phaser.GameObjects.Text;
  private afkToggleBg!: Phaser.GameObjects.Graphics;
  private afkToggleTxt!: Phaser.GameObjects.Text;
  private scoreText!: Phaser.GameObjects.Text;
  private hintIndex = 0;
  private hintList: CardInfo[][] = [];
  private statsOverlay?: Phaser.GameObjects.Container;
  private gameEndedNormally = false; // 对局是否正常结束（用于 shutdown 判断是否需通知服务端终止）
  private syncTimer?: Phaser.Time.TimerEvent; // 周期性同步检测定时器
  private stateReceived = false; // 是否已收到服务端对局状态（同步拉取模式）
  private enterEpoch = 0; // 进入 GameScene 时的 gameStartEpoch，用于过滤过期 MsgGameStart 重放
  private autoPassTimer?: Phaser.Time.TimerEvent; // 无牌可出时延迟 2s 自动过
  // 语音队列（来源含席位标识，顺序播报不覆盖）
  private lastSpeakText = '';
  private lastSpeakSource = '';
  private lastWarningByPlayer = new Map<string, string>(); // 按席位追踪警告文本，防止被其他语音插队导致去重失效
  private speechQueue: Array<{ text: string; source: string; pitch: number }> = [];
  private speechPlaying = false;
  // 框选
  private dragging = false;
  private dragStartX = 0;
  private dragStartY = 0;
  private dragMoved = false;
  private dragToggled = new Set<string>(); // 框选中已切换过状态的牌（防止重复切换）
  private dragRect?: Phaser.GameObjects.Rectangle;
  private handXs: number[] = [];

  constructor() {
    super('Game');
  }

  create() {
    // ── 关键防护：检测重复创建 ──
    // hashchange 路由可能触发 GameScene 被 scene.start 两次，
    // 导致两个 GameScene 实例同时存在。第一个 shutdown 时会发送 MsgLeaveGame 终止对局。
    // 此处检测是否已有另一个活跃的 GameScene，若有则立即停止自身。
    const allScenes: Phaser.Scene[] = this.scene.manager.scenes;
    const otherGame = allScenes.find(
      (s: Phaser.Scene) => s !== this && s instanceof GameScene && s.sys.isActive(),
    );
    if (otherGame) {
      console.warn('[GameScene] 检测到重复创建，立即停止自身');
      this.scene.stop(this);
      return;
    }

    // ── 重置场景实例残留状态 ──
    // Phaser 场景实例是单例复用的，scene.start 重启时只重跑 create()，
    // 实例字段不会自动复位：
    //   - 上局残留 gameEndedNormally=true → MsgGameState/MsgGameSyncResult/500ms 安全网
    //     全部被跳过，新一局拿不到服务端权威状态，UI 卡死在空牌桌；
    //   - stateReceived 残留 → shutdown 守卫误判，异常退出时不通知服务端；
    //   - selected/hintList/语音去重等残留 → 新局手牌被预选、提示错位、首条语音被吞。
    this.gameEndedNormally = false;
    this.stateReceived = false;
    this.selected.clear();
    this.hintIndex = 0;
    this.hintList = [];
    this.lastSpeakText = '';
    this.lastSpeakSource = '';
    this.lastWarningByPlayer.clear();
    this.speechQueue = [];
    this.speechPlaying = false;
    this.dragging = false;
    this.dragRect = undefined;
    this.maintenanceOverlay = undefined;
    this.statsOverlay = undefined;
    this.timerEvent = undefined;
    this.autoPassTimer = undefined;
    this.syncTimer = undefined;

    this.cameras.main.setBackgroundColor('#0c3a2a');
    this.add.image(640, 360, 'bg').setScale(0.8);

    this.buildPanels();
    this.buildTable();
    this.buildBidPanel();
    this.buildPlayPanel();
    this.buildCounterPanel();
    this.buildScoreDisplay();
    this.buildSoundToggle();
    this.buildAfkToggle();
    this.buildExitButton();
    // 侧边栏已由外壳页面（shell.ts，纯 DOM）提供，与游戏画布物理隔离

    // ── 流程状态机：标记进入对局 ──
    setFlowState(FlowState.GAME);

    // ── 记录进入时的对局代次（必须在 subscribe 之前，否则 epoch 守卫会拒绝重放消息）──
    this.enterEpoch = store.gameStartEpoch;

    // ── 订阅消息（REPLAYABLE 缓冲的消息在此重放）──
    // 注意：不再调用 net.clearPending()，状态机在退出 GAME 时已自动清理
    // （registerFlowCleanup）。此处 clearPending 会误删当前局的缓冲消息
    // （如 LobbyScene → GameScene 直达路径中 MsgDealCards 等已缓冲的消息）
    this.subscribe();
    this.input.on('pointerdown', (p: Phaser.Input.Pointer) => this.onPointerDown(p));
    this.input.on('pointermove', (p: Phaser.Input.Pointer) => this.onPointerMove(p));
    this.input.on('pointerup', () => this.onPointerUp());
    
    // 安全网：确保 viewOf 已建立（关键修复：GameScene 创建时可能已错过 MsgGameStart）
    if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
      this.rebuildViewMapping();
    }
    
    this.renderAll();

    if (store.phase === 'playing') safePlay(this, 'deal');
    if (store.maintenance) this.showMaintenanceOverlay();

    // 全部初始化成功后才标记：确保 RoomScene 兑底机制能正确判断
    store.inGame = true;

    // ── 延迟安全网：确保 pending 消息排空后 UI 正确渲染 ──
    // 覆盖场景：buildPanels() 时 store.playerID 尚未设置（极端时序）
    queueMicrotask(() => {
      if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
        console.warn('[GameScene] viewOf 为空，延迟重建');
        this.rebuildViewMapping();
        this.renderPanels();
      }
      // 若手牌已到账但初始渲染时为空，补渲染
      if (store.hand.length > 0 && this.handContainer.length <= 1) {
        this.renderHand();
        this.renderCounter();
      }
      // 确保上家出牌已渲染
      if (store.lastPlayed.length > 0) {
        this.renderLastPlayed();
      }
    });

    // ── 500ms 延迟安全网：确保所有 UI 元素正确显示 ──
    // 覆盖场景：消息处理延迟、状态恢复延迟等
    this.time.delayedCall(500, () => {
      if (this.gameEndedNormally) return;
      // 重建视图映射（如果仍为空）
      if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
        console.warn('[GameScene] 500ms 延迟：viewOf 仍为空，强制重建');
        this.rebuildViewMapping();
      }
      // 重新渲染面板（用户名/积分/头像）
      this.renderPanels();
      // 重新渲染上家出牌
      this.renderLastPlayed();
      // 重新渲染手牌（如果已到账但未渲染）
      if (store.hand.length > 0 && this.handContainer.length <= 1) {
        this.renderHand();
        this.renderCounter();
      }
    });

    // URL 路由：标记当前场景，便于浏览器自动化与直接访问
    if (location.hash !== '#/game') location.hash = '/game';

    // Phaser 3.90 不会自动调用场景子类的 shutdown()，需手动接线，
    // 否则切场景时订阅/定时器/音效永不清理，且异常退出时不会通知服务端。
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());

    // ── 主动拉取权威状态 ──
    // 通过 MsgRequestGameState 获取服务端权威对局状态，
    // 确保 viewOf / store.players / store.hand 等数据正确初始化。
    // 注意：不再发送初始 MsgGameSync，避免与 MsgGameState 响应竞态
    // （MsgGameSyncResult 先于 MsgGameState 到达时可能误触发 goToLobby）
    net.send(MsgTypes.MsgRequestGameState);

    // ── 周期性同步检测（延迟启动，等状态恢复完成后再检测）──
    this.syncTimer = this.time.addEvent({
      delay: 30000, // 30秒同步一次，降低CPU占用
      loop: true,
      callback: () => {
        if (!this.gameEndedNormally) net.send(MsgTypes.MsgGameSync);
      },
    });
  }

  private buildScoreDisplay() {
    this.scoreText = this.add.text(90, 14, `🎯 ${store.score}  🏆 ${store.rank || '—'}`, {
      ...FONT, fontSize: '18px', color: '#ffd54f',
    });
  }

  private buildSoundToggle() {
    this.soundToggle = this.add.text(1268, 40, store.muted ? '🔇 静音' : '🔊 音效', {
      ...FONT, fontSize: '16px', color: store.muted ? '#aaaaaa' : '#ffd54f',
    }).setOrigin(1, 0.5).setDepth(60).setInteractive({ useHandCursor: true });
    this.soundToggle.on('pointerdown', () => {
      store.toggleMuted();
      this.sound.setMute(store.muted);
      this.soundToggle.setText(store.muted ? '🔇 静音' : '🔊 音效');
      this.soundToggle.setColor(store.muted ? '#aaaaaa' : '#ffd54f');
    });
  }

  /** 挂机开关：挂机后由机器人策略自动出牌，主动操作或点击取消即可恢复手动 */
  private buildAfkToggle() {
    const container = this.add.container(1200, 644).setDepth(60);
    const bg = this.add.graphics();
    this.drawAfkBtnNormal(bg);
    const interactive = this.add.rectangle(0, 0, 110, 36, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    const txt = this.add.text(0, 0, '💤 托管', { fontFamily: 'Arial', fontSize: '15px', color: '#ffffff', fontStyle: 'bold' }).setOrigin(0.5);
    container.add([bg, interactive, txt]);

    interactive.on('pointerover', () => this.tweens.add({ targets: container, scale: 1.05, duration: 100 }));
    interactive.on('pointerout', () => this.tweens.add({ targets: container, scale: 1, duration: 100 }));
    interactive.on('pointerdown', () => {
      if (store.gameEnded) return; // 对局结束后不允许切换挂机状态
      const afk = !store.afkPlayers.has(store.playerID);
      // 本地先行切换按钮态（乐观更新），确保页面文案立即变为"取消托管"，
      // 不必等待服务端 MsgAfkChanged 网络往返；服务端回执幂等，再同步一次。
      if (afk) store.afkPlayers.add(store.playerID);
      else store.afkPlayers.delete(store.playerID);
      this.renderAfkToggle();
      net.send(MsgTypes.MsgAfk, { afk } satisfies AfkPayload);
    });

    this.afkToggleBg = bg;
    this.afkToggleTxt = txt;
    this.renderAfkToggle();
  }

  /** 托管按钮普通状态（蓝色渐变光泽，与麻将一致） */
  private drawAfkBtnNormal(g: Phaser.GameObjects.Graphics) {
    g.clear();
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-55 + 3, -18 + 5, 110, 36, 10);
    g.fillStyle(0x1565c0, 1);
    g.fillRoundedRect(-55, -18, 110, 36, 10);
    g.fillStyle(0x42a5f5, 0.5);
    g.fillRoundedRect(-55, -18, 110, 18, 10);
    g.lineStyle(2, 0xffffff, 0.8);
    g.strokeRoundedRect(-55, -18, 110, 36, 10);
    g.lineStyle(1, 0xffffff, 0.4);
    g.strokeRoundedRect(-52, -16, 104, 10, 8);
  }

  /** 托管按钮激活状态（橙色渐变光泽，与麻将一致） */
  private drawAfkBtnActive(g: Phaser.GameObjects.Graphics) {
    g.clear();
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-55 + 3, -18 + 5, 110, 36, 10);
    g.fillStyle(0xe65100, 1);
    g.fillRoundedRect(-55, -18, 110, 36, 10);
    g.fillStyle(0xff9800, 0.5);
    g.fillRoundedRect(-55, -18, 110, 18, 10);
    g.lineStyle(2, 0xffffff, 0.8);
    g.strokeRoundedRect(-55, -18, 110, 36, 10);
    g.lineStyle(1, 0xffffff, 0.4);
    g.strokeRoundedRect(-52, -16, 104, 10, 8);
  }

  private renderAfkToggle() {
    const myAfk = store.afkPlayers.has(store.playerID);
    if (this.afkToggleTxt && this.afkToggleBg) {
      this.afkToggleTxt.setText(myAfk ? '取消托管' : '💤 托管');
      if (myAfk) this.drawAfkBtnActive(this.afkToggleBg);
      else this.drawAfkBtnNormal(this.afkToggleBg);
    }
  }

  /** 退出按钮：即时终止对局并返回大厅，确保 UI 与后台同步 */
  private buildExitButton() {
    const exitContainer = this.add.container(1200, 690).setDepth(60);

    const g = this.add.graphics();
    // 红色渐变光泽（与麻将退出按钮一致）
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-48 + 3, -18 + 5, 96, 36, 10);
    g.fillStyle(0xc62828, 1);
    g.fillRoundedRect(-48, -18, 96, 36, 10);
    g.fillStyle(0xef5350, 0.5);
    g.fillRoundedRect(-48, -18, 96, 18, 10);
    g.lineStyle(2, 0xffffff, 0.8);
    g.strokeRoundedRect(-48, -18, 96, 36, 10);
    g.lineStyle(1, 0xffffff, 0.4);
    g.strokeRoundedRect(-45, -16, 90, 10, 8);

    const interactive = this.add.rectangle(0, 0, 96, 36, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });

    const txt = this.add.text(0, 0, '🚪 退出', { fontFamily: 'Arial', fontSize: '15px', color: '#ffffff', fontStyle: 'bold' }).setOrigin(0.5);

    exitContainer.add([g, interactive, txt]);

    interactive.on('pointerover', () => this.tweens.add({ targets: exitContainer, scale: 1.05, duration: 100 }));
    interactive.on('pointerout', () => this.tweens.add({ targets: exitContainer, scale: 1, duration: 100 }));
    interactive.on('pointerdown', () => {
      // 先通知服务端终止对局（必须在 resetGame 之前，否则 store.inGame 被重置后 shutdown 不会发送）
      if (store.inGame && !store.gameEnded) {
        net.send(MsgTypes.MsgLeaveGame, {} satisfies LeaveGamePayload);
      }
      this.gameEndedNormally = true; // 标记已主动处理，防止 shutdown 重复发送
      store.resetGame();
      // ── 流程状态机：返回大厅 ──
      setFlowState(FlowState.LOBBY);
      this.scene.start('Lobby');
    });
  }

  shutdown() {
    this.sound.stopAll();
    try { window.speechSynthesis?.cancel(); } catch { /* ignore */ }
    this.timerEvent?.remove();
    this.syncTimer?.remove();
    this.cancelAutoPass();
    this.speechQueue = [];
    this.speechPlaying = false;
    this.lastWarningByPlayer.clear();
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
    // 对局未正常结束 且 未收到状态恢复 → 通知服务端终止，确保 UI 与后台同步
    // stateReceived=true 表示通过同步拉取进入，对局在服务端确实活跃，不应终止
    if (!this.gameEndedNormally && !this.stateReceived && store.inGame) {
      net.send(MsgTypes.MsgLeaveGame, {} satisfies LeaveGamePayload);
    }
  }

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 斗地主是事件驱动场景，不需要每帧更新
    // 所有变化都通过网络消息、计时器、点击等事件触发
  }

  /** 同步检测失败：重置状态并返回大厅，让用户可以手动操作 */
  private goToLobby() {
    this.gameEndedNormally = true; // 避免 shutdown 再次发送 MsgLeaveGame
    store.reset();
    // ── 流程状态机：回退大厅 ──
    setFlowState(FlowState.LOBBY);
    this.scene.start('Lobby');
  }

  // --- 布局构建 ---

  private buildPanels() {
    // ── 关键修复：清空数组，防止场景重复创建时数组膨胀 ──
    // hashchange 路由可能触发 GameScene 重复创建，导致 panelNames 等数组
    // 累积 6 个元素（前 3 个属于已 shutdown 的旧场景，不可见；后 3 个属于新场景）。
    // renderPanels() 只更新索引 0-2，用户看到的却是索引 3-5（空内容）。
    this.panelIcons = [];
    this.panelCounts = [];
    this.panelNames = [];
    this.panelScores = [];
    this.panelBidStatus = [];
    // 视图下标映射：0=自己，1=上家(seat+2)，2=下家(seat+1)
    this.rebuildViewMapping();

    const PANEL_DEPTH = 15; // 面板元素深度（高于背景，低于出牌区和手牌）
    for (let i = 0; i < 3; i++) {
      const { x, y } = PANEL_POS[i];
      const isSelf = i === 0;
      // 头像区域（透明可点击区域，无深色底）
      const hitArea = this.add.circle(x, y, 30, 0x000000, 0).setStrokeStyle(0).setInteractive({ useHandCursor: true }).setDepth(PANEL_DEPTH);
      const vi = i;
      hitArea.on('pointerdown', () => this.showPlayerStats(vi));
      // 角色头像（地主/农民/默认）
      this.panelIcons.push(this.add.image(x, y, 'btn', 'icon_default.png').setScale(0.7).setDepth(PANEL_DEPTH));
      // 剩余张数（头像右上角小徽章）
      this.panelCounts.push(this.add.text(x + 26, y - 26, '', {
        ...FONT, fontSize: '12px', color: '#ffd54f', backgroundColor: '#00000088', padding: { x: 3, y: 1 },
      }).setOrigin(0.5).setDepth(PANEL_DEPTH));
      // 用户名（头像正下方）
      const nameStyle = { ...FONT, fontSize: isSelf ? '15px' : '13px', color: isSelf ? '#ffe082' : '#ffffff', stroke: '#000000', strokeThickness: 2 } as Phaser.Types.GameObjects.Text.TextStyle;
      this.panelNames.push(this.add.text(x, y + 38, '', nameStyle).setOrigin(0.5).setDepth(PANEL_DEPTH));
      // 积分/角色信息（名字下方）
      this.panelScores.push(this.add.text(x, y + 56, '', { ...FONT, fontSize: '12px', color: '#81d4fa', stroke: '#000000', strokeThickness: 1 }).setOrigin(0.5).setDepth(PANEL_DEPTH));
      // 叫分状态（积分下方）
      this.panelBidStatus.push(this.add.text(x, y + 74, '', { ...FONT, fontSize: '12px', color: '#ff8a80', stroke: '#000000', strokeThickness: 1 }).setOrigin(0.5).setDepth(PANEL_DEPTH));
    }
  }

  /** 重建视图映射（viewOf）：当 store.players 延迟到达时重新计算座位→视图下标 */
  private rebuildViewMapping() {
    this.viewOf.clear();
    // ── 关键修复：如果 playerID 为空但 players 有数据，尝试从 players 中恢复 ──
    if (!store.playerID && store.players.length > 0) {
      const human = store.players.find((p) => !p.is_bot);
      if (human) {
        store.playerID = human.id;
        console.warn('[rebuildViewMapping] store.playerID 为空，从 players 中恢复:', human.id);
      }
    }
    let mySeat = store.seatOf(store.playerID);
    // ── 增强兜底：playerID 已设但不在 players 中（时序竞态），用第一个非机器人玩家 ──
    if (mySeat < 0 && store.players.length > 0) {
      const human = store.players.find((p) => !p.is_bot);
      if (human) {
        if (!store.playerID) store.playerID = human.id;
        mySeat = human.seat;
        console.warn('[rebuildViewMapping] playerID 不在 players 中，使用 human seat:', mySeat);
      } else {
        // 全是机器人（极端情况），用 players[0] 的座位
        mySeat = store.players[0].seat;
        console.warn('[rebuildViewMapping] 无人类玩家，使用 players[0] seat:', mySeat);
      }
    }
    if (mySeat < 0) {
      console.warn('[rebuildViewMapping] mySeat < 0, playerID:', store.playerID, 'players:', store.players.map(p => p.id));
      return;
    }
    for (const p of store.players) {
      const delta = (p.seat - mySeat + 3) % 3;
      this.viewOf.set(p.id, delta === 0 ? 0 : delta === 2 ? 1 : 2);
    }
  }

  private buildTable() {
    // ── 清空数组，防止场景重复创建时数组膨胀 ──
    this.bottomSprites = [];
    this.playAreas = [];
    // 底牌区（顶部中央 3 张牌背）
    for (let i = 0; i < 3; i++) {
      const s = this.add.sprite(580 + i * 60, 56, 'poker', BACK_FRAME).setScale(0.55);
      this.bottomSprites.push(s);
    }
    this.multiplierText = this.add.text(640, 120, '', { ...FONT, fontSize: '20px', color: '#ffd54f' }).setOrigin(0.5);
    this.turnText = this.add.text(640, 330, '', { ...FONT, fontSize: '22px', color: '#ffffff', backgroundColor: '#00000066', padding: { x: 10, y: 4 } })
      .setOrigin(0.5).setDepth(30);

    // 各家出牌展示区
    for (let i = 0; i < 3; i++) {
      this.playAreas.push(this.add.container(PLAY_POS[i].x, PLAY_POS[i].y).setDepth(10));
    }

    // 自己的手牌容器
    this.handContainer = this.add.container(640, 660).setDepth(20);
  }

  private buildBidPanel() {
    this.bidPanel = this.add.container(640, 560).setDepth(40).setVisible(false);
    this.bidText = this.add.text(0, -50, '', { ...FONT, fontSize: '22px', color: '#ffd54f' }).setOrigin(0.5);
    this.bidPanel.add([this.bidText]);
  }

  /** 根据阶段动态渲染叫分 / 加倍按钮 */
  private showBidChoices(phase: string, highBid: number) {
    this.clearBidChoices();
    if (phase === 'double') {
      this.bidText.setText('是否加倍？');
      const yes = this.bidButton(-80, '加倍', () => net.send(MsgTypes.MsgDouble, { double: true } satisfies DoublePayload));
      const no = this.bidButton(80, '不加倍', () => net.send(MsgTypes.MsgDouble, { double: false } satisfies DoublePayload));
      this.bidButtons = [yes, no];
    } else {
      this.bidText.setText(highBid > 0 ? `轮到你叫分（当前最高 ${highBid} 分）` : '轮到你叫分');
      const pass = this.bidButton(-210, '不叫', () => net.send(MsgTypes.MsgBid, { score: 0 } satisfies BidPayload));
      const b1 = this.bidButton(-70, '1分', () => net.send(MsgTypes.MsgBid, { score: 1 } satisfies BidPayload), highBid < 1);
      const b2 = this.bidButton(70, '2分', () => net.send(MsgTypes.MsgBid, { score: 2 } satisfies BidPayload), highBid < 2);
      const b3 = this.bidButton(210, '3分', () => net.send(MsgTypes.MsgBid, { score: 3 } satisfies BidPayload), highBid < 3);
      this.bidButtons = [pass, b1, b2, b3];
    }
    this.bidButtons.forEach((b) => this.bidPanel.add(b));
  }

  private clearBidChoices() {
    this.bidButtons.forEach((b) => b.destroy(true));
    this.bidButtons = [];
  }

  private bidButton(x: number, label: string, cb: () => void, enabled = true): Phaser.GameObjects.Container {
    const bg = this.add.rectangle(x, 0, 120, 44, enabled ? 0xc62828 : 0x555555)
      .setStrokeStyle(1, 0xffffff, 0.5);
    if (enabled) {
      bg.setInteractive({ useHandCursor: true });
      bg.on('pointerdown', () => {
        this.bidPanel.setVisible(false);
        this.clearBidChoices();
        cb();
      });
    }
    const t = this.add.text(x, 0, label, { ...FONT, fontSize: '22px', color: enabled ? '#ffffff' : '#999999' }).setOrigin(0.5);
    return this.add.container(0, 0, [bg, t]);
  }

  private buildPlayPanel() {
    this.playPanel = this.add.container(640, 540).setDepth(40).setVisible(false);
    this.passBtn = this.add.image(-210, 0, 'btn', 'pass.png').setInteractive({ useHandCursor: true });
    this.passBtn.on('pointerdown', () => {
      net.send(MsgTypes.MsgPass);
      this.playPanel.setVisible(false);
    });
    // 重新选牌按钮（出牌按钮左侧）：清空当前选牌，方便重新选择（样式与提示/出牌按钮统一）
    const reBg = this.add.rectangle(-70, 0, 128, 48, 0xc62828)
      .setStrokeStyle(1, 0xffffff, 0.5)
      .setInteractive({ useHandCursor: true });
    const reTxt = this.add.text(-70, 0, '重选', { ...FONT, fontSize: '22px', color: '#ffffff' }).setOrigin(0.5);
    this.reselectBtn = this.add.container(0, 0, [reBg, reTxt]);
    reBg.on('pointerdown', () => {
      this.selected.clear();
      this.hintIndex = 0;
      this.renderHand();
    });
    const hintBtn = this.add.image(70, 0, 'btn', 'hint.png').setInteractive({ useHandCursor: true });
    hintBtn.on('pointerdown', () => this.applyHint());
    this.shotBtn = this.add.image(210, 0, 'btn', 'shot.png').setInteractive({ useHandCursor: true });
    this.shotBtn.on('pointerdown', () => this.playSelected());
    this.playPanel.add([this.passBtn, this.reselectBtn, hintBtn, this.shotBtn]);
  }

  private buildCounterPanel() {
    // 记牌器：右上角可折叠，默认关闭（技术设计 9.4）
    const toggle = this.add.text(1268, 12, '记牌器 ▸', { ...FONT, fontSize: '15px', color: '#ffd54f' })
      .setOrigin(1, 0).setDepth(60).setInteractive({ useHandCursor: true });
    this.counterPanel = this.add.container(0, 0).setDepth(59).setVisible(false);
    const bg = this.add.rectangle(1150, 150, 230, 240, 0x000000, 0.6);
    this.counterPanel.add(bg);
    const ranks = this.counter.displayRanks();
    ranks.forEach((r, i) => {
      const label = r === 17 ? '大王' : r === 16 ? '小王' : r === 15 ? '2' : r === 14 ? 'A' : r === 11 ? 'J' : r === 12 ? 'Q' : r === 13 ? 'K' : String(r);
      const t = this.add.text(1060, 48 + i * 15, `${label}: `, { ...FONT, fontSize: '13px' });
      const v = this.add.text(1120, 48 + i * 15, '', { ...FONT, fontSize: '13px', color: '#ffd54f' });
      this.counterTexts.set(r, v);
      this.counterPanel.add([t, v]);
    });
    toggle.on('pointerdown', () => {
      const show = !this.counterPanel.visible;
      this.counterPanel.setVisible(show);
      toggle.setText(show ? '记牌器 ▾' : '记牌器 ▸');
      if (show) this.renderCounter();
    });
  }

  // --- 渲染 ---

  /**
   * 渲染上家出牌：基于 store.lastPlayed 全局上下文，独立于消息流。
   * 确保在任何状态恢复（MsgGameState）或场景重建后都能正确显示。
   */
  private renderLastPlayed() {
    // 安全网：如果 viewOf 为空但玩家数据已就绪，先重建映射
    if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
      this.rebuildViewMapping();
    }
    // 清除所有出牌区
    for (const area of this.playAreas) {
      area.removeAll(true);
    }
    // 如果有上家出牌，渲染到对应位置
    if (store.lastPlayed.length > 0 && store.lastPlayerID) {
      const v = this.viewOf.get(store.lastPlayerID);
      if (v !== undefined) {
        this.showPlayedCards(store.lastPlayerID, store.lastPlayed);
      } else {
        // 再次尝试重建映射，因为可能是在处理消息时 viewOf 尚未建立
        this.rebuildViewMapping();
        const v2 = this.viewOf.get(store.lastPlayerID);
        if (v2 !== undefined) {
          this.showPlayedCards(store.lastPlayerID, store.lastPlayed);
        } else {
          console.warn('[renderLastPlayed] lastPlayerID 不在 viewOf 中:', store.lastPlayerID);
        }
      }
    }
  }

  private renderAll() {
    // 安全网：如果 viewOf 为空但玩家数据已就绪，先重建映射
    // （场景切换间隙 REPLAYABLE 消息可能尚未重放）
    if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
      this.rebuildViewMapping();
    }
    store.hand = sortHandDesc(store.hand);
    this.renderHand();
    this.renderPanels();
    if (store.landlordID) this.revealBottom();
    // 使用专门的 renderLastPlayed 方法渲染上家出牌
    this.renderLastPlayed();
    this.multiplierText.setText(store.multiplier > 1 ? `倍数 ×${store.multiplier}` : '');
    if (store.currentTurn) {
      const action = store.phase === 'bidding' ? 'bid' : store.phase === 'doubling' ? 'double' : 'play';
      this.showTurnHint(store.currentTurn, action);
    }
    // 重连/场景恢复：若轮到用户出牌且未挂机，显示操作面板
    // （仅靠 MsgPlayTurn 消息触发显示不够——场景重建时消息不会重发）
    if (store.phase === 'playing' && store.currentTurn === store.playerID && !store.afkPlayers.has(store.playerID)) {
      this.playPanel.setVisible(true);
      this.passBtn.setVisible(!store.mustPlay);
      this.shotBtn.setAlpha(store.canBeat || store.mustPlay ? 1 : 0.4);
      // 场景恢复时：无牌可大过也需调度自动过
      if (!store.canBeat && !store.mustPlay) this.scheduleAutoPass();
    }
    this.renderCounter();
  }

  private renderPanels() {
    // 安全网：如果 viewOf 为空但玩家数据已就绪，先重建映射
    if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
      this.rebuildViewMapping();
    }
    // 若 viewOf 仍为空，跳过渲染（避免用空文本覆盖已有面板内容）
    if (this.viewOf.size === 0) return;
    for (let v = 0; v < 3; v++) {
      const pid = [...this.viewOf.entries()].find(([, idx]) => idx === v)?.[0] ?? '';
      const p = store.player(pid);
      const isSelf = v === 0;
      const mark = p?.is_bot ? ' 🤖' : store.afkPlayers.has(pid) ? ' 💤' : '';
      // 用户名 + 标记
      this.panelNames[v].setText(p ? `${p.name}${mark}` : '');
      // 剩余张数
      if (isSelf) {
        this.panelCounts[v].setText(`🃏${store.hand.length}张`);
      } else {
        this.panelCounts[v].setText(p ? `🃏${p.cards_count}张` : '');
      }
      // 积分/角色信息
      if (p) {
        const sessionId = p.id;
        const sessionScore = store.sessionScores.get(sessionId) ?? 0;
        const role = p.id === store.landlordID ? (store.landlordID ? '地主' : '') : (store.landlordID ? '农民' : '');
        const scoreStr = sessionScore !== 0 ? ` 🎯${sessionScore > 0 ? '+' : ''}${sessionScore}` : '';
        // 自己额外显示总积分和排名
        if (isSelf) {
          this.panelScores[v].setText(`${role}${scoreStr}  🏆${store.score}分`);
        } else {
          this.panelScores[v].setText(`${role}${scoreStr}`);
        }
      } else {
        this.panelScores[v].setText('');
      }
      // 头像：地主/农民/默认（try-catch 防止纹理图集异常中断整个渲染循环）
      try {
        this.panelIcons[v].setFrame(
          p && p.id === store.landlordID ? 'icon_landlord.png' : p && store.landlordID ? 'icon_farmer.png' : 'icon_default.png',
        );
      } catch (e) {
        console.warn('[renderPanels] setFrame 失败:', e);
      }
    }
  }

  private renderHand() {
    this.handContainer.removeAll(true);
    this.handSprites.clear();
    this.handXs = handLayoutXs(store.hand.length, 1100);
    store.hand.forEach((c, i) => {
      const key = `${c.suit}-${c.rank}`;
      const s = this.add.sprite(this.handXs[i], 0, 'poker', cardFrame(c)).setScale(0.9).setInteractive({ useHandCursor: true });
      s.setData('key', key);
      s.on('pointerdown', () => this.toggleSelect(s));
      if (this.selected.has(key)) s.y = -24;
      this.handSprites.set(key, s);
      this.handContainer.add(s);
    });
  }

  /** 框选：按下开始，移动超过阈值进入拖选模式，抬起把矩形内手牌全部选中/取消 */
  private onPointerDown(e: Phaser.Input.Pointer): void {
    if (e.rightButtonDown?.()) return;
    this.dragging = true;
    this.dragMoved = false;
    this.dragStartX = e.x;
    this.dragStartY = e.y;
    this.dragToggled = new Set();
  }

  private onPointerMove(e: Phaser.Input.Pointer): void {
    if (!this.dragging) return;
    const dx = e.x - this.dragStartX;
    const dy = e.y - this.dragStartY;
    if (!this.dragMoved && dx * dx + dy * dy < 36) return;
    this.dragMoved = true;
    if (!this.dragRect) {
      this.dragRect = this.add.rectangle(0, 0, 0, 0, 0x44aaff, 0.18)
        .setStrokeStyle(1, 0x44aaff, 0.7).setOrigin(0, 0).setDepth(25);
    }
    const x = Math.min(this.dragStartX, e.x);
    const y = Math.min(this.dragStartY, e.y);
    const w = Math.abs(e.x - this.dragStartX);
    const h = Math.abs(e.y - this.dragStartY);
    this.dragRect.setPosition(x, y).setSize(w, h);
    // 世界坐标 → 手牌容器本地坐标（handContainer 位于 (640, 660)）
    const lx1 = x - this.handContainer.x;
    const lx2 = x + w - this.handContainer.x;
    let changed = false;
    store.hand.forEach((c, i) => {
      const key = `${c.suit}-${c.rank}`;
      const cx = this.handXs[i];
      if (cx >= lx1 && cx <= lx2 && !this.dragToggled.has(key)) {
        // 切换选中状态：已选→取消，未选→选中（每张牌每次拖拽经过只切换一次）
        if (this.selected.has(key)) this.selected.delete(key);
        else this.selected.add(key);
        this.dragToggled.add(key);
        changed = true;
      }
    });
    if (changed) {
      this.handSprites.forEach((s, key) => { s.y = this.selected.has(key) ? -24 : 0; });
    }
  }

  private onPointerUp(): void {
    if (this.dragRect) { this.dragRect.destroy(); this.dragRect = undefined; }
    this.dragging = false;
    this.dragMoved = false;
    this.dragToggled.clear();
  }

  private toggleSelect(s: Phaser.GameObjects.Sprite) {
    const key = s.getData('key') as string;
    if (this.selected.has(key)) {
      this.selected.delete(key);
      s.y = 0;
    } else {
      this.selected.add(key);
      s.y = -24;
    }
  }

  private selectedCards(): CardInfo[] {
    return store.hand.filter((c) => this.selected.has(`${c.suit}-${c.rank}`));
  }

  private applyHint() {
    this.cancelAutoPass(); // 玩家主动操作，重置自动过计时
    const last = store.mustPlay ? null : store.lastPlayed;
    // 重新生成提示列表（手牌可能变化）
    const newHints = hintAll(store.hand, last);
    if (newHints.length === 0) {
      this.selected.clear();
      this.renderHand();
      this.toast('没有能大过的牌');
      // 确认无牌可出，重新计时 2s 后自动过
      if (!store.mustPlay) this.scheduleAutoPass();
      return;
    }
    // 如果列表变化或越界，从头开始
    if (newHints.length !== this.hintList.length || this.hintIndex >= newHints.length) {
      this.hintIndex = 0;
    }
    this.hintList = newHints;
    const cards = this.hintList[this.hintIndex];
    this.hintIndex = (this.hintIndex + 1) % this.hintList.length;
    this.selected.clear();
    for (const c of cards) this.selected.add(`${c.suit}-${c.rank}`);
    this.renderHand();
    // 提示后给玩家额外 2s 决策时间
    if (!store.canBeat && !store.mustPlay) this.scheduleAutoPass();
  }

  private playSelected() {
    const cards = this.selectedCards();
    if (cards.length === 0) {
      this.toast('请先选牌');
      return;
    }
    net.send(MsgTypes.MsgPlayCards, { cards } satisfies PlayCardsPayload);
  }

  /** 无牌可大过时，延迟 2s 自动发送不出（给玩家观察上家出牌的时间） */
  private scheduleAutoPass() {
    this.cancelAutoPass();
    this.autoPassTimer = this.time.delayedCall(2000, () => {
      if (store.phase === 'playing' && store.currentTurn === store.playerID && !store.canBeat && !store.mustPlay) {
        net.send(MsgTypes.MsgPass);
        this.playPanel.setVisible(false);
      }
    });
  }

  /** 取消自动过定时器（玩家手动操作或回合切换时调用） */
  private cancelAutoPass() {
    if (this.autoPassTimer) {
      this.autoPassTimer.remove();
      this.autoPassTimer = undefined;
    }
  }

  private toast(msg: string) {
    const t = this.add.text(640, 500, msg, { ...FONT, color: '#ff8a80', backgroundColor: '#000000aa', padding: { x: 10, y: 4 } })
      .setOrigin(0.5).setDepth(300); // 高于结算弹窗（200），避免提示被遮挡
    this.tweens.add({ targets: t, alpha: 0, delay: 1500, duration: 400, onComplete: () => t.destroy() });
  }

  private renderCounter() {
    for (const [rank, t] of this.counterTexts) {
      t.setText(String(this.counter.get(rank)));
    }
  }

  private showTurnHint(pid: string, action: 'bid' | 'double' | 'play' = 'play') {
    const v = this.viewOf.get(pid) ?? 0;
    const name = store.player(pid)?.name ?? '';
    const me = v === 0;
    if (action === 'bid') {
      this.turnText.setText(me ? '轮到你叫分' : `等待 ${name} 叫分…`);
    } else if (action === 'double') {
      this.turnText.setText(me ? '轮到你选择加倍' : `等待 ${name} 选择加倍…`);
    } else {
      this.turnText.setText(me ? '轮到你出牌' : `等待 ${name} 出牌…`);
    }
  }

  private setBidStatus(pid: string, text: string, color = '#ff8a80') {
    const v = this.viewOf.get(pid);
    if (v !== undefined) {
      this.panelBidStatus[v].setText(text).setColor(color);
    }
  }

  private clearAllBidStatus() {
    this.panelBidStatus.forEach((t) => t.setText(''));
  }

  private showPlayedCards(pid: string, cards: CardInfo[]) {
    const v = this.viewOf.get(pid) ?? 0;
    const area = this.playAreas[v];
    area.removeAll(true);
    const sorted = sortHandDesc(cards);
    const gap = 34;
    sorted.forEach((c, i) => {
      const s = this.add.sprite((i - (sorted.length - 1) / 2) * gap, 0, 'poker', cardFrame(c)).setScale(0.55);
      area.add(s);
    });
  }

  private revealBottom() {
    sortHandDesc(store.bottomCards).forEach((c, i) => {
      if (this.bottomSprites[i]) this.bottomSprites[i].setFrame(cardFrame(c));
    });
  }

  private startCountdown(seconds: number) {
    this.timerEvent?.remove();
    let left = seconds;
    const originalText = this.turnText.text;
    const render = () => {
      if (left <= 0) {
        this.turnText.setText(originalText.replace(/\s*\(\d+s\)\s*$/, '').trim());
        return;
      }
      const base = originalText.replace(/\s*\(\d+s\)\s*$/, '').trim();
      this.turnText.setText(`${base} (${left}s)`);
    };
    render();
    this.timerEvent = this.time.addEvent({
      delay: 1000,
      repeat: seconds - 1,
      callback: () => {
        left--;
        render();
      },
    });
  }

  // --- 消息处理 ---

  private subscribe() {
    const on = (t: Parameters<typeof net.on>[0], h: Parameters<typeof net.on>[1]) => this.unsubscribers.push(net.on(t, h));

    // ── 游戏开始：确保 store.players 已填充 ──
    // epoch 守卫：过滤过期重放（LobbyScene/RoomScene 已处理的 MsgGameStart）
    on(MsgTypes.MsgGameStart, (p: GameStartPayload) => {
      if (store.gameStartEpoch !== this.enterEpoch) {
        console.warn(`[GameScene] 忽略过期 MsgGameStart (msg epoch=${store.gameStartEpoch}, enter=${this.enterEpoch})`);
        return;
      }
      for (const pl of p.players) {
        if (pl.id === store.playerID && pl.is_bot) {
          console.warn('[GameScene/game_start] 当前玩家被错误标记为机器人，本地修正');
          pl.is_bot = false;
        }
      }
      store.players = p.players;
      store.afkPlayers.clear();
      store.gameEnded = false;
      this.lastWarningByPlayer.clear();
      // 提取地主 ID（如果 players 中已包含地主标记）
      if (!store.landlordID) {
        const lp = store.players.find((pl) => pl.is_landlord);
        if (lp) store.landlordID = lp.id;
      }
      this.rebuildViewMapping();
      this.renderPanels();
    });

    on(MsgTypes.MsgDealCards, (p: DealCardsPayload) => {
      store.hand = sortHandDesc(p.cards);
      store.bottomCards = p.bottom_cards ?? [];
      store.phase = 'bidding';
      this.clearAllBidStatus();
      this.counter.reset();
      this.counter.deduct(store.hand);
      this.renderHand();
      this.renderCounter();
      // 确保 viewOf 已建立并刷新面板（显示用户名/积分/头像）
      if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
        this.rebuildViewMapping();
      }
      this.renderPanels();
      safePlay(this, 'deal');
    });

    on(MsgTypes.MsgBidTurn, (p: BidTurnPayload) => {
      store.currentTurn = p.player_id;
      store.multiplier = p.multiplier;
      store.phase = p.phase === 'double' ? 'doubling' : 'bidding';
      this.multiplierText.setText(p.multiplier > 1 ? `倍数 ×${p.multiplier}` : '');
      this.showTurnHint(p.player_id, p.phase === 'double' ? 'double' : 'bid');
      this.startCountdown(p.timeout);
      // 确保 viewOf 已建立并刷新面板
      if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
        this.rebuildViewMapping();
      }
      this.renderPanels();
      // 挂机状态下不弹决策面板，由服务端机器人策略自动决策
      if (p.player_id === store.playerID && !store.afkPlayers.has(store.playerID)) {
        this.showBidChoices(p.phase, p.high_bid);
        this.bidPanel.setVisible(true);
      } else {
        this.bidPanel.setVisible(false);
      }
    });

    on(MsgTypes.MsgBidResult, (p: BidResultPayload) => {
      const v = this.viewOf.get(p.player_id) ?? 0;
      const { x, y } = PANEL_POS[v];
      const label = p.phase === 'double'
        ? (p.double ? '加倍' : '不加倍')
        : (p.score > 0 ? `叫 ${p.score} 分` : '不叫');
      const active = p.phase === 'double' ? p.double : p.score > 0;
      showBubble(this, x + 50, y - 20, label);
      this.setBidStatus(p.player_id, label, active ? '#ffd54f' : '#aaaaaa');
      if (p.phase === 'double') {
        // 加倍阶段用语音合成播报（音频文件可能为叫分内容，不适用于加倍）
        this.speakHandType(p.double ? '加倍' : '不加倍', undefined, `bid-${p.player_id}`);
      } else {
        safePlay(this, p.score > 0 ? `m_score_${p.score}` : 'm_score_0');
      }
      store.multiplier = p.multiplier;
      this.multiplierText.setText(p.multiplier > 1 ? `倍数 ×${p.multiplier}` : '');
    });

    on(MsgTypes.MsgLandlord, (p: LandlordPayload) => {
      store.landlordID = p.player_id;
      store.bottomCards = p.bottom_cards ?? [];
      store.multiplier = p.multiplier;
      store.phase = 'doubling';
      // 确定地主后隐藏叫分面板，等待加倍阶段通知
      this.bidPanel.setVisible(false);
      this.playPanel.setVisible(false);
      this.clearAllBidStatus();
      this.revealBottom();
      // 底牌飞向地主
      const v = this.viewOf.get(p.player_id) ?? 0;
      const target = PANEL_POS[v];
      this.bottomSprites.forEach((s, i) => {
        s.setFrame(cardFrame(sortHandDesc(p.bottom_cards)[i] ?? p.bottom_cards[i]));
        const offsetX = (i - 1) * 35; // 3 张牌并排：-35, 0, +35
        this.tweens.add({ targets: s, x: target.x + offsetX, y: target.y - 60, duration: 600, delay: i * 100 });
      });
      if (p.player_id === store.playerID) {
        store.hand = sortHandDesc([...store.hand, ...store.bottomCards]);
        this.renderHand();
      }
      this.renderPanels();
      this.multiplierText.setText(p.multiplier > 1 ? `倍数 ×${p.multiplier}` : '');
      showBubble(this, target.x + 50, target.y - 20, '地主');
    });

    on(MsgTypes.MsgPlayTurn, (p: PlayTurnPayload) => {
      store.currentTurn = p.player_id;
      store.mustPlay = p.must_play;
      store.canBeat = p.can_beat;
      store.phase = 'playing';
      // 新一轮（must_play）时清空所有出牌区及 store 中的上家出牌记录
      if (p.must_play) {
        for (const area of this.playAreas) area.removeAll(true);
        store.lastPlayed = [];
        store.lastPlayerID = '';
      }
      this.showTurnHint(p.player_id, 'play');
      this.startCountdown(p.timeout);
      // 确保 viewOf 已建立并刷新面板
      if (this.viewOf.size === 0 && store.players.length > 0 && store.playerID) {
        this.rebuildViewMapping();
      }
      this.renderPanels();
      // 挂机状态下不弹出操作面板，由服务端机器人策略自动出牌
      if (p.player_id === store.playerID && !store.afkPlayers.has(store.playerID)) {
        this.cancelAutoPass();
        this.playPanel.setVisible(true);
        this.passBtn.setVisible(!p.must_play); // 新一轮隐藏"不出"
        this.shotBtn.setAlpha(p.can_beat || p.must_play ? 1 : 0.4); // 压不过置灰
        // 无牌可大过且非新一轮：展示 2s 观察期后自动过
        if (!p.can_beat && !p.must_play) {
          this.scheduleAutoPass();
        }
      } else {
        this.playPanel.setVisible(false);
      }
    });

    on(MsgTypes.MsgAfkChanged, (p: AfkChangedPayload) => {
      // 对局已结束后忽略残留的挂机变更，防止上一局超时消息污染下一局
      if (store.gameEnded) return;
      if (p.afk) store.afkPlayers.add(p.player_id);
      else store.afkPlayers.delete(p.player_id);
      this.renderAfkToggle();
      this.renderPanels();
      if (p.player_id === store.playerID) {
        this.toast(p.afk ? '已挂机，机器人将自动出牌' : '已取消挂机');
        if (p.afk) {
          this.playPanel.setVisible(false);
          this.bidPanel.setVisible(false);
          this.clearBidChoices();
        }
      }
    });

    on(MsgTypes.MsgCardPlayed, (p: CardPlayedPayload) => {
      this.cancelAutoPass();
      this.playPanel.setVisible(false);
      this.counter.deduct(p.cards);
      this.renderCounter();
      this.showPlayedCards(p.player_id, p.cards);
      const v = this.viewOf.get(p.player_id) ?? 0;
      const { x, y } = PANEL_POS[v];
      if (p.hand_type) showBubble(this, x + 50, y - 20, p.hand_type);

      this.speakHandType(p.hand_type, p.cards, `play-${p.player_id}`);

      if (p.player_id === store.playerID) {
        store.hand = removeCards(store.hand, p.cards);
        this.selected.clear();
        this.renderHand();
      }
      const pl = store.player(p.player_id);
      if (pl) pl.cards_count = p.cards_left;
      this.renderPanels();
      store.lastPlayed = p.cards;
      store.lastPlayerID = p.player_id;
      // 剩余 ≤ 2 张语音提醒
      if (p.cards_left > 0 && p.cards_left <= 2) {
        this.speakHandType(`小心哦，我就剩${p.cards_left}张牌了`, undefined, `warning-${p.player_id}`);
      }
    });

    on(MsgTypes.MsgPlayerPass, (p: PlayerPassPayload) => {
      this.cancelAutoPass();
      const v = this.viewOf.get(p.player_id) ?? 0;
      const { x, y } = PANEL_POS[v];
      showBubble(this, x + 50, y - 20, '不出');
      // 清空该玩家出牌区，显示"不出"提示（其他玩家的出牌保留可见）
      this.playAreas[v].removeAll(true);
      const passText = this.add.text(0, 0, '不出', { ...FONT, fontSize: '18px', color: '#cccccc', stroke: '#000000', strokeThickness: 2 }).setOrigin(0.5);
      this.playAreas[v].add(passText);
      if (p.player_id === store.playerID) this.playPanel.setVisible(false);
      this.speakHandType('过', undefined, `pass-${p.player_id}`);
    });

    on(MsgTypes.MsgGameOver, (p: GameOverPayload) => {
      store.gameEnded = true; // 标记对局结束，阻止残留 MsgAfkChanged 重新标记用户
      this.gameEndedNormally = true; // 对局已正常结束，shutdown 时无需发送 MsgLeaveGame
      // ── 流程状态机：对局结束 ──
      setFlowState(FlowState.GAME_OVER);
      // ── 立即清理缓冲消息，防止 stale 游戏消息泄漏到下一局 ──
      // （状态机退出 GAME 时也会自动清理，此处提前清理缩小 stale 窗口）
      net.clearPending();
      this.turnText.setText('');
      this.playPanel.setVisible(false);
      this.bidPanel.setVisible(false);
      // 不取消语音播报：结束前的最后一次出牌语音可能正在播放或仍在队列中，
      // 直接 cancel 会把它覆盖掉；语音队列会自行排空，胜负音效可与其并行。
      safePlay(this, this.isWin(p) ? 'win' : 'lose');
      const myScore = p.scores.find((s) => s.player_id === store.playerID);
      if (myScore) {
        store.score += myScore.score;
        this.scoreText.setText(`🎯 ${store.score}  🏆 ${store.rank || '—'}`);
      }
      // 更新本轮各玩家积分
      for (const s of p.scores) {
        store.sessionScores.set(s.player_id, (store.sessionScores.get(s.player_id) ?? 0) + s.score);
      }
      this.showGameOverDialog(p);
    });

    on(MsgTypes.MsgStatsResult, (p: StatsResultPayload) => {
      // 结算落库后服务端推送权威统计：人机房积分经机器人重分配，本地累加值需对齐服务端口径
      if (p.player_id !== store.playerID) return;
      store.score = p.score;
      store.rank = p.rank;
      this.scoreText.setText(`🎯 ${store.score}  🏆 ${store.rank || '—'}`);
    });

    on(MsgTypes.MsgMaintenancePush, (p: MaintenancePayload) => {
      store.maintenance = p.maintenance;
      if (p.maintenance) this.showMaintenanceOverlay();
      else this.hideMaintenanceOverlay();
    });

    on(MsgTypes.MsgError, (p: ErrorPayload) => this.toast(p.message));

    // ── 同步检测响应：服务端状态与客户端不一致时自动回退大厅 ──
    on(MsgTypes.MsgGameSyncResult, (p: GameSyncResultPayload) => {
      if (this.gameEndedNormally) return; // 对局已正常结束，忽略同步响应
      if (p.status === 'active') return; // 服务端确认对局进行中，状态一致
      // 状态不一致：服务端无活跃对局（waiting=房间等待中 / none=无房间）
      console.warn(`[GameScene] 同步检测失败：服务端状态=${p.status}，客户端认为对局进行中，回退大厅`);
      this.goToLobby();
    });

    // ── 状态恢复响应：通过同步拉取模式获得完整对局状态 ──
    // 平台信封 GameStatePayload.state 内为斗地主私有状态（GameStateDTO）
    on(MsgTypes.MsgGameState, (p: GameStatePayload) => {
      if (this.gameEndedNormally) return;
      if (!p.available || !p.state) {
        // 服务端无活跃对局，回退大厅
        console.warn('[GameScene] 状态恢复：服务端无活跃对局，回退大厅');
        this.goToLobby();
        return;
      }
      const dto = p.state as GameStateDTO;
      this.stateReceived = true;
      store.stateReceived = true; // 已收到服务端权威对局状态（同步拉取路径）
      // 用服务端权威状态覆盖 store（复用重连恢复逻辑）
      store.restoreGameState(dto);
      // 重建视图映射（viewOf 可能因 store.players 为空而未初始化）
      this.rebuildViewMapping();
      // 重新渲染整个牌桌（renderAll 内部包含 renderPanels）
      this.renderAll();
      // 状态恢复后：根据阶段显示对应操作面板（地主 ID 从 players 中提取）
      if (!store.landlordID) {
        const lp = store.players.find((pl) => pl.is_landlord);
        if (lp) store.landlordID = lp.id;
      }
      if (store.currentTurn === store.playerID && !store.afkPlayers.has(store.playerID)) {
        if (store.phase === 'bidding' || store.phase === 'doubling') {
          // 叫分/加倍阶段：显示叫分面板
          this.showBidChoices(store.phase === 'doubling' ? 'double' : 'bid', 0);
          this.bidPanel.setVisible(true);
        } else if (store.phase === 'playing') {
          // 出牌阶段：显示出牌面板
          this.playPanel.setVisible(true);
          this.passBtn.setVisible(!store.mustPlay);
          this.shotBtn.setAlpha(store.canBeat || store.mustPlay ? 1 : 0.4);
          // 状态恢复时：无牌可大过也需调度自动过
          if (!store.canBeat && !store.mustPlay) this.scheduleAutoPass();
        }
      }
      console.log('[GameScene] 状态恢复成功', `phase=${dto.phase}`, `hand=${dto.hand?.length ?? 0}张`, `lastPlayed=${dto.last_played?.length ?? 0}张`);
      // ── 延迟安全网：确保状态恢复后 UI 完整渲染 ──
      // renderAll 已在上方调用，但部分渲染（如面板文本、上家出牌）可能因
      // viewOf 映射或 Phaser 渲染帧时序问题未生效，延迟补渲染一次
      this.time.delayedCall(100, () => {
        if (this.gameEndedNormally) return;
        this.renderPanels();
        this.renderLastPlayed();
      });
    });
  }

  private showMaintenanceOverlay() {
    if (this.maintenanceOverlay) return;
    this.maintenanceOverlay = this.add.container(640, 360).setDepth(300);
    const bg = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6);
    const title = this.add.text(0, -40, '🛠️ 服务器维护中', {
      ...FONT, fontSize: '40px', color: '#ffd54f',
    }).setOrigin(0.5);
    const msg = this.add.text(0, 20, '本局结束后将无法继续开始新对局\n请稍后再试', {
      ...FONT, fontSize: '22px', color: '#eeeeee', align: 'center',
    }).setOrigin(0.5);
    this.maintenanceOverlay.add([bg, title, msg]);
  }

  private hideMaintenanceOverlay() {
    this.maintenanceOverlay?.destroy();
    this.maintenanceOverlay = undefined;
  }

  private isWin(p: GameOverPayload): boolean {
    const myScore = p.scores.find((s) => s.player_id === store.playerID);
    return (myScore?.score ?? 0) > 0;
  }

  /** 语音播报牌型（Web Speech API，中文）：加入队列顺序播报，不覆盖未播完的内容
   *  source: 触发来源（含席位标识如 `play-xxx`），同来源同文本视为重复忽略 */
  private speakHandType(handType?: string, cards?: CardInfo[], source = 'play') {
    if (!handType || store.muted) return;
    const text = this.describeHand(handType, cards);
    // 来源级去重：相同文本 + 相同来源 → 视为重复消息，忽略
    if (text === this.lastSpeakText && source === this.lastSpeakSource) return;
    // 警告类语音按席位独立去重（同一玩家相同警告文本不重复播报，避免被中间其他语音覆盖去重状态）
    if (source.startsWith('warning-')) {
      const pid = source.slice(8);
      if (this.lastWarningByPlayer.get(pid) === text) return;
      this.lastWarningByPlayer.set(pid, text);
    }
    // 限制队列长度，防止堆积过多语音
    if (this.speechQueue.length >= 5) {
      this.speechQueue.shift(); // 移除最旧的语音
    }
    this.lastSpeakText = text;
    this.lastSpeakSource = source;
    this.speechQueue.push({ text, source, pitch: text.includes('炸弹') || text.includes('王炸') ? 1.3 : 1.0 });
    this.processSpeechQueue();
  }

  /** 按顺序处理语音队列，确保每条语音完整播完再播下一条 */
  private processSpeechQueue() {
    if (this.speechPlaying || this.speechQueue.length === 0) return;
    const item = this.speechQueue.shift()!;
    try {
      const synth = window.speechSynthesis;
      if (!synth) return;
      // 先取消所有正在播放的语音，避免堆积
      synth.cancel();
      this.speechPlaying = true;
      const u = new SpeechSynthesisUtterance(item.text);
      u.rate = 1.1;
      u.pitch = item.pitch;
      const next = () => { this.speechPlaying = false; this.processSpeechQueue(); };
      u.onend = next;
      u.onerror = next;
      // 兜底：防止 onend/onerror 未触发导致队列卡死
      this.time.delayedCall(4000, next);
      // 文本含英文字母时选英文语音
      const hasLatin = /[A-Za-z]/.test(item.text);
      const voices = synth.getVoices();
      if (voices.length > 0) {
        const targetLang = hasLatin ? 'en' : 'zh';
        const voice = voices.find((v) => v.lang.startsWith(targetLang))
          ?? voices.find((v) => v.lang.startsWith(hasLatin ? 'en-US' : 'zh-CN'));
        if (voice) { u.voice = voice; u.lang = voice.lang; }
        else u.lang = hasLatin ? 'en-US' : 'zh-CN';
      } else {
        u.lang = hasLatin ? 'en-US' : 'zh-CN';
      }
      synth.speak(u);
    } catch {
      this.speechPlaying = false;
    }
  }

  /** 牌型 → 中文语音描述：一个3 / 对8 / 三个K / 炸弹 / 王炸 / 顺子… */
  private describeHand(handType: string, cards?: CardInfo[]): string {
    const keyRank = (cs: CardInfo[] | undefined): CardInfo | undefined => {
      if (!cs || cs.length === 0) return undefined;
      // 取出现次数最多的 rank 作为关键牌（炸弹/三张/对子/三带等都一致）
      const cnt = new Map<number, number>();
      for (const c of cs) cnt.set(c.rank, (cnt.get(c.rank) ?? 0) + 1);
      let best = cs[0];
      let bestN = 0;
      for (const c of cs) {
        const n = cnt.get(c.rank) ?? 0;
        if (n > bestN) { best = c; bestN = n; }
      }
      return best;
    };
    const rn = (c: CardInfo | undefined): string => {
      if (!c) return '';
      // 为语音播报使用更准确的中文读音
      if (c.rank >= 3 && c.rank <= 10) return String(c.rank);
      switch (c.rank) {
        case 11: return '钩';  // J
        case 12: return '圈';  // Q
        case 13: return '凯';  // K
        case 14: return '尖';  // A
        case 15: return '二';
        case 16: return '小王';
        case 17: return '大王';
        default: return String(c.rank);
      }
    };
    if (handType === '王炸') return '王炸';
    if (handType === '炸弹') return '炸弹';
    if (handType === '单张') return `${rn(cards?.[0])}`;
    if (handType === '对子') return `对${rn(keyRank(cards))}`;
    if (handType === '三张') return `三个${rn(keyRank(cards))}`;
    if (handType === '三带一') return `三个${rn(keyRank(cards))}带一`;
    if (handType === '三带二') return `三个${rn(keyRank(cards))}带一对`;
    if (handType === '四带二') return `四带二`;
    if (handType === '四带两对') return `四带两对`;
    if (handType === '顺子') return '顺子';
    if (handType === '连对') return '连对';
    if (handType === '飞机') return '飞机';
    if (handType === '飞机带单') return '飞机带单';
    if (handType === '飞机带对') return '飞机带对';
    if (handType === '不出' || handType === '过') return '过';
    return handType;
  }

  private showGameOverDialog(p: GameOverPayload) {
    // 斗地主扩展数据（剩余手牌/倍数）在平台信封的 extra 字段内
    const extra = (p.extra ?? {}) as GameOverExtra;
    const playerHands = extra.player_hands ?? [];
    const multiplier = extra.multiplier ?? 1;
    const winnerIsLandlord = p.scores.find((s) => s.player_id === p.winner_id)?.is_landlord ?? false;
    const win = this.isWin(p);
    const dlg = this.add.container(640, 360).setDepth(200);
    // 根据剩余手牌行数动态计算高度
    const handRows = playerHands.length;
    const cardScale = 0.45;
    const cardGap = 32;
    const handRowHeight = 64;
    const handAreaHeight = handRows * handRowHeight;
    const dialogHeight = Math.min(700, Math.max(500, 380 + handAreaHeight));
    const bg = this.add.rectangle(0, 0, 720, dialogHeight, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    const topY = -dialogHeight / 2 + 30;
    const title = this.add.text(0, topY, win ? '🎉 胜利！' : '💀 失败', {
      ...FONT, fontSize: '40px', color: win ? '#ffd54f' : '#ff8a80',
    }).setOrigin(0.5);

    const lines: Phaser.GameObjects.GameObject[] = [];
    lines.push(this.add.text(0, topY + 50, `获胜方：${p.winner_name}（${winnerIsLandlord ? '地主' : '农民'}）  倍数 ×${multiplier}`, { ...FONT, fontSize: '20px' }).setOrigin(0.5));
    p.scores.forEach((s, i) => {
      lines.push(this.add.text(0, topY + 88 + i * 30,
        `${s.player_name}（${s.is_landlord ? '地主' : '农民'}）  得分 ${s.score > 0 ? '+' : ''}${s.score}`, { ...FONT, fontSize: '20px' }).setOrigin(0.5));
    });
    // 各家剩余手牌（放大显示）
    const handStartY = topY + 88 + p.scores.length * 30 + 24;
    playerHands.forEach((h, i) => {
      const y = handStartY + i * handRowHeight;
      lines.push(this.add.text(-310, y, `${h.player_name}:`, { ...FONT, fontSize: '16px', color: '#ffe082' }).setOrigin(0, 0.5));
      const sorted = sortHandDesc(h.cards);
      const startX = -210;
      sorted.forEach((c, j) => {
        lines.push(this.add.sprite(startX + j * cardGap, y, 'poker', cardFrame(c)).setScale(cardScale).setOrigin(0.5));
      });
    });

    // 按钮固定在底部
    const btnY = dialogHeight / 2 - 35;
    const btnWidth = 140;
    const btnGap = 20;
    
    // 复盘按钮
    const replayBtnX = -btnWidth / 2 - btnGap / 2;
    const replayBtn = this.add.rectangle(replayBtnX, btnY, btnWidth, 44, 0x1976d2).setStrokeStyle(1, 0xffffff, 0.5).setInteractive({ useHandCursor: true });
    const replayBtnText = this.add.text(replayBtnX, btnY, '📊 复盘', { ...FONT, fontSize: '20px' }).setOrigin(0.5);
    
    replayBtn.on('pointerover', () => replayBtn.setFillStyle(0x2196f3, 1));
    replayBtn.on('pointerout', () => replayBtn.setFillStyle(0x1976d2, 1));
    replayBtn.on('pointerdown', () => {
      this.showReplayDialog(p);
    });
    
    // 返回房间按钮
    const returnBtnX = btnWidth / 2 + btnGap / 2;
    const btn = this.add.rectangle(returnBtnX, btnY, btnWidth, 44, 0x2e7d32).setStrokeStyle(1, 0xffffff, 0.5).setInteractive({ useHandCursor: true });
    const btnText = this.add.text(returnBtnX, btnY, '返回房间', { ...FONT, fontSize: '22px' }).setOrigin(0.5);
    
    btn.on('pointerover', () => btn.setFillStyle(0x388e3c, 1));
    btn.on('pointerout', () => btn.setFillStyle(0x2e7d32, 1));
    btn.on('pointerdown', () => {
      dlg.destroy();
      store.resetGame();
      // 服务端结算后已把房间复位（机器人保持准备、真人取消准备），
      // 同步本地准备标记，避免房间 UI 显示旧的”已准备”，与服务端开局判定保持一致
      store.players.forEach((pl) => { pl.ready = !!pl.is_bot; });
      // ── 流程状态机：返回房间 ──
      setFlowState(FlowState.ROOM);
      this.scene.start('Room');
    });

    dlg.add([bg, title, ...lines, replayBtn, replayBtnText, btn, btnText]);
  }
  
  /** 显示复盘报告对话框 */
  private async showReplayDialog(_p: GameOverPayload) {
    const roomCode = store.roomCode || '';
    if (!roomCode) {
      this.toast('无法获取房间信息');
      return;
    }

    try {
      const reportText = await fetchLatestReplayText('ddz', roomCode, store.playerID || undefined);
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '📊 对局复盘报告',
        content: reportText,
        width: 800,
        height: 600,
      });
    } catch (err) {
      console.error('加载复盘报告失败:', err);
      this.toast(err instanceof Error && err.message === REPLAY_NOT_READY ? REPLAY_NOT_READY : '加载复盘报告失败，请稍后重试');
    }
  }

  /** 点击头像查看玩家本轮对战统计 */
  private showPlayerStats(viewIdx: number) {
    if (this.statsOverlay) this.statsOverlay.destroy();
    const pid = [...this.viewOf.entries()].find(([, idx]) => idx === viewIdx)?.[0] ?? '';
    const p = store.player(pid);
    if (!p) return;
    const sessionScore = store.sessionScores.get(pid) ?? 0;

    const dlg = this.add.container(640, 360).setDepth(300);
    const bg = this.add.rectangle(0, 0, 420, 360, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    const title = this.add.text(0, -140, p.name + (p.is_bot ? ' 🤖' : ''), {
      ...FONT, fontSize: '24px', color: '#ffd54f',
    }).setOrigin(0.5);

    const lines: Phaser.GameObjects.GameObject[] = [];
    const role = p.id === store.landlordID ? '地主' : '农民';
    lines.push(this.add.text(0, -90, `身份：${role}`, { ...FONT, fontSize: '18px' }).setOrigin(0.5));
    lines.push(this.add.text(0, -58, `剩余手牌：${viewIdx === 0 ? store.hand.length : p.cards_count} 张`, { ...FONT, fontSize: '18px' }).setOrigin(0.5));
    lines.push(this.add.text(0, -26, `本轮积分：${sessionScore > 0 ? '+' : ''}${sessionScore}`, { ...FONT, fontSize: '18px', color: sessionScore >= 0 ? '#81c784' : '#ff8a80' }).setOrigin(0.5));
    lines.push(this.add.text(0, 8, `在线状态：${p.online ? '在线' : '离线'}`, { ...FONT, fontSize: '16px', color: p.online ? '#81c784' : '#aaa' }).setOrigin(0.5));
    lines.push(this.add.text(0, 38, `座位：${p.seat + 1}`, { ...FONT, fontSize: '16px', color: '#cccccc' }).setOrigin(0.5));

    // 如果是自己，显示历史统计
    if (pid === store.playerID) {
      lines.push(this.add.text(0, 72, `总积分：${store.score}  排名：${store.rank || '—'}`, { ...FONT, fontSize: '16px', color: '#ffd54f' }).setOrigin(0.5));
    }

    const closeBtn = this.add.rectangle(0, 130, 120, 40, 0x37474f).setStrokeStyle(1, 0xffffff, 0.5).setInteractive({ useHandCursor: true });
    const closeText = this.add.text(0, 130, '关闭', { ...FONT, fontSize: '18px' }).setOrigin(0.5);
    closeBtn.on('pointerdown', () => { dlg.destroy(); this.statsOverlay = undefined; });

    dlg.add([bg, title, ...lines, closeBtn, closeText]);
    this.statsOverlay = dlg;
  }
}
