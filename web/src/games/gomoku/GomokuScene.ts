import Phaser from 'phaser';
import { net, hydrateIdentity } from '../../platform/net/ws';
import { store } from '../../platform/state/store';
import { setFlowState, FlowState } from '../../platform/flow/gameFlow';
import { gkState, GK_SIZE, GK_EMPTY, GK_BLACK, GK_WHITE } from './state';
import { safePlay } from '../../platform/ui/audio';
import { fetchLatestReplayText, REPLAY_NOT_READY } from '../../platform/replay/replayApi';
import {
  MsgTypes,
  ErrorPayload,
  GameOverPayload,
  GameStartPayload,
  GameStatePayload,
  GameSyncResultPayload,
  GkAfkChangedPayload,
  GkAfkPayload,
  GkDrawRequestPayload,
  GkDrawRequestedPayload,
  GkDrawResponsePayload,
  GkDrawResultPayload,
  GkGameStateDTO,
  GkMoveMadePayload,
  GkMovePayload,
  GkResignPayload,
  GkTurnPayload,
  LeaveGamePayload,
  MaintenancePayload,
  StatsResultPayload,
} from '../../platform/protocol';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;

// 棋盘绘制参数：15 路棋盘居中偏左，右侧为玩家面板与操作区
const CELL = 42;
const BOARD_CX = 400;
const BOARD_CY = 370;
const HALF = ((GK_SIZE - 1) * CELL) / 2; // 边线到中心的距离
const STONE_R = 19;

/** GomokuScene 五子棋对局：棋盘渲染、落子交互、回合提示、挂机托管、结算弹窗 */
export class GomokuScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];

  private gridGfx!: Phaser.GameObjects.Graphics;
  private stoneGfx!: Phaser.GameObjects.Graphics;

  private myPanel!: Phaser.GameObjects.Text;
  private oppPanel!: Phaser.GameObjects.Text;
  private myScoreText!: Phaser.GameObjects.Text;
  private oppScoreText!: Phaser.GameObjects.Text;
  // 头像卡（头像圆 + 首字 + 执子徽章 + 名字 + 积分），下方永远是自己，上方是对手
  private myAvatar!: Phaser.GameObjects.Arc;
  private myAvatarLetter!: Phaser.GameObjects.Text;
  private myStoneBadge!: Phaser.GameObjects.Arc;
  private oppAvatar!: Phaser.GameObjects.Arc;
  private oppAvatarLetter!: Phaser.GameObjects.Text;
  private oppStoneBadge!: Phaser.GameObjects.Arc;
  private turnText!: Phaser.GameObjects.Text;
  private moveText!: Phaser.GameObjects.Text;
  private scoreText!: Phaser.GameObjects.Text;
  private afkToggleBg!: Phaser.GameObjects.Graphics;
  private afkToggleTxt!: Phaser.GameObjects.Text;
  private soundToggle!: Phaser.GameObjects.Text;
  private timerEvent?: Phaser.Time.TimerEvent;
  private syncTimer?: Phaser.Time.TimerEvent;
  private maintenanceOverlay?: Phaser.GameObjects.Container;
  private gameEndedNormally = false;
  private stateReceived = false;
  private enterEpoch = 0;

  constructor() {
    super('GomokuGame');
  }

  create() {
    // ── 防重复创建（与斗地主 GameScene 同构的守卫）──
    const allScenes: Phaser.Scene[] = this.scene.manager.scenes;
    const other = allScenes.find(
      (s: Phaser.Scene) => s !== this && s instanceof GomokuScene && s.sys.isActive(),
    );
    if (other) {
      console.warn('[GomokuScene] 检测到重复创建，立即停止自身');
      this.scene.stop(this);
      return;
    }

    // ── 重置场景实例残留状态（场景实例单例复用，字段不会自动复位）──
    this.gameEndedNormally = false;
    this.stateReceived = false;
    this.unsubscribers = [];
    this.timerEvent = undefined;
    this.syncTimer = undefined;
    this.maintenanceOverlay = undefined;
    // 身份兑底恢复：connected/reconnected 可能在此前无订阅者时到达且缓冲已被清理，
    // 恢复 store.playerID，否则落子守卫会静默丢弃所有点击、面板找不到自己
    hydrateIdentity();

    this.cameras.main.setBackgroundColor('#2b3a4a');

    this.buildBoard();
    this.buildSidePanels();
    this.buildScoreDisplay();
    this.buildSoundToggle();
    this.buildAfkToggle();
    this.buildResignButton();
    this.buildDrawButton();
    this.buildExitButton();

    // ── 流程状态机：标记进入对局 ──
    setFlowState(FlowState.GAME);

    // ── 记录进入时的对局代次（必须在 subscribe 之前）──
    this.enterEpoch = store.gameStartEpoch;

    this.subscribe();
    this.input.on('pointerdown', (p: Phaser.Input.Pointer) => this.onClick(p));

    // 首局棋盘从空开始（重连恢复路径由 MsgGameState 覆盖渲染）
    if (gkState.board.length === 0) gkState.board = new Array(GK_SIZE * GK_SIZE).fill(GK_EMPTY);
    this.renderBoard();
    this.renderPanels();
    this.renderTurn();

    if (store.maintenance) this.showMaintenanceOverlay();

    store.inGame = true;

    // URL 路由：标记当前场景（'/game' 按注册表分发到本场景）
    if (location.hash !== '#/game') location.hash = '/game';

    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());

    // ── 主动拉取权威状态（重连/场景重建恢复）──
    net.send(MsgTypes.MsgRequestGameState);

    // ── 周期性同步检测 ──
    this.syncTimer = this.time.addEvent({
      delay: 30000, // 30秒同步一次，降低CPU占用
      loop: true,
      callback: () => {
        if (!this.gameEndedNormally) net.send(MsgTypes.MsgGameSync);
      },
    });
  }

  shutdown() {
    this.timerEvent?.remove();
    this.syncTimer?.remove();
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
    // 对局未正常结束 且 未收到状态恢复 → 通知服务端终止（与斗地主一致）
    if (!this.gameEndedNormally && !this.stateReceived && store.inGame) {
      net.send(MsgTypes.MsgLeaveGame, {} satisfies LeaveGamePayload);
    }
  }

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 五子棋是静态场景，不需要每帧更新
    // 所有变化都通过事件驱动（网络消息、点击等）
  }

  // --- 布局构建 ---

  private buildBoard() {
    // 木纹底色
    this.add.rectangle(BOARD_CX, BOARD_CY, HALF * 2 + CELL, HALF * 2 + CELL, 0xd9a860)
      .setStrokeStyle(2, 0x8d6e3f);

    // 网格线 + 星位
    this.gridGfx = this.add.graphics();
    this.gridGfx.lineStyle(1, 0x5d4322, 1);
    for (let i = 0; i < GK_SIZE; i++) {
      const off = -HALF + i * CELL;
      this.gridGfx.lineBetween(BOARD_CX - HALF, BOARD_CY + off, BOARD_CX + HALF, BOARD_CY + off);
      this.gridGfx.lineBetween(BOARD_CX + off, BOARD_CY - HALF, BOARD_CX + off, BOARD_CY + HALF);
    }
    const stars: Array<[number, number]> = [[3, 3], [3, 11], [11, 3], [11, 11], [7, 7]];
    this.gridGfx.fillStyle(0x5d4322, 1);
    for (const [r, c] of stars) {
      this.gridGfx.fillCircle(this.cellX(c), this.cellY(r), 3);
    }

    this.stoneGfx = this.add.graphics();
  }

  private buildSidePanels() {
    // 头像卡：棋盘会翻转保证己方棋子始终在下方，因此自己的头像固定在下、对手在上
    this.myAvatar = this.add.circle(940, 512, 22, 0x1976d2).setStrokeStyle(2, 0xffffff, 0.7);
    this.myAvatarLetter = this.add.text(940, 512, '', { ...FONT, fontSize: '20px', color: '#ffffff' }).setOrigin(0.5);
    // 执子徽章：头像右下角小棋子，表示所执颜色
    this.myStoneBadge = this.add.circle(956, 528, 9, 0x111111).setStrokeStyle(1, 0xffffff, 0.9);
    this.myPanel = this.add.text(972, 502, '', { ...FONT, fontSize: '17px', color: '#ffe082' })
      .setOrigin(0, 0.5);
    this.myScoreText = this.add.text(972, 528, '', { ...FONT, fontSize: '14px', color: '#ffd700' })
      .setOrigin(0, 0.5);

    this.oppAvatar = this.add.circle(940, 212, 22, 0x6a1b9a).setStrokeStyle(2, 0xffffff, 0.7);
    this.oppAvatarLetter = this.add.text(940, 212, '', { ...FONT, fontSize: '20px', color: '#ffffff' }).setOrigin(0.5);
    this.oppStoneBadge = this.add.circle(956, 228, 9, 0xfafafa).setStrokeStyle(1, 0x999999, 0.9);
    this.oppPanel = this.add.text(972, 202, '', { ...FONT, fontSize: '17px', color: '#ffe082' })
      .setOrigin(0, 0.5);
    this.oppScoreText = this.add.text(972, 228, '', { ...FONT, fontSize: '14px', color: '#ffd700' })
      .setOrigin(0, 0.5);

    this.turnText = this.add.text(1070, 350, '', {
      ...FONT, fontSize: '20px', backgroundColor: '#00000066', padding: { x: 10, y: 6 },
    }).setOrigin(0.5);
    this.moveText = this.add.text(1070, 395, '', { ...FONT, fontSize: '14px', color: '#aaaaaa' })
      .setOrigin(0.5);
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

  /** 挂机开关：挂机后由机器人策略自动落子，主动落子或点击取消即可恢复手动 */
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
      if (store.gameEnded) return;
      const afk = !store.afkPlayers.has(store.playerID);
      // 本地先行切换按钮态（乐观更新），确保文案立即变为"取消托管"，
      // 不必等待服务端 MsgGkAfkChanged 网络往返；服务端回执幂等，再同步一次。
      if (afk) store.afkPlayers.add(store.playerID);
      else store.afkPlayers.delete(store.playerID);
      this.renderAfkToggle();
      net.send(MsgTypes.MsgGkAfk, { afk } satisfies GkAfkPayload);
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
      if (store.inGame && !store.gameEnded) {
        net.send(MsgTypes.MsgLeaveGame, {} satisfies LeaveGamePayload);
      }
      this.gameEndedNormally = true;
      store.resetGame();
      gkState.reset();
      setFlowState(FlowState.LOBBY);
      this.scene.start('Lobby');
    });
  }

  /** 认输按钮：对局进行中始终可点击 */
  private buildResignButton() {
    const container = this.add.container(1100, 690).setDepth(60);
    const g = this.add.graphics();
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-48 + 3, -18 + 5, 96, 36, 10);
    g.fillStyle(0xb71c1c, 1);
    g.fillRoundedRect(-48, -18, 96, 36, 10);
    g.fillStyle(0xe53935, 0.5);
    g.fillRoundedRect(-48, -18, 96, 18, 10);
    g.lineStyle(2, 0xffffff, 0.8);
    g.strokeRoundedRect(-48, -18, 96, 36, 10);
    g.lineStyle(1, 0xffffff, 0.4);
    g.strokeRoundedRect(-45, -16, 90, 10, 8);

    const interactive = this.add.rectangle(0, 0, 96, 36, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    const txt = this.add.text(0, 0, '🏳️ 认输', { fontFamily: 'Arial', fontSize: '15px', color: '#ffffff', fontStyle: 'bold' }).setOrigin(0.5);
    container.add([g, interactive, txt]);

    interactive.on('pointerover', () => this.tweens.add({ targets: container, scale: 1.05, duration: 100 }));
    interactive.on('pointerout', () => this.tweens.add({ targets: container, scale: 1, duration: 100 }));
    interactive.on('pointerdown', async () => {
      if (store.gameEnded || gkState.phase === 'ended') return;
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '🏳️ 认输确认',
        content: '确定要认输吗？认输后对手获胜。',
        width: 400,
        height: 240,
        onConfirm: () => {
          net.send(MsgTypes.MsgGkResign, {} satisfies GkResignPayload);
        },
      });
    });
  }

  /** 和棋按钮：仅在自己的回合时可点击 */
  private buildDrawButton() {
    const container = this.add.container(1100, 644).setDepth(60);
    const g = this.add.graphics();
    this.drawBtnDrawNormal(g);

    const interactive = this.add.rectangle(0, 0, 96, 36, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    const txt = this.add.text(0, 0, '🤝 和棋', { fontFamily: 'Arial', fontSize: '15px', color: '#ffffff', fontStyle: 'bold' }).setOrigin(0.5);
    container.add([g, interactive, txt]);

    interactive.on('pointerover', () => {
      if (store.currentTurn === store.playerID && !store.gameEnded) {
        this.tweens.add({ targets: container, scale: 1.05, duration: 100 });
      }
    });
    interactive.on('pointerout', () => this.tweens.add({ targets: container, scale: 1, duration: 100 }));
    interactive.on('pointerdown', () => {
      if (store.gameEnded || gkState.phase === 'ended') return;
      if (store.currentTurn !== store.playerID) {
        this.toast('只有你的回合才能请求和棋');
        return;
      }
      net.send(MsgTypes.MsgGkDrawRequest, { requester_id: store.playerID } satisfies GkDrawRequestPayload);
      this.toast('已向对手发送和棋请求');
    });
  }

  private drawBtnDrawNormal(g: Phaser.GameObjects.Graphics) {
    g.clear();
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-48 + 3, -18 + 5, 96, 36, 10);
    g.fillStyle(0xf9a825, 1);
    g.fillRoundedRect(-48, -18, 96, 36, 10);
    g.fillStyle(0xffee58, 0.5);
    g.fillRoundedRect(-48, -18, 96, 18, 10);
    g.lineStyle(2, 0xffffff, 0.8);
    g.strokeRoundedRect(-48, -18, 96, 36, 10);
    g.lineStyle(1, 0xffffff, 0.4);
    g.strokeRoundedRect(-45, -16, 90, 10, 8);
  }

  // --- 渲染 ---

  /** 己方执子颜色（1=黑 2=白）：优先用服务端 DTO 下发的 my_color，开局初期回退按座位推断（0 号座执黑）；座位未知时不翻转（默认执黑视角） */
  private myColorSelf(): number {
    if (gkState.myColor === GK_BLACK || gkState.myColor === GK_WHITE) return gkState.myColor;
    const seat = store.seatOf(store.playerID);
    if (seat === 0) return GK_BLACK;
    if (seat === 1) return GK_WHITE;
    return GK_BLACK;
  }

  /** 棋盘翻转：己方执白时上下左右镜像显示，保证己方棋子始终在下方（正常下棋视角） */
  private get flipped(): boolean {
    return this.myColorSelf() === GK_WHITE;
  }

  /** 逻辑列 → 屏幕坐标（执白时镜像） */
  private cellX(col: number): number {
    const c = this.flipped ? GK_SIZE - 1 - col : col;
    return BOARD_CX - HALF + c * CELL;
  }

  /** 逻辑行 → 屏幕坐标（执白时镜像） */
  private cellY(row: number): number {
    const r = this.flipped ? GK_SIZE - 1 - row : row;
    return BOARD_CY - HALF + r * CELL;
  }

  private renderBoard() {
    this.stoneGfx.clear();
    const board = gkState.board;
    for (let r = 0; r < GK_SIZE; r++) {
      for (let c = 0; c < GK_SIZE; c++) {
        const v = board[r * GK_SIZE + c];
        if (v === GK_EMPTY) continue;
        const x = this.cellX(c);
        const y = this.cellY(r);
        if (v === GK_BLACK) {
          this.stoneGfx.fillStyle(0x111111, 1);
          this.stoneGfx.fillCircle(x, y, STONE_R);
          this.stoneGfx.lineStyle(1, 0x555555, 1);
          this.stoneGfx.strokeCircle(x, y, STONE_R);
        } else {
          this.stoneGfx.fillStyle(0xfafafa, 1);
          this.stoneGfx.fillCircle(x, y, STONE_R);
          this.stoneGfx.lineStyle(1, 0x999999, 1);
          this.stoneGfx.strokeCircle(x, y, STONE_R);
        }
      }
    }
    // 最近一手高亮
    if (gkState.lastRow >= 0 && gkState.lastCol >= 0) {
      const v = board[gkState.lastRow * GK_SIZE + gkState.lastCol];
      this.stoneGfx.lineStyle(2, v === GK_BLACK ? 0xff5252 : 0xd32f2f, 1);
      this.stoneGfx.strokeCircle(this.cellX(gkState.lastCol), this.cellY(gkState.lastRow), STONE_R + 3);
    }
    this.moveText.setText(gkState.moveNumber > 1 ? `第 ${gkState.moveNumber} 手` : '');
  }

  private myColorOf(pid: string): number {
    // 0 号座执黑（与服务端 rule.ColorOfSeat 一致）
    return store.seatOf(pid) === 0 ? GK_BLACK : 2;
  }

  private renderPanels() {
    const me = store.player(store.playerID);
    const opp = store.players.find((p) => p.id !== store.playerID);

    const mark = (p: typeof me): string => {
      if (!p) return '';
      const bot = p.is_bot ? ' 🤖' : '';
      const afk = store.afkPlayers.has(p.id) ? ' 💤' : '';
      const off = p.online === false ? ' 📴' : '';
      return bot + afk + off;
    };

    // 头像：机器人紫色底（与房间席位一致），真人蓝色底；执子徽章小棋子表示颜色
    const applyAvatar = (
      avatar: Phaser.GameObjects.Arc,
      letter: Phaser.GameObjects.Text,
      badge: Phaser.GameObjects.Arc,
      p: typeof me,
      color: number,
    ) => {
      if (!p) {
        avatar.setVisible(false);
        letter.setVisible(false);
        badge.setVisible(false);
        return;
      }
      avatar.setVisible(true);
      letter.setVisible(true);
      badge.setVisible(true);
      avatar.setFillStyle(p.is_bot ? 0x6a1b9a : 0x1976d2);
      letter.setText((p.name || '?').trim().charAt(0) || '?');
      const black = color === GK_BLACK;
      badge.setFillStyle(black ? 0x111111 : 0xfafafa);
      badge.setStrokeStyle(1, black ? 0xffffff : 0x999999, 0.9);
    };

    const myColor = this.myColorSelf();
    applyAvatar(this.myAvatar, this.myAvatarLetter, this.myStoneBadge, me, myColor);
    this.myPanel.setText(me ? `${me.name}${mark(me)}（${myColor === GK_BLACK ? '黑' : '白'}）` : '');
    this.myScoreText.setText(me ? `💰 ${store.sessionScores.get(me.id) ?? 0}` : '');

    if (opp) {
      const oppColor = this.myColorOf(opp.id);
      applyAvatar(this.oppAvatar, this.oppAvatarLetter, this.oppStoneBadge, opp, oppColor);
      this.oppPanel.setText(`${opp.name}${mark(opp)}（${oppColor === GK_BLACK ? '黑' : '白'}）`);
      this.oppScoreText.setText(`💰 ${store.sessionScores.get(opp.id) ?? 0}`);
    } else {
      this.oppAvatar.setVisible(false);
      this.oppAvatarLetter.setVisible(false);
      this.oppStoneBadge.setVisible(false);
      this.oppPanel.setText('等待对手…');
      this.oppScoreText.setText('');
    }
  }

  private renderTurn() {
    if (store.gameEnded || gkState.phase === 'ended') {
      this.turnText.setText('');
      return;
    }
    const pid = store.currentTurn;
    if (!pid) {
      this.turnText.setText('');
      return;
    }
    const isMe = pid === store.playerID;
    const name = store.player(pid)?.name ?? '';
    this.turnText.setText(isMe ? '轮到你落子' : `等待 ${name} 落子…`);
  }

  private renderAfkToggle() {
    const myAfk = store.afkPlayers.has(store.playerID);
    if (this.afkToggleTxt && this.afkToggleBg) {
      this.afkToggleTxt.setText(myAfk ? '取消托管' : '💤 托管');
      if (myAfk) this.drawAfkBtnActive(this.afkToggleBg);
      else this.drawAfkBtnNormal(this.afkToggleBg);
    }
  }

  private startCountdown(seconds: number) {
    this.timerEvent?.remove();
    if (seconds <= 0) return;
    let left = seconds;
    const base = this.turnText.text.replace(/\s*\(\d+s\)\s*$/, '').trim();
    const render = () => {
      if (left <= 0) {
        this.turnText.setText(base);
        return;
      }
      this.turnText.setText(`${base} (${left}s)`);
    };
    render();
    this.timerEvent = this.time.addEvent({
      delay: 1000,
      repeat: seconds - 1,
      callback: () => { left--; render(); },
    });
  }

  // --- 交互 ---

  private onClick(p: Phaser.Input.Pointer) {
    if (store.gameEnded || gkState.phase === 'ended') return;
    if (store.currentTurn !== store.playerID) return;
    if (store.afkPlayers.has(store.playerID)) return; // 挂机中由服务端托管

    // 像素 → 行列（四舍五入到最近交叉点；执白镜像时需反变换回服务端坐标系）
    const dCol = (p.x - (BOARD_CX - HALF)) / CELL;
    const dRow = (p.y - (BOARD_CY - HALF)) / CELL;
    const col = Math.round(this.flipped ? GK_SIZE - 1 - dCol : dCol);
    const row = Math.round(this.flipped ? GK_SIZE - 1 - dRow : dRow);
    if (row < 0 || row >= GK_SIZE || col < 0 || col >= GK_SIZE) return;
    // 点击偏离交叉点过远忽略（避免误触相邻线）
    const dx = Math.abs(p.x - this.cellX(col));
    const dy = Math.abs(p.y - this.cellY(row));
    if (dx > CELL * 0.45 || dy > CELL * 0.45) return;

    if (gkState.board[row * GK_SIZE + col] !== GK_EMPTY) {
      this.toast('该位置已有棋子');
      return;
    }
    net.send(MsgTypes.MsgGkMove, { row, col } satisfies GkMovePayload);
  }

  private toast(msg: string) {
    const t = this.add.text(BOARD_CX, BOARD_CY + HALF + 40, msg, {
      ...FONT, color: '#ff8a80', backgroundColor: '#000000aa', padding: { x: 10, y: 4 },
    }).setOrigin(0.5).setDepth(300); // 高于结算弹窗（200），避免提示被遮挡
    this.tweens.add({ targets: t, alpha: 0, delay: 1500, duration: 400, onComplete: () => t.destroy() });
  }

  // --- 消息处理 ---

  private subscribe() {
    const on = (t: Parameters<typeof net.on>[0], h: Parameters<typeof net.on>[1]) => this.unsubscribers.push(net.on(t, h));

    // ── 游戏开始：填充玩家并重置棋盘（epoch 守卫过滤过期重放）──
    on(MsgTypes.MsgGameStart, (p: GameStartPayload) => {
      if (store.gameStartEpoch !== this.enterEpoch) {
        console.warn(`[GomokuScene] 忽略过期 MsgGameStart (msg epoch=${store.gameStartEpoch}, enter=${this.enterEpoch})`);
        return;
      }
      for (const pl of p.players) {
        if (pl.id === store.playerID && pl.is_bot) pl.is_bot = false;
      }
      store.players = p.players;
      store.afkPlayers.clear();
      store.gameEnded = false;
      gkState.reset();
      gkState.board = new Array(GK_SIZE * GK_SIZE).fill(GK_EMPTY);
      gkState.phase = 'playing';
      this.renderBoard();
      this.renderPanels();
      this.renderAfkToggle();
    });

    on(MsgTypes.MsgGkTurn, (p: GkTurnPayload) => {
      store.currentTurn = p.player_id;
      gkState.phase = 'playing';
      this.renderTurn();
      this.startCountdown(p.timeout);
    });

    on(MsgTypes.MsgGkMoveMade, (p: GkMoveMadePayload) => {
      if (p.row < 0 || p.row >= GK_SIZE || p.col < 0 || p.col >= GK_SIZE) return;
      gkState.board[p.row * GK_SIZE + p.col] = p.color;
      gkState.lastRow = p.row;
      gkState.lastCol = p.col;
      gkState.moveNumber = p.move_number + 1;
      safePlay(this, 'stone'); // 落子音效（收/发双方均播放，与斗地主出牌音效一致）
      this.renderBoard();
    });

    on(MsgTypes.MsgGkAfkChanged, (p: GkAfkChangedPayload) => {
      if (store.gameEnded) return;
      if (p.afk) store.afkPlayers.add(p.player_id);
      else store.afkPlayers.delete(p.player_id);
      this.renderAfkToggle();
      this.renderPanels();
      if (p.player_id === store.playerID) {
        this.toast(p.afk ? '已挂机，机器人将自动落子' : '已取消挂机');
      }
    });

    on(MsgTypes.MsgGameOver, (p: GameOverPayload) => {
      store.gameEnded = true;
      this.gameEndedNormally = true;
      setFlowState(FlowState.GAME_OVER);
      net.clearPending();
      this.timerEvent?.remove();
      this.turnText.setText('');
      const myScore = p.scores.find((s) => s.player_id === store.playerID);
      if (myScore) {
        store.score += myScore.score;
        this.scoreText.setText(`🎯 ${store.score}  🏆 ${store.rank || '—'}`);
      }
      for (const s of p.scores) {
        store.sessionScores.set(s.player_id, (store.sessionScores.get(s.player_id) ?? 0) + s.score);
      }
      this.renderPanels();
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

    // ── 和棋请求通知：对手请求和棋时弹窗确认 ──
    on(MsgTypes.MsgGkDrawRequested, async (p: GkDrawRequestedPayload) => {
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '🤝 和棋请求',
        content: `对手 ${p.requester_name} 请求和棋，是否同意？`,
        width: 420,
        height: 250,
        onConfirm: () => {
          net.send(MsgTypes.MsgGkDrawResponse, { accept: true } satisfies GkDrawResponsePayload);
        },
        onCancel: () => {
          net.send(MsgTypes.MsgGkDrawResponse, { accept: false } satisfies GkDrawResponsePayload);
        },
      });
    });

    // ── 和棋请求结果：被拒绝时 toast 提示 ──
    on(MsgTypes.MsgGkDrawResult, (p: GkDrawResultPayload) => {
      if (!p.accepted) {
        this.toast('对手拒绝了和棋请求');
      }
    });

    // ── 同步检测响应：服务端无活跃对局时回退大厅 ──
    on(MsgTypes.MsgGameSyncResult, (p: GameSyncResultPayload) => {
      if (this.gameEndedNormally) return;
      if (p.status === 'active') return;
      console.warn(`[GomokuScene] 同步检测失败：服务端状态=${p.status}，回退大厅`);
      this.goToLobby();
    });

    // ── 状态恢复响应：同步拉取获得完整对局状态 ──
    on(MsgTypes.MsgGameState, (p: GameStatePayload) => {
      if (this.gameEndedNormally) return;
      if (!p.available || !p.state) {
        console.warn('[GomokuScene] 状态恢复：服务端无活跃对局，回退大厅');
        this.goToLobby();
        return;
      }
      const dto = p.state as GkGameStateDTO;
      this.stateReceived = true;
      store.stateReceived = true;
      gkState.restore(dto);
      this.renderBoard();
      this.renderPanels();
      this.renderTurn();
      this.renderAfkToggle();
      console.log('[GomokuScene] 状态恢复成功', `phase=${dto.phase}`, `move=${dto.move_number}`);
    });
  }

  private goToLobby() {
    this.gameEndedNormally = true;
    store.reset();
    gkState.reset();
    setFlowState(FlowState.LOBBY);
    this.scene.start('Lobby');
  }

  private showMaintenanceOverlay() {
    if (this.maintenanceOverlay) return;
    this.maintenanceOverlay = this.add.container(640, 360).setDepth(300);
    const bg = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6);
    const title = this.add.text(0, -40, '🛠️ 服务器维护中', { ...FONT, fontSize: '40px', color: '#ffd54f' }).setOrigin(0.5);
    const msg = this.add.text(0, 20, '本局结束后将无法继续开始新对局\n请稍后再试', {
      ...FONT, fontSize: '22px', align: 'center',
    }).setOrigin(0.5);
    this.maintenanceOverlay.add([bg, title, msg]);
  }

  private hideMaintenanceOverlay() {
    this.maintenanceOverlay?.destroy();
    this.maintenanceOverlay = undefined;
  }

  private showGameOverDialog(p: GameOverPayload) {
    const myScore = p.scores.find((s) => s.player_id === store.playerID)?.score ?? 0;
    const result = myScore > 0 ? '🎉 胜利！' : myScore < 0 ? '💀 失败' : '🤝 平局';
    const color = myScore > 0 ? '#ffd54f' : myScore < 0 ? '#ff8a80' : '#81d4fa';

    const dlg = this.add.container(640, 360).setDepth(200);
    const bg = this.add.rectangle(0, 0, 520, 380, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    const title = this.add.text(0, -140, result, { ...FONT, fontSize: '36px', color }).setOrigin(0.5);
    const winnerLine = this.add.text(0, -100,
      p.winner_id ? `获胜方：${p.winner_name}` : '双方战平',
      { ...FONT, fontSize: '20px' }).setOrigin(0.5);

    const lines: Phaser.GameObjects.GameObject[] = [];
    p.scores.forEach((s, i) => {
      lines.push(this.add.text(0, -60 + i * 30,
        `${s.player_name}  得分 ${s.score > 0 ? '+' : ''}${s.score}`,
        { ...FONT, fontSize: '18px' }).setOrigin(0.5));
    });

    // 按钮区域
    const btnY = 145;
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
    const btnText = this.add.text(returnBtnX, btnY, '返回房间', { ...FONT, fontSize: '20px' }).setOrigin(0.5);
    
    btn.on('pointerover', () => btn.setFillStyle(0x388e3c, 1));
    btn.on('pointerout', () => btn.setFillStyle(0x2e7d32, 1));
    btn.on('pointerdown', () => {
      dlg.destroy();
      store.resetGame();
      gkState.reset();
      // 服务端结算后房间已复位（机器人保持准备、真人取消准备），同步本地标记
      store.players.forEach((pl) => { pl.ready = !!pl.is_bot; });
      setFlowState(FlowState.ROOM);
      this.scene.start('Room');
    });

    dlg.add([bg, title, winnerLine, ...lines, replayBtn, replayBtnText, btn, btnText]);
  }
  
  /** 显示复盘报告对话框 */
  private async showReplayDialog(_p: GameOverPayload) {
    const roomCode = store.roomCode || '';
    if (!roomCode) {
      this.toast('无法获取房间信息');
      return;
    }

    try {
      const reportText = await fetchLatestReplayText('gomoku', roomCode, store.playerID || undefined);
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '📊 五子棋对局复盘',
        content: reportText,
        width: 800,
        height: 600,
      });
    } catch (err) {
      console.error('加载复盘报告失败:', err);
      this.toast(err instanceof Error && err.message === REPLAY_NOT_READY ? REPLAY_NOT_READY : '加载复盘报告失败，请稍后重试');
    }
  }
}
