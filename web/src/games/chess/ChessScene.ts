import Phaser from 'phaser';
import { net, hydrateIdentity } from '../../platform/net/ws';
import { store } from '../../platform/state/store';
import { setFlowState, FlowState } from '../../platform/flow/gameFlow';
import { ccState, CC_COLS, CC_ROWS, CC_EMPTY } from './state';
import { safePlay } from '../../platform/ui/audio';
import { fetchLatestReplayText, REPLAY_NOT_READY } from '../../platform/replay/replayApi';
import {
  MsgTypes,
  ErrorPayload,
  GameOverPayload,
  GameStartPayload,
  GameStatePayload,
  GameSyncResultPayload,
  CcAfkChangedPayload,
  CcAfkPayload,
  CcDrawRequestPayload,
  CcDrawRequestedPayload,
  CcDrawResponsePayload,
  CcDrawResultPayload,
  CcGameStateDTO,
  CcMoveMadePayload,
  CcMovePayload,
  CcResignPayload,
  CcTurnPayload,
  LeaveGamePayload,
  MaintenancePayload,
  StatsResultPayload,
} from '../../platform/protocol';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;

const CELL = 56;
const BOARD_CX = 380;
const HALF_X = ((CC_COLS - 1) * CELL) / 2;
const BOARD_CY = 390;
const HALF_Y = ((CC_ROWS - 1) * CELL) / 2;
const PIECE_R = 24;

const PIECE_NAMES: Record<number, [string, string]> = {
  1: ['帅', '将'],
  2: ['車', '车'],
  3: ['馬', '马'],
  4: ['炮', '砲'],
  5: ['相', '象'],
  6: ['仕', '士'],
  7: ['兵', '卒'],
};

/** 口播用棋子简体名（[红方, 黑方]）：繁体棋盘文字（車馬砲）可能被语音引擎误读 */
const CHESS_SPEAK_NAMES: Record<number, [string, string]> = {
  1: ['帅', '将'],
  2: ['车', '车'],
  3: ['马', '马'],
  4: ['炮', '炮'],
  5: ['相', '象'],
  6: ['士', '士'],
  7: ['兵', '卒'],
};

export class ChessScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];

  private boardGfx!: Phaser.GameObjects.Graphics;
  private pieceGfx!: Phaser.GameObjects.Graphics;
  private selectGfx!: Phaser.GameObjects.Graphics;

  private myPanel!: Phaser.GameObjects.Text;
  private oppPanel!: Phaser.GameObjects.Text;
  private myScoreText!: Phaser.GameObjects.Text;
  private oppScoreText!: Phaser.GameObjects.Text;
  // 头像卡（头像圆 + 首字 + 用户名 + 积分），下方永远是自己，上方是对手
  private myAvatar!: Phaser.GameObjects.Arc;
  private myAvatarLetter!: Phaser.GameObjects.Text;
  private oppAvatar!: Phaser.GameObjects.Arc;
  private oppAvatarLetter!: Phaser.GameObjects.Text;
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
  // 语音队列（走子棋谱口播，与斗地主 speakHandType 同构）
  private speechQueue: Array<{ text: string; pitch: number }> = [];
  private speechPlaying = false;
  private lastSpokenMoveNumber = -1; // 按手数去重，防重放/重发消息重复播报

  constructor() {
    super('ChessGame');
  }

  create() {
    const allScenes: Phaser.Scene[] = this.scene.manager.scenes;
    const other = allScenes.find(
      (s: Phaser.Scene) => s !== this && s instanceof ChessScene && s.sys.isActive(),
    );
    if (other) {
      console.warn('[ChessScene] 检测到重复创建，立即停止自身');
      this.scene.stop(this);
      return;
    }

    this.gameEndedNormally = false;
    this.stateReceived = false;
    this.unsubscribers = [];
    this.timerEvent = undefined;
    this.syncTimer = undefined;
    this.maintenanceOverlay = undefined;
    this.speechQueue = [];
    this.speechPlaying = false;
    this.lastSpokenMoveNumber = -1;
    // 身份兑底恢复：connected/reconnected 可能在此前无订阅者时到达且缓冲已被清理，
    // 恢复 store.playerID，否则落子守卫会静默丢弃所有点击、面板找不到自己
    hydrateIdentity();
    try { window.speechSynthesis?.cancel(); } catch { /* ignore */ }

    this.cameras.main.setBackgroundColor('#2b3a4a');

    this.buildBoard();
    this.buildSidePanels();
    this.buildScoreDisplay();
    this.buildSoundToggle();
    this.buildAfkToggle();
    this.buildResignButton();
    this.buildDrawButton();
    this.buildExitButton();

    setFlowState(FlowState.GAME);
    this.enterEpoch = store.gameStartEpoch;

    this.subscribe();
    this.input.on('pointerdown', (p: Phaser.Input.Pointer) => this.onClick(p));

    if (ccState.board.length === 0) ccState.board = new Array(CC_COLS * CC_ROWS).fill(CC_EMPTY);
    this.renderBoard();
    this.renderPanels();
    this.renderTurn();

    if (store.maintenance) this.showMaintenanceOverlay();

    store.inGame = true;

    if (location.hash !== '#/game') location.hash = '/game';

    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());

    net.send(MsgTypes.MsgRequestGameState);

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
    try { window.speechSynthesis?.cancel(); } catch { /* ignore */ }
    this.speechQueue = [];
    this.speechPlaying = false;
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
    if (!this.gameEndedNormally && !this.stateReceived && store.inGame) {
      net.send(MsgTypes.MsgLeaveGame, {} satisfies LeaveGamePayload);
    }
  }

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 中国象棋是静态场景，不需要每帧更新
    // 所有变化都通过事件驱动（网络消息、点击等）
  }

  // --- 布局 ---

  private buildBoard() {
    this.boardGfx = this.add.graphics();
    this.drawGrid();

    this.pieceGfx = this.add.graphics();
    this.selectGfx = this.add.graphics();
  }

  private drawGrid() {
    const g = this.boardGfx;
    const left = BOARD_CX - HALF_X;
    const top = BOARD_CY - HALF_Y;
    const right = BOARD_CX + HALF_X;
    const bottom = BOARD_CY + HALF_Y;
    const w = right - left;
    const h = bottom - top;

    // 底色（画在 Graphics 上，避免覆盖线条）
    g.fillStyle(0xd9a860, 1);
    g.fillRect(left, top, w, h);
    g.lineStyle(2, 0x8d6e3f, 1);
    g.strokeRect(left, top, w, h);

    // 横线
    g.lineStyle(1.5, 0x5d4322, 1);
    for (let r = 0; r < CC_ROWS; r++) {
      const y = top + r * CELL;
      g.lineBetween(left, y, right, y);
    }
    // 竖线（注意河界中间断开，但边线不断）
    for (let c = 0; c < CC_COLS; c++) {
      if (c === 0 || c === CC_COLS - 1) {
        g.lineBetween(left + c * CELL, top, left + c * CELL, bottom);
      } else {
        // 上半部分 row 0-4
        g.lineBetween(left + c * CELL, top, left + c * CELL, top + 4 * CELL);
        // 下半部分 row 5-9
        g.lineBetween(left + c * CELL, top + 5 * CELL, left + c * CELL, bottom);
      }
    }

    // 九宫格斜线
    const palaceTopX = left + 3 * CELL;
    const palaceTopY = top;
    g.lineBetween(palaceTopX, palaceTopY, palaceTopX + 2 * CELL, palaceTopY + 2 * CELL);
    g.lineBetween(palaceTopX + 2 * CELL, palaceTopY, palaceTopX, palaceTopY + 2 * CELL);
    const palaceBotX = left + 3 * CELL;
    const palaceBotY = top + 7 * CELL;
    g.lineBetween(palaceBotX, palaceBotY, palaceBotX + 2 * CELL, palaceBotY + 2 * CELL);
    g.lineBetween(palaceBotX + 2 * CELL, palaceBotY, palaceBotX, palaceBotY + 2 * CELL);

    // 九宫格内框（小十字标记，模拟传统象棋棋盘）
    const markSize = 4;
    const markCols = [0, 1, 7, 8];
    const markRows = [2, 7];
    g.lineStyle(1.5, 0x5d4322, 1);
    for (const mc of markCols) {
      for (const mr of markRows) {
        const cx = left + mc * CELL;
        const cy = top + mr * CELL;
        const onLeft = mc === 0;
        const onRight = mc === 8;
        const onTop = mr === 2;
        const onBot = mr === 7;
        if (onLeft && onTop) {
          // 左上
          g.lineBetween(cx, cy - markSize, cx, cy);
          g.lineBetween(cx - markSize, cy, cx, cy);
        } else if (onRight && onTop) {
          g.lineBetween(cx, cy - markSize, cx, cy);
          g.lineBetween(cx + markSize, cy, cx, cy);
        } else if (onLeft && onBot) {
          g.lineBetween(cx, cy + markSize, cx, cy);
          g.lineBetween(cx - markSize, cy, cx, cy);
        } else if (onRight && onBot) {
          g.lineBetween(cx, cy + markSize, cx, cy);
          g.lineBetween(cx + markSize, cy, cx, cy);
        }
      }
    }

    // 河界文字
    this.add.text(BOARD_CX, BOARD_CY, '楚 河          汉 界', {
      ...FONT, fontSize: '22px', color: '#5d4322',
    }).setOrigin(0.5).setDepth(5);
  }

  private buildSidePanels() {
    // 头像卡：棋盘会翻转保证己方棋子始终在下方，因此自己的头像固定在下、对手在上
    this.myAvatar = this.add.circle(762, 512, 22, 0x1976d2).setStrokeStyle(2, 0xffffff, 0.7);
    this.myAvatarLetter = this.add.text(762, 512, '', { ...FONT, fontSize: '20px', color: '#ffffff' }).setOrigin(0.5);
    this.myPanel = this.add.text(796, 502, '', { ...FONT, fontSize: '17px', color: '#ffe082' })
      .setOrigin(0, 0.5);
    this.myScoreText = this.add.text(796, 528, '', { ...FONT, fontSize: '14px', color: '#ffd700' })
      .setOrigin(0, 0.5);

    this.oppAvatar = this.add.circle(762, 212, 22, 0x6a1b9a).setStrokeStyle(2, 0xffffff, 0.7);
    this.oppAvatarLetter = this.add.text(762, 212, '', { ...FONT, fontSize: '20px', color: '#ffffff' }).setOrigin(0.5);
    this.oppPanel = this.add.text(796, 202, '', { ...FONT, fontSize: '17px', color: '#ffe082' })
      .setOrigin(0, 0.5);
    this.oppScoreText = this.add.text(796, 228, '', { ...FONT, fontSize: '14px', color: '#ffd700' })
      .setOrigin(0, 0.5);

    this.turnText = this.add.text(950, 350, '', {
      ...FONT, fontSize: '20px', backgroundColor: '#00000066', padding: { x: 10, y: 6 },
    }).setOrigin(0.5);
    this.moveText = this.add.text(950, 395, '', { ...FONT, fontSize: '14px', color: '#aaaaaa' })
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
      // 不必等待服务端 MsgCcAfkChanged 网络往返；服务端回执幂等，再同步一次。
      if (afk) store.afkPlayers.add(store.playerID);
      else store.afkPlayers.delete(store.playerID);
      this.renderAfkToggle();
      net.send(MsgTypes.MsgCcAfk, { afk } satisfies CcAfkPayload);
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
      if (store.gameEnded || ccState.phase === 'ended') return;
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '🏳️ 认输确认',
        content: '确定要认输吗？认输后对手获胜。',
        width: 400,
        height: 240,
        onConfirm: () => {
          net.send(MsgTypes.MsgCcResign, {} satisfies CcResignPayload);
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
      if (store.gameEnded || ccState.phase === 'ended') return;
      if (store.currentTurn !== store.playerID) {
        this.toast('只有你的回合才能请求和棋');
        return;
      }
      net.send(MsgTypes.MsgCcDrawRequest, { requester_id: store.playerID } satisfies CcDrawRequestPayload);
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
      ccState.reset();
      setFlowState(FlowState.LOBBY);
      this.scene.start('Lobby');
    });
  }

  // --- 坐标转换 ---

  /** 己方阵营（1=红, 2=黑）：优先用服务端 DTO 下发的 my_camp，开局初期回退按座位推断（0 号座执红）；座位未知时不翻转（默认红方视角） */
  private myCampOf(): number {
    if (ccState.myCamp === 1 || ccState.myCamp === 2) return ccState.myCamp;
    const seat = store.seatOf(store.playerID);
    if (seat === 0) return 1;
    if (seat === 1) return 2;
    return 1;
  }

  /** 棋盘翻转：己方执黑时上下左右镜像显示，保证己方棋子始终在下方（正常下棋视角） */
  private get flipped(): boolean {
    return this.myCampOf() === 2;
  }

  /** 逻辑列 → 屏幕坐标（执黑时镜像） */
  private cellX(col: number): number {
    const c = this.flipped ? CC_COLS - 1 - col : col;
    return BOARD_CX - HALF_X + c * CELL;
  }

  /** 逻辑行 → 屏幕坐标（执黑时镜像） */
  private cellY(row: number): number {
    const r = this.flipped ? CC_ROWS - 1 - row : row;
    return BOARD_CY - HALF_Y + r * CELL;
  }

  // --- 渲染 ---

  private renderBoard() {
    this.pieceGfx.clear();
    this.selectGfx.clear();
    
    // 清理旧的棋子文字（在重绘之前）
    this.cleanupPieceTexts();
    
    let board = ccState.board;
    // 防御性修正：保证棋盘数组长度恒为 9×10，防止异常数据（较短数组 → 越界读 undefined）导致整片 "?"。
    if (board.length !== CC_COLS * CC_ROWS) {
      const nb = new Array(CC_COLS * CC_ROWS).fill(CC_EMPTY);
      for (let i = 0; i < board.length && i < nb.length; i++) {
        const v = board[i];
        nb[i] = v && Number.isInteger(v) ? v : CC_EMPTY;
      }
      ccState.board = nb;
      board = ccState.board;
    }

    for (let r = 0; r < CC_ROWS; r++) {
      for (let c = 0; c < CC_COLS; c++) {
        const v = board[r * CC_COLS + c];
        if (v === CC_EMPTY) continue;
        this.drawPiece(r, c, v);
      }
    }

    // 最近一手高亮
    if (ccState.lastFromRow >= 0 && ccState.lastFromCol >= 0) {
      this.highlightCell(ccState.lastFromRow, ccState.lastFromCol, 0xffeb3b);
    }
    if (ccState.lastToRow >= 0 && ccState.lastToCol >= 0) {
      this.highlightCell(ccState.lastToRow, ccState.lastToCol, 0xff5252);
    }

    // 选中棋子高亮 + 合法目标
    if (ccState.selectedRow >= 0 && ccState.selectedCol >= 0) {
      this.highlightCell(ccState.selectedRow, ccState.selectedCol, 0x4caf50);
      for (const t of ccState.legalTargets) {
        const tx = this.cellX(t.col);
        const ty = this.cellY(t.row);
        const tv = board[t.row * CC_COLS + t.col];
        if (tv !== CC_EMPTY) {
          // 可吃子：红圈
          this.selectGfx.lineStyle(2, 0xff5252, 0.8);
          this.selectGfx.strokeCircle(tx, ty, PIECE_R + 3);
        } else {
          // 空位：绿点
          this.selectGfx.fillStyle(0x4caf50, 0.5);
          this.selectGfx.fillCircle(tx, ty, 8);
        }
      }
    }

    this.moveText.setText(ccState.moveNumber > 1 ? `第 ${ccState.moveNumber} 手` : '');
  }

  private drawPiece(row: number, col: number, piece: number) {
    // 防御性过滤：piece 不在合法区间（0 空 / 11-17 红 / 21-27 黑）时跳过渲染，
    // 避免棋盘因异常数据出现整片 "?" 或棋子错位。
    if (!Number.isInteger(piece) || piece <= 0) {
      console.warn('[ChessScene] 跳过非法棋子值:', piece, `row=${row} col=${col}`);
      return;
    }
    const camp = Math.floor(piece / 10);
    const type = piece % 10;
    if ((camp !== 1 && camp !== 2) || type < 1 || type > 7) {
      console.warn('[ChessScene] 跳过非法棋子值:', piece, `row=${row} col=${col}`);
      return;
    }

    const x = this.cellX(col);
    const y = this.cellY(row);
    const isRed = camp === 1;

    // 棋子底色
    this.pieceGfx.fillStyle(isRed ? 0xf5e6c8 : 0xe0d5c0, 1);
    this.pieceGfx.fillCircle(x, y, PIECE_R);
    this.pieceGfx.lineStyle(2, isRed ? 0xcc0000 : 0x333333, 1);
    this.pieceGfx.strokeCircle(x, y, PIECE_R);
    // 内圈
    this.pieceGfx.lineStyle(1, isRed ? 0xcc0000 : 0x555555, 0.6);
    this.pieceGfx.strokeCircle(x, y, PIECE_R - 3);

    // 文字：使用Graphics绘制，避免创建大量Text对象
    const names = PIECE_NAMES[type];
    const ch = names ? (isRed ? names[0] : names[1]) : '?';

    // 在pieceGfx上绘制文字（作为纹理）
    // 由于Graphics不支持直接绘制文字，我们改用更高效的方式：
    // 创建一个可复用的文字池，而不是每次创建新对象
    const txt = this.add.text(x, y, ch, {
      fontFamily: 'KaiTi, STKaiti, Arial',
      fontSize: '22px',
      color: isRed ? '#cc0000' : '#333333',
      fontStyle: 'bold',
    }).setOrigin(0.5).setDepth(10);

    this._pieceTexts.push(txt);
  }

  private _pieceTexts: Phaser.GameObjects.Text[] = [];

  private highlightCell(row: number, col: number, color: number) {
    const x = this.cellX(col);
    const y = this.cellY(row);
    this.selectGfx.lineStyle(2, color, 0.7);
    this.selectGfx.strokeRect(x - PIECE_R - 2, y - PIECE_R - 2, (PIECE_R + 2) * 2, (PIECE_R + 2) * 2);
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

    // 头像：机器人紫色底（与房间席位一致），真人用阵营底色，描边跟随棋子阵营
    const campColor = (camp: number) => (camp === 1 ? 0xcc0000 : 0x555555);
    const applyAvatar = (
      avatar: Phaser.GameObjects.Arc,
      letter: Phaser.GameObjects.Text,
      p: typeof me,
      camp: number,
    ) => {
      if (!p) {
        avatar.setVisible(false);
        letter.setVisible(false);
        return;
      }
      avatar.setVisible(true);
      letter.setVisible(true);
      avatar.setFillStyle(p.is_bot ? 0x6a1b9a : campColor(camp));
      avatar.setStrokeStyle(2, campColor(camp), 0.9);
      letter.setText((p.name || '?').trim().charAt(0) || '?');
    };

    const myCamp = this.myCampOf();
    applyAvatar(this.myAvatar, this.myAvatarLetter, me, myCamp);
    this.myPanel.setText(me ? `${me.name}${mark(me)}（${myCamp === 1 ? '红' : '黑'}）` : '');
    this.myScoreText.setText(me ? `💰 ${store.sessionScores.get(me.id) ?? 0}` : '');

    if (opp) {
      const oppCamp = this.campOfPid(opp.id);
      applyAvatar(this.oppAvatar, this.oppAvatarLetter, opp, oppCamp);
      this.oppPanel.setText(`${opp.name}${mark(opp)}（${oppCamp === 1 ? '红' : '黑'}）`);
      this.oppScoreText.setText(`💰 ${store.sessionScores.get(opp.id) ?? 0}`);
    } else {
      this.oppAvatar.setVisible(false);
      this.oppAvatarLetter.setVisible(false);
      this.oppPanel.setText('等待对手…');
      this.oppScoreText.setText('');
    }
  }

  private campOfPid(pid: string): number {
    return store.seatOf(pid) === 0 ? 1 : 2;
  }

  private renderTurn() {
    if (store.gameEnded || ccState.phase === 'ended') {
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
    this.turnText.setText(isMe ? '轮到你走子' : `等待 ${name} 走子…`);
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
    if (store.gameEnded || ccState.phase === 'ended') return;
    if (store.afkPlayers.has(store.playerID)) return;

    // 屏幕坐标 → 逻辑坐标（执黑镜像时需反变换回服务端坐标系）
    const dCol = (p.x - (BOARD_CX - HALF_X)) / CELL;
    const dRow = (p.y - (BOARD_CY - HALF_Y)) / CELL;
    const col = Math.round(this.flipped ? CC_COLS - 1 - dCol : dCol);
    const row = Math.round(this.flipped ? CC_ROWS - 1 - dRow : dRow);
    if (row < 0 || row >= CC_ROWS || col < 0 || col >= CC_COLS) return;

    const dx = Math.abs(p.x - this.cellX(col));
    const dy = Math.abs(p.y - this.cellY(row));
    if (dx > CELL * 0.45 || dy > CELL * 0.45) return;

    const piece = ccState.board[row * CC_COLS + col];
    const myCamp = ccState.myCamp;

    // 已选中棋子 → 点击合法目标则走子
    if (ccState.selectedRow >= 0) {
      const isTarget = ccState.legalTargets.some((t) => t.row === row && t.col === col);
      if (isTarget) {
        net.send(MsgTypes.MsgCcMove, {
          from_row: ccState.selectedRow,
          from_col: ccState.selectedCol,
          to_row: row,
          to_col: col,
        } satisfies CcMovePayload);
        ccState.clearSelection();
        this.cleanupPieceTexts();
        this.renderBoard();
        return;
      }
      // 点击己方另一棋子 → 切换选中
      if (piece !== CC_EMPTY && Math.floor(piece / 10) === myCamp && store.currentTurn === store.playerID) {
        this.selectPiece(row, col);
        this.cleanupPieceTexts();
        this.renderBoard();
        return;
      }
      // 其他 → 取消选中
      ccState.clearSelection();
      this.cleanupPieceTexts();
      this.renderBoard();
      return;
    }

    // 未选中 → 点击己方棋子选中
    if (store.currentTurn !== store.playerID) return;
    if (piece === CC_EMPTY || Math.floor(piece / 10) !== myCamp) return;

    this.selectPiece(row, col);
    this.cleanupPieceTexts();
    this.renderBoard();
  }

  private selectPiece(row: number, col: number) {
    ccState.selectedRow = row;
    ccState.selectedCol = col;
    ccState.legalTargets = this.computeLegalTargets(row, col);
  }

  /** 客户端简易合法走法计算（仅用于交互提示，服务端为权威） */
  private computeLegalTargets(row: number, col: number): Array<{ row: number; col: number }> {
    const board = ccState.board;
    const piece = board[row * CC_COLS + col];
    if (piece === CC_EMPTY) return [];
    const camp = Math.floor(piece / 10);
    const type = piece % 10;
    const targets: Array<{ row: number; col: number }> = [];

    const pseudo = this.pseudoMoves(row, col, camp, type);
    for (const [tr, tc] of pseudo) {
      // 模拟走子检查是否送将/对将
      const nb = [...board];
      nb[row * CC_COLS + col] = CC_EMPTY;
      nb[tr * CC_COLS + tc] = piece;
      if (!this.isInCheck(nb, camp) && this.flyingGeneralsOk(nb)) {
        targets.push({ row: tr, col: tc });
      }
    }
    return targets;
  }

  private pseudoMoves(row: number, col: number, camp: number, type: number): Array<[number, number]> {
    const board = ccState.board;
    const inB = (r: number, c: number) => r >= 0 && r < CC_ROWS && c >= 0 && c < CC_COLS;
    const notFriend = (r: number, c: number) => {
      const v = board[r * CC_COLS + c];
      return v === CC_EMPTY || Math.floor(v / 10) !== camp;
    };
    const moves: Array<[number, number]> = [];

    switch (type) {
      case 1: { // General
        const minR = camp === 1 ? 7 : 0;
        const maxR = camp === 1 ? 9 : 2;
        for (const [dr, dc] of [[-1, 0], [1, 0], [0, -1], [0, 1]]) {
          const nr = row + dr, nc = col + dc;
          if (nr >= minR && nr <= maxR && nc >= 3 && nc <= 5 && notFriend(nr, nc)) {
            moves.push([nr, nc]);
          }
        }
        break;
      }
      case 2: { // Chariot
        for (const [dr, dc] of [[-1, 0], [1, 0], [0, -1], [0, 1]]) {
          let r = row + dr, c = col + dc;
          while (inB(r, c)) {
            if (board[r * CC_COLS + c] === CC_EMPTY) {
              moves.push([r, c]);
            } else {
              if (notFriend(r, c)) moves.push([r, c]);
              break;
            }
            r += dr; c += dc;
          }
        }
        break;
      }
      case 3: { // Horse
        const steps = [
          [-2, -1, -1, 0], [-2, 1, -1, 0], [2, -1, 1, 0], [2, 1, 1, 0],
          [-1, -2, 0, -1], [-1, 2, 0, 1], [1, -2, 0, -1], [1, 2, 0, 1],
        ];
        for (const [dr, dc, lr, lc] of steps) {
          const nr = row + dr, nc = col + dc;
          if (!inB(nr, nc)) continue;
          if (board[(row + lr) * CC_COLS + (col + lc)] !== CC_EMPTY) continue;
          if (notFriend(nr, nc)) moves.push([nr, nc]);
        }
        break;
      }
      case 4: { // Cannon
        for (const [dr, dc] of [[-1, 0], [1, 0], [0, -1], [0, 1]]) {
          let r = row + dr, c = col + dc;
          let mounted = false;
          while (inB(r, c)) {
            if (!mounted) {
              if (board[r * CC_COLS + c] === CC_EMPTY) {
                moves.push([r, c]);
              } else {
                mounted = true;
              }
            } else {
              if (board[r * CC_COLS + c] !== CC_EMPTY) {
                if (notFriend(r, c)) moves.push([r, c]);
                break;
              }
            }
            r += dr; c += dc;
          }
        }
        break;
      }
      case 5: { // Elephant
        const dirs = [[-2, -2, -1, -1], [-2, 2, -1, 1], [2, -2, 1, -1], [2, 2, 1, 1]];
        for (const [dr, dc, br, bc] of dirs) {
          const nr = row + dr, nc = col + dc;
          if (!inB(nr, nc)) continue;
          if (camp === 1 && nr < 5) continue;
          if (camp === 2 && nr > 4) continue;
          if (board[(row + br) * CC_COLS + (col + bc)] !== CC_EMPTY) continue;
          if (notFriend(nr, nc)) moves.push([nr, nc]);
        }
        break;
      }
      case 6: { // Advisor
        const minR = camp === 1 ? 7 : 0;
        const maxR = camp === 1 ? 9 : 2;
        for (const [dr, dc] of [[-1, -1], [-1, 1], [1, -1], [1, 1]]) {
          const nr = row + dr, nc = col + dc;
          if (nr >= minR && nr <= maxR && nc >= 3 && nc <= 5 && notFriend(nr, nc)) {
            moves.push([nr, nc]);
          }
        }
        break;
      }
      case 7: { // Soldier
        const fwd = camp === 1 ? -1 : 1;
        const crossed = (camp === 1 && row <= 4) || (camp === 2 && row >= 5);
        const add = (dr: number, dc: number) => {
          const nr = row + dr, nc = col + dc;
          if (inB(nr, nc) && notFriend(nr, nc)) moves.push([nr, nc]);
        };
        add(fwd, 0);
        if (crossed) { add(0, -1); add(0, 1); }
        break;
      }
    }
    return moves;
  }

  private isInCheck(board: number[], camp: number): boolean {
    const cols = CC_COLS;
    const inB = (r: number, c: number) => r >= 0 && r < CC_ROWS && c >= 0 && c < CC_COLS;
    // Find general
    const genVal = camp === 1 ? 11 : 21;
    let gr = -1, gc = -1;
    for (let r = 0; r < CC_ROWS; r++) {
      for (let c = 0; c < CC_COLS; c++) {
        if (board[r * cols + c] === genVal) { gr = r; gc = c; break; }
      }
      if (gr >= 0) break;
    }
    if (gr < 0) return true;

    const enemy = camp === 1 ? 2 : 1;

    // Horse threats
    const horseOffsets = [
      [-2, -1, -1, 0], [-2, 1, -1, 0], [2, -1, 1, 0], [2, 1, 1, 0],
      [-1, -2, 0, -1], [-1, 2, 0, 1], [1, -2, 0, -1], [1, 2, 0, 1],
    ];
    for (const [dr, dc, lr, lc] of horseOffsets) {
      const hr = gr + dr, hc = gc + dc;
      if (!inB(hr, hc)) continue;
      const h = board[hr * cols + hc];
      if (h === CC_EMPTY || Math.floor(h / 10) !== enemy || h % 10 !== 3) continue;
      const legR = gr + lr, legC = gc + lc;
      if (board[legR * cols + legC] === CC_EMPTY) return true;
    }

    // Chariot & General threats (straight lines)
    const dirs = [[-1, 0], [1, 0], [0, -1], [0, 1]];
    for (const [dr, dc] of dirs) {
      let r = gr + dr, c = gc + dc;
      while (inB(r, c)) {
        const p = board[r * cols + c];
        if (p !== CC_EMPTY) {
          if (Math.floor(p / 10) === enemy && (p % 10 === 2 || p % 10 === 1)) return true;
          break;
        }
        r += dr; c += dc;
      }
    }

    // Cannon threats
    for (const [dr, dc] of dirs) {
      let r = gr + dr, c = gc + dc;
      let mounted = false;
      while (inB(r, c)) {
        const p = board[r * cols + c];
        if (!mounted) {
          if (p !== CC_EMPTY) mounted = true;
        } else {
          if (p !== CC_EMPTY) {
            if (Math.floor(p / 10) === enemy && p % 10 === 4) return true;
            break;
          }
        }
        r += dr; c += dc;
      }
    }

    // Soldier threats
    const soldierFwd = camp === 1 ? 1 : -1; // enemy soldier approaches from this direction
    for (const [dr, dc] of [[soldierFwd, 0], [0, -1], [0, 1]]) {
      const sr = gr + dr, sc = gc + dc;
      if (!inB(sr, sc)) continue;
      const s = board[sr * cols + sc];
      if (s === CC_EMPTY || Math.floor(s / 10) !== enemy || s % 10 !== 7) continue;
      if (dc !== 0) {
        if (enemy === 1 && sr > 4) continue;
        if (enemy === 2 && sr < 5) continue;
      }
      return true;
    }

    return false;
  }

  private flyingGeneralsOk(board: number[]): boolean {
    const cols = CC_COLS;
    let rr = -1, rc = -1, br = -1, bc = -1;
    for (let r = 0; r < CC_ROWS; r++) {
      for (let c = 0; c < CC_COLS; c++) {
        if (board[r * cols + c] === 11) { rr = r; rc = c; }
        if (board[r * cols + c] === 21) { br = r; bc = c; }
      }
    }
    if (rr < 0 || br < 0) return true;
    if (rc !== bc) return true;
    for (let r = br + 1; r < rr; r++) {
      if (board[r * cols + rc] !== CC_EMPTY) return true;
    }
    return false;
  }

  private cleanupPieceTexts() {
    for (const t of this._pieceTexts) t.destroy();
    this._pieceTexts = [];
  }

  /** 语音播报走法（Web Speech API，中文口语）：加入队列顺序播报，静音时不播 */
  private speakMove(piece: number, fromRow: number, toRow: number, captured: boolean, targetPiece: number, inCheck: boolean) {
    if (store.muted) return;
    let text = this.describeMove(piece, fromRow, toRow, captured, targetPiece);
    if (inCheck) text += '，将军！';
    // 限制队列长度，防止堆积过多语音
    if (this.speechQueue.length >= 5) this.speechQueue.shift();
    this.speechQueue.push({ text, pitch: inCheck ? 1.25 : 1.0 });
    this.processSpeechQueue();
  }

  /** 走法 → 中文口播（口语风格）：跳马 / 飞象 / 支士 / 进卒 / 炮打马 / 进车吃炮 …
   *  进/退判定：红方行号减小为进（红方在下），黑方相反；同行横走为平 */
  private describeMove(piece: number, fromRow: number, toRow: number, captured: boolean, targetPiece: number): string {
    const isRed = Math.floor(piece / 10) === 1;
    const type = piece % 10;
    // 口播统一用简体常用字，避免繁体棋子文字（車馬砲）被语音引擎误读
    const name = CHESS_SPEAK_NAMES[type][isRed ? 0 : 1];
    const target = captured ? CHESS_SPEAK_NAMES[targetPiece % 10][Math.floor(targetPiece / 10) === 1 ? 0 : 1] : '';
    const dir = fromRow === toRow ? '平' : (isRed ? toRow < fromRow : toRow > fromRow) ? '进' : '退';
    switch (type) {
      case 3: return '跳马';                      // 马
      case 5: return `飞${name}`;                 // 相/象
      case 6: return '支士';                      // 仕/士
      case 4: return captured ? `炮打${target}` : `${dir}炮`; // 炮：吃子播"打"
      default: {                                  // 车/帅/兵：进退平 + 吃子附目标
        const base = `${dir}${name}`;
        return captured ? `${base}吃${target}` : base;
      }
    }
  }

  /** 按顺序处理语音队列，确保每条语音完整播完再播下一条（与斗地主 processSpeechQueue 同构） */
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
      const voice = synth.getVoices().find((v) => v.lang.startsWith('zh'));
      if (voice) { u.voice = voice; u.lang = voice.lang; }
      else u.lang = 'zh-CN';
      const next = () => { this.speechPlaying = false; this.processSpeechQueue(); };
      u.onend = next;
      u.onerror = next;
      // 兜底：防止 onend/onerror 未触发导致队列卡死（短语播报不超 2s）
      this.time.delayedCall(4000, next);
      synth.speak(u);
    } catch {
      this.speechPlaying = false;
    }
  }

  private toast(msg: string) {
    const t = this.add.text(BOARD_CX, BOARD_CY + HALF_Y + 40, msg, {
      ...FONT, color: '#ff8a80', backgroundColor: '#000000aa', padding: { x: 10, y: 4 },
    }).setOrigin(0.5).setDepth(300); // 高于结算弹窗（200），避免提示被遮挡
    this.tweens.add({ targets: t, alpha: 0, delay: 1500, duration: 400, onComplete: () => t.destroy() });
  }

  // --- 消息处理 ---

  private subscribe() {
    const on = (t: Parameters<typeof net.on>[0], h: Parameters<typeof net.on>[1]) => this.unsubscribers.push(net.on(t, h));

    on(MsgTypes.MsgGameStart, (p: GameStartPayload) => {
      if (store.gameStartEpoch !== this.enterEpoch) {
        console.warn(`[ChessScene] ignore stale MsgGameStart (msg epoch=${store.gameStartEpoch}, enter=${this.enterEpoch})`);
        return;
      }
      for (const pl of p.players) {
        if (pl.id === store.playerID && pl.is_bot) pl.is_bot = false;
      }
      store.players = p.players;
      store.afkPlayers.clear();
      store.gameEnded = false;
      ccState.reset();
      ccState.board = new Array(CC_COLS * CC_ROWS).fill(CC_EMPTY);
      ccState.phase = 'playing';
      this.cleanupPieceTexts();
      this.renderBoard();
      this.renderPanels();
      this.renderAfkToggle();
    });

    on(MsgTypes.MsgCcTurn, (p: CcTurnPayload) => {
      store.currentTurn = p.player_id;
      ccState.phase = 'playing';
      ccState.clearSelection();
      this.cleanupPieceTexts();
      this.renderBoard();
      this.renderTurn();
      this.startCountdown(p.timeout);
    });

    on(MsgTypes.MsgCcMoveMade, (p: CcMoveMadePayload) => {
      if (p.from_row < 0 || p.to_row < 0) return;
      // 手数去重：重放/重发的同一条走子消息不重复播报
      if (p.move_number === this.lastSpokenMoveNumber) return;
      this.lastSpokenMoveNumber = p.move_number;
      // 是否吃子需在覆盖棋盘前判断（目标位原有敌子），旧盘快照同时用于将军判定
      const targetPiece = ccState.board[p.to_row * CC_COLS + p.to_col];
      const captured = targetPiece !== CC_EMPTY;
      const after = [...ccState.board];
      after[p.from_row * CC_COLS + p.from_col] = CC_EMPTY;
      after[p.to_row * CC_COLS + p.to_col] = p.piece;
      ccState.board[p.from_row * CC_COLS + p.from_col] = CC_EMPTY;
      ccState.board[p.to_row * CC_COLS + p.to_col] = p.piece;
      ccState.lastFromRow = p.from_row;
      ccState.lastFromCol = p.from_col;
      ccState.lastToRow = p.to_row;
      ccState.lastToCol = p.to_col;
      ccState.moveNumber = p.move_number + 1;
      ccState.clearSelection();
      const moverCamp = Math.floor(p.piece / 10);
      const inCheck = this.isInCheck(after, moverCamp === 1 ? 2 : 1); // 走完后对方是否被将军
      this.speakMove(p.piece, p.from_row, p.to_row, captured, targetPiece, inCheck);
      safePlay(this, captured ? 'chess_capture' : 'chess_move'); // 走子/吃子音效
      this.cleanupPieceTexts();
      this.renderBoard();
    });

    on(MsgTypes.MsgCcAfkChanged, (p: CcAfkChangedPayload) => {
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
    on(MsgTypes.MsgCcDrawRequested, async (p: CcDrawRequestedPayload) => {
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '🤝 和棋请求',
        content: `对手 ${p.requester_name} 请求和棋，是否同意？`,
        width: 420,
        height: 250,
        onConfirm: () => {
          net.send(MsgTypes.MsgCcDrawResponse, { accept: true } satisfies CcDrawResponsePayload);
        },
        onCancel: () => {
          net.send(MsgTypes.MsgCcDrawResponse, { accept: false } satisfies CcDrawResponsePayload);
        },
      });
    });

    // ── 和棋请求结果：被拒绝时 toast 提示 ──
    on(MsgTypes.MsgCcDrawResult, (p: CcDrawResultPayload) => {
      if (!p.accepted) {
        this.toast('对手拒绝了和棋请求');
      }
    });

    on(MsgTypes.MsgGameSyncResult, (p: GameSyncResultPayload) => {
      if (this.gameEndedNormally) return;
      if (p.status === 'active') return;
      console.warn(`[ChessScene] sync failed: status=${p.status}, going to lobby`);
      this.goToLobby();
    });

    on(MsgTypes.MsgGameState, (p: GameStatePayload) => {
      if (this.gameEndedNormally) return;
      if (!p.available || !p.state) {
        console.warn('[ChessScene] no active game, going to lobby');
        this.goToLobby();
        return;
      }
      const dto = p.state as CcGameStateDTO;
      this.stateReceived = true;
      store.stateReceived = true;
      ccState.restore(dto);
      this.cleanupPieceTexts();
      this.renderBoard();
      this.renderPanels();
      this.renderTurn();
      this.renderAfkToggle();
      console.log('[ChessScene] state restored', `phase=${dto.phase}`, `move=${dto.move_number}`);
    });
  }

  private goToLobby() {
    this.gameEndedNormally = true;
    store.reset();
    ccState.reset();
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
      this.gameEndedNormally = true;
      store.resetGame();
      ccState.reset();
      setFlowState(FlowState.LOBBY);
      this.scene.start('Lobby');
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
      const reportText = await fetchLatestReplayText('chess', roomCode, store.playerID || undefined);
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '📊 象棋对局复盘',
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
