import Phaser from 'phaser';
import { net } from '../../platform/net/ws';
import { store } from '../../platform/state/store';
import { setFlowState, FlowState } from '../../platform/flow/gameFlow';
import { mjState, tileName, tileSpeech, tileColor, MJ_HONOR_START } from './state';
import { fetchLatestReplayText, REPLAY_NOT_READY } from '../../platform/replay/replayApi';
import {
  MsgTypes,
  ErrorPayload,
  GameOverPayload,
  GameStartPayload,
  GameStatePayload,
  GameSyncResultPayload,
  MjActionAvailPayload,
  MjAfkChangedPayload,
  MjAfkPayload,
  MjDiscardPayload,
  MjDiscardedPayload,
  MjDicePayload,
  MjDrawPayload,
  MjKongMadePayload,
  MjKongPayload,
  MjPassPayload,
  MjPongPayload,
  MjPongMadePayload,
  MjTurnPayload,
  MjWinPayload,
  LeaveGamePayload,
  MaintenancePayload,
  StatsResultPayload,
} from '../../platform/protocol';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;
const SMALL_FONT = { fontFamily: 'Arial', fontSize: '16px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;
const BUTTON_FONT = { fontFamily: 'Arial', fontSize: '22px', color: '#ffffff', fontStyle: 'bold' } as Phaser.Types.GameObjects.Text.TextStyle;

const TILE_W = 44;
const TILE_H = 62;
const SMALL_TILE_W = 30;
const SMALL_TILE_H = 42;

// 画布尺寸（与斗地主保持一致）
const CANVAS_W = 1280;
const CANVAS_H = 720;

// 头像/面板位置：索引 = relPos（0=自己, 1=右家, 2=对家, 3=左家）
// 布局原则：头像和用户名避开手牌、弃牌池、面子区域
const AVATAR_POS = [
  { x: 160, y: 580 },    // 自己（左下角，远离底部手牌区）
  { x: 1228, y: 460 },  // 右家（右下角，避开右侧手牌x~1236和弃牌x~1120）
  { x: 320, y: 46 },    // 对家（左上角，避开中央手牌和弃牌区）
  { x: 52, y: 460 },    // 左家（左下角，避开左侧手牌x=14和弃牌x=130）
];

/** 腾讯欢乐麻将风格 - 全面优化版 */
export class MahjongScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];
  
  // Graphics层
  private handGfx!: Phaser.GameObjects.Graphics;
  private discardGfx!: Phaser.GameObjects.Graphics;
  private meldGfx!: Phaser.GameObjects.Graphics;
  private oppGfx!: Phaser.GameObjects.Graphics;
  private bgGfx!: Phaser.GameObjects.Graphics;
  
  // 信息文本
  private turnText!: Phaser.GameObjects.Text;
  private wallText!: Phaser.GameObjects.Text;
  private scoreText!: Phaser.GameObjects.Text;
  
  // 玩家头像与面板：索引 = relPos（0=自己, 1=右家, 2=对家, 3=左家）
  private avatars: Phaser.GameObjects.Arc[] = [];
  private avatarLetters: Phaser.GameObjects.Text[] = [];
  private seatPanels: Phaser.GameObjects.Text[] = [];
  private seatScoreTexts: Phaser.GameObjects.Text[] = []; // 头像下方的积分文本
  private turnRings: Phaser.GameObjects.Arc[] = []; // 当前回合呼吸光环
  
  // 操作按钮区（出牌、提示）
  private playPanel!: Phaser.GameObjects.Container;
  private discardBtn!: Phaser.GameObjects.Container;
  private hintBtn!: Phaser.GameObjects.Container;
  
  // 动作按钮（碰、杠、胡、不胡）- 按优先级排列（无吃）
  private actionContainer!: Phaser.GameObjects.Container;
  private pongBtn!: Phaser.GameObjects.Container;
  private kongBtn!: Phaser.GameObjects.Container;
  private winBtn!: Phaser.GameObjects.Container;
  private passBtn!: Phaser.GameObjects.Container;
  private passBtnLabel?: Phaser.GameObjects.Text;
  private actionCountdownText?: Phaser.GameObjects.Text;
  /** 自杠（暗杠/补杠）按钮展示中：区分服务端询问与本地自杠提示 */
  private selfKongShown = false;
  
  // 其他
  private timerEvent?: Phaser.Time.TimerEvent;
  private actionTimer?: Phaser.Time.TimerEvent;
  private turnSecondsLeft = 0;
  private actionSecondsLeft = 0;
  private lastDiscarderIdx = -1;
  private syncTimer?: Phaser.Time.TimerEvent;
  private maintenanceOverlay?: Phaser.GameObjects.Container;
  
  /** 语音播报相关 */
  private speechQueue: Array<{ text: string; pitch: number }> = [];
  private speechPlaying = false;
  private lastSpeakText = '';
  private lastSpeakSource = '';
  /** 相邻两条语音之间的间隔（毫秒）：便于用户听清并区分不同席位的播报 */
  private static readonly SPEECH_GAP_MS = 400;
  /** 语音队列上限：画面即时应用、语音只保最新——溢出丢弃最旧（宁可少播一条，也不念几拍前的旧牌） */
  private static readonly MAX_SPEECH_QUEUE = 3;
  
  /** render() 动态创建的文字对象：每次重绘前先销毁，避免无限累积 */
  private dynTexts: Phaser.GameObjects.Text[] = [];
  private gameEndedNormally = false;
  private stateReceived = false;
  private enterEpoch = 0;
  private newTileIndex = -1; // 新摸到的牌在手牌中的索引（高亮显示）

  constructor() {
    super('MahjongGame');
  }

  /** 语音播报文本（斗地主式"语音追画面"）：画面即时推进，语音只保最新若干条
   *  source 带席位标识（如 `discard-xxx`），避免多席位轮转时相同文本被误判为重复而吞音 */
  private speakText(text: string, source = 'discard') {
    if (!text || store.muted) return;
    // 同席位同文本才判重（跨席位的相同播报必须各自播出）
    if (text === this.lastSpeakText && source === this.lastSpeakSource) return;
    this.lastSpeakText = text;
    this.lastSpeakSource = source;
    // 队列满时丢弃最旧：画面已推进到最新一拍，旧语音再说只会加重"语音念旧牌"的错位
    if (this.speechQueue.length >= MahjongScene.MAX_SPEECH_QUEUE) this.speechQueue.shift();
    this.speechQueue.push({ text, pitch: 1.0 });
    this.processSpeechQueue();
  }

  /** 显示临时提示消息 */
  private toast(msg: string) {
    const t = this.add.text(CANVAS_W / 2, CANVAS_H - 80, msg, { 
      fontFamily: 'Arial', 
      fontSize: '16px', 
      color: '#ff8a80', 
      backgroundColor: '#000000aa', 
      padding: { x: 10, y: 4 } 
    })
      .setOrigin(0.5)
      .setDepth(300); // 高于结算弹窗，避免提示被遮挡
    this.tweens.add({ targets: t, alpha: 0, delay: 1500, duration: 400, onComplete: () => t.destroy() });
  }

  /** 处理语音队列：按顺序播报，每条约完整播完再播下一条 */
  private processSpeechQueue() {
    if (this.speechPlaying || this.speechQueue.length === 0) return;

    const item = this.speechQueue.shift();
    if (!item) return;

    this.speechPlaying = true;

    try {
      const synth = window.speechSynthesis;
      if (!synth) {
        this.speechPlaying = false;
        this.onSpeechIdle();
        return;
      }
      // 播前强杀："丢旧保新"策略的必要一环——新语音必须立即出声，不能被未播完的旧 utterance 堵住
      synth.cancel();

      const utterance = new SpeechSynthesisUtterance(item.text);
      utterance.rate = 1.0;
      utterance.pitch = item.pitch;

      // done 标记：onend/onerror 与估算兜底定时器只会触发一次后续动作，
      // 避免兜底定时器在后续语音播放中误触发导致跳播
      let done = false;
      const next = () => {
        if (done) return;
        done = true;
        this.speechPlaying = false;
        this.onSpeechIdle();
      };
      utterance.onend = next;
      utterance.onerror = next;
      // 兜底：防止 onend/onerror 未触发导致语音队列卡死。按文本长度估算时长（上限 4s）——
      // 固定长兜底会在 WebKit 不触发 onend 时把一拍拖得远超服务端拍间隔，语音将永久追不上画面
      this.time.delayedCall(this.speechEstimateMs(item.text), next);

      // 优先选择中文语音，确保“打三万/碰/杠/胡”等读法正确
      const voices = synth.getVoices();
      if (voices.length > 0) {
        const voice = voices.find((v) => v.lang.startsWith('zh'))
          ?? voices.find((v) => v.lang.startsWith('zh-CN'));
        if (voice) {
          utterance.voice = voice;
          utterance.lang = voice.lang;
        } else {
          utterance.lang = 'zh-CN';
        }
      } else {
        utterance.lang = 'zh-CN';
      }

      synth.speak(utterance);
    } catch (e) {
      console.warn('[Mahjong] 语音播报失败:', e);
      this.speechPlaying = false;
      this.onSpeechIdle();
    }
  }

  /** 播报时长估算（毫秒）：400ms 起播 + 每字 260ms，限幅 1.2~4s。
   *  用于 onend/onerror 未触发时的兜底放行，上限必须低于服务端单拍间隔（托管约 3~4.3s），
   *  否则语音消费速度低于画面产速，将追不上当前牌局 */
  private speechEstimateMs(text: string): number {
    return Math.min(4000, Math.max(1200, 400 + 260 * text.length));
  }

  /** 播报间隔：上一条播完后留一拍空隙再播下一条，便于区分不同席位的播报 */
  private onSpeechIdle() {
    if (this.speechQueue.length > 0) {
      this.time.delayedCall(MahjongScene.SPEECH_GAP_MS, () => this.processSpeechQueue());
    }
  }

  /** 清空语音队列（重连/开局/结算/退出：画面跳到最新，未播的旧语音已无意义） */
  private clearSpeechQueue() {
    this.speechQueue = [];
    this.speechPlaying = false;
    this.lastSpeakText = '';
    this.lastSpeakSource = '';
    try { window.speechSynthesis.cancel(); } catch { /* ignore */ }
  }

  create() {
    const allScenes: Phaser.Scene[] = this.scene.manager.scenes;
    const other = allScenes.find(
      (s: Phaser.Scene) => s !== this && s instanceof MahjongScene && s.sys.isActive(),
    );
    if (other) {
      console.warn('[MahjongScene] 检测到重复创建，立即停止自身');
      this.scene.stop(this);
      return;
    }

    this.gameEndedNormally = false;
    this.stateReceived = false;
    this.unsubscribers = [];
    this.timerEvent = undefined;
    this.syncTimer = undefined;
    this.maintenanceOverlay = undefined;

    this.buildUI();
    setFlowState(FlowState.GAME);
    this.enterEpoch = store.gameStartEpoch;

    this.subscribe();
    this.input.on('pointerdown', (p: Phaser.Input.Pointer, currentlyOver: Phaser.GameObjects.GameObject[]) => {
      // 忽略点落到 UI 交互对象（提示/出牌/碰杠胡/托管/退出等按钮）上的点击：
      // 否则场景级点击逻辑会紧接着清空 selectedTile / 收起自杠按钮（hideActions），
      // 导致按钮 80ms 后真正执行动作时条件已失效（如自杠点击无反应）。
      // 注意：Phaser 3.85 的 Pointer 没有 downObject 属性，必须用事件第二参数
      // currentlyOver（压在指针下的交互对象列表）判断。
      if (currentlyOver && currentlyOver.length > 0) return;
      this.onClick(p);
    });

    this.render();

    if (store.maintenance) this.showMaintenanceOverlay();

    store.inGame = true;

    if (location.hash !== '#/game') location.hash = '/game';

    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());

    net.send(MsgTypes.MsgRequestGameState);

    this.syncTimer = this.time.addEvent({
      delay: 30000,
      loop: true,
      callback: () => {
        if (!this.gameEndedNormally) net.send(MsgTypes.MsgGameSync);
      },
    });
  }

  shutdown() {
    this.timerEvent?.remove();
    this.actionTimer?.remove();
    this.syncTimer?.remove();
    this.clearSpeechQueue();
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
    if (!this.gameEndedNormally && !this.stateReceived && store.inGame) {
      net.send(MsgTypes.MsgLeaveGame, {} satisfies LeaveGamePayload);
    }
  }

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 呼吸光环动画（每帧更新）
    const time = this.time.now / 1000;
    for (let i = 0; i < this.turnRings.length; i++) {
      const ring = this.turnRings[i];
      if (ring && ring.visible) {
        const pulse = 0.5 + 0.5 * Math.sin(time * 3);
        ring.setAlpha(0.3 + pulse * 0.4);
        ring.setScale(1 + pulse * 0.15);
      }
    }
  }

  // --- UI 构建 ---

  private buildUI() {
    // 背景 Graphics 层（最底层）
    this.bgGfx = this.add.graphics();
    this.drawBackground();
    
    // 游戏 Graphics 层
    this.handGfx = this.add.graphics();
    this.discardGfx = this.add.graphics();
    this.meldGfx = this.add.graphics();
    this.oppGfx = this.add.graphics();

    // 顶部标题栏
    // this.add.text(CANVAS_W / 2, 18, ' 🀄 欢乐麻将 ', { 
    //   fontFamily: 'Arial', fontSize: '20px', fontStyle: 'bold', 
    //   color: '#ffe082', backgroundColor: '#00000088', padding: { x: 16, y: 4 }
    // }).setOrigin(0.5);
    
    // 得分显示（右上角）
    this.scoreText = this.add.text(CANVAS_W - 16, 16, `🎯 ${store.score}  🏆 ${store.rank || '—'}`, {
      ...FONT, fontSize: '16px', color: '#ffd54f', backgroundColor: '#00000088', padding: { x: 10, y: 4 },
    }).setOrigin(1, 0);
    
    // 剩余牌数（顶部中央偏左，带方框）
    this.wallText = this.add.text(20, 14, '', {
      ...FONT, fontSize: '14px', color: '#ffd54f', backgroundColor: '#00000088', padding: { x: 10, y: 4 },
    });
    
    // 轮到提示（自己手牌上方、操作按钮区之上，含倒计时；空文本时隐藏避免残留灰底块）
    this.turnText = this.add.text(CANVAS_W / 2, 562, '', {
      ...FONT, fontSize: '24px', backgroundColor: '#000000cc', padding: { x: 16, y: 7 }
    }).setOrigin(0.5).setDepth(55).setVisible(false);
    
    // 玩家面板
    this.buildPanels();
    
    // 操作按钮区
    this.buildPlayPanel();
    
    // 动作按钮
    this.buildActionButtons();
    
    // 托管按钮
    this.buildAfkToggle();
    
    // 退出按钮
    this.buildExitButton();
  }

  /** 绘制背景（毛毡纹理感 + 中央弃牌区边框） */
  private drawBackground() {
    const g = this.bgGfx;
    g.clear();
    
    // 深绿色桌面（模拟毛毡）
    g.fillStyle(0x0d4f2a, 1);
    g.fillRect(0, 0, CANVAS_W, CANVAS_H);
    
    // 添加微妙的纹理效果（通过细微的颜色变化）
    // 四角渐暗，中央微亮，模拟灯光效果
    const cx = CANVAS_W / 2;
    const cy = CANVAS_H / 2;
    for (let y = 0; y < CANVAS_H; y += 20) {
      for (let x = 0; x < CANVAS_W; x += 20) {
        const dx = (x - cx) / cx;
        const dy = (y - cy) / cy;
        const dist = Math.sqrt(dx * dx + dy * dy);
        const brightness = Math.max(0, 1 - dist * 0.6);
        const r = Math.round(13 + brightness * 15);
        const gg = Math.round(79 + brightness * 25);
        const b = Math.round(42 + brightness * 15);
        g.fillStyle((r << 16) | (gg << 8) | b, 0.06);
        g.fillRect(x, y, 20, 20);
      }
    }
    
    // 中央河区（四方弃牌托底，腾讯欢乐麻将桌面风格）
    // 宽度收窄至 780：给上/下家碰杠面子区腾出更多空白，保证面子牌完整显示
    const px = 250, py = 152, pw = 780, ph = 330;
    // 河区暗色托底 + 金属光泽上缘
    g.fillStyle(0x0a3d22, 0.55);
    g.fillRoundedRect(px, py, pw, ph, 22);
    g.fillStyle(0xffffff, 0.04);
    g.fillRoundedRect(px, py, pw, 10, 22);
    // 金色外框
    g.lineStyle(2.5, 0xd4a017, 0.55);
    g.strokeRoundedRect(px, py, pw, ph, 22);
    // 内层细框
    g.lineStyle(1.5, 0xffd700, 0.18);
    g.strokeRoundedRect(px + 6, py + 6, pw - 12, ph - 12, 18);
    // 十字分隔线（区分四方弃牌象限）
    const mx = CANVAS_W / 2;
    const my = py + ph / 2;
    g.lineStyle(1.5, 0xffd700, 0.10);
    g.lineBetween(px + 12, my, px + pw - 12, my);
    g.lineBetween(mx, py + 12, mx, py + ph - 12);
    // 中央"中"字徽记（极淡装饰）
    g.fillStyle(0xffd700, 0.05);
    g.fillCircle(mx, my, 48);
    g.lineStyle(2, 0xffd700, 0.10);
    g.strokeCircle(mx, my, 48);
  }

  /** 构建四个席位的头像与面板（腾讯风格：精美圆头像 + 呼吸光环） */
  private buildPanels() {
    this.avatars = [];
    this.avatarLetters = [];
    this.seatPanels = [];
    this.seatScoreTexts = [];
    this.turnRings = [];
    
    for (let rel = 0; rel < 4; rel++) {
      const pos = AVATAR_POS[rel];
      
      // 呼吸光环（在头像后面）
      const ring = this.add.circle(pos.x, pos.y, 28, 0xffd700, 0)
        .setStrokeStyle(2, 0xffd700, 0)
        .setDepth(9)
        .setVisible(false);
      this.turnRings.push(ring);
      
      const avatar = this.add.circle(pos.x, pos.y, 22, 0x445566)
        .setStrokeStyle(2, 0xffffff, 0.5)
        .setDepth(10)
        .setVisible(false);
      
      const letter = this.add.text(pos.x, pos.y, '', {
        ...FONT, fontSize: '20px', fontStyle: 'bold', color: '#ffffff',
      }).setOrigin(0.5).setDepth(11).setVisible(false);
      
      // 名字面板方向：根据席位方位放在头像外侧，朝中央倾斜
      let textX = pos.x;
      let textY = pos.y;
      let originX = 0.5;
      let originY = 0.5;
      switch (rel) {
        case 0: // 自己（左下角）：名字在头像下方
          textX = pos.x; textY = pos.y + 34; originX = 0.5; originY = 0; break;
        case 1: // 右家（右下角）：名字在头像上方，避开手牌和弃牌区
          textX = pos.x; textY = pos.y - 34; originX = 0.5; originY = 1; break;
        case 2: // 对家（左上角）：名字在头像右侧
          textX = pos.x + 34; textY = pos.y; originX = 0; originY = 0.5; break;
        case 3: // 左家（左下角）：名字在头像上方，避开手牌和弃牌区
          textX = pos.x; textY = pos.y - 34; originX = 0.5; originY = 1; break;
      }
      const panel = this.add.text(textX, textY, '', {
        ...FONT, fontSize: '16px',
      }).setOrigin(originX, originY).setDepth(10).setVisible(false);

      // 积分文本：头像下方（自己席位在名字面板下方，避开重叠）
      const scoreY = rel === 0 ? pos.y + 58 : pos.y + 30;
      const scoreText = this.add.text(pos.x, scoreY, '', {
        ...FONT, fontSize: '13px', color: '#ffd54f', stroke: '#000000', strokeThickness: 2,
      }).setOrigin(0.5, 0).setDepth(10).setVisible(false);

      this.avatars.push(avatar);
      this.avatarLetters.push(letter);
      this.seatPanels.push(panel);
      this.seatScoreTexts.push(scoreText);
    }
  }

  /** 根据 relPos 渲染单个席位的头像与面板 */
  private renderSeatPanel(rel: number, pl: { playerName: string; isDealer: boolean; afk: boolean; online: boolean; playerId: string; handCount: number; score: number }) {
    if (rel < 0 || rel >= 4) return;
    const avatar = this.avatars[rel];
    const letter = this.avatarLetters[rel];
    const panel = this.seatPanels[rel];
    const scoreText = this.seatScoreTexts[rel];
    const ring = this.turnRings[rel];

    avatar.setVisible(true);
    letter.setVisible(true);
    panel.setVisible(true);
    scoreText.setVisible(true);
    
    const isTurn = pl.playerId === mjState.currentTurn && mjState.phase !== 'ended';
    
    // 呼吸光环：只在当前回合显示
    ring.setVisible(isTurn);
    ring.setFillStyle(isTurn ? 0xffd700 : 0x000000, isTurn ? 0.2 : 0);
    ring.setStrokeStyle(isTurn ? 3 : 0, isTurn ? 0xffd700 : 0x000000, 0.8);
    
    // 头像底色
    avatar.setFillStyle(isTurn ? 0xff7043 : (rel === 0 ? 0xc0392b : 0x445566));
    // 边框：当前回合金色发光 > 庄家金圈 > 普通白圈
    if (isTurn) avatar.setStrokeStyle(3, 0xffd54f, 1);
    else if (pl.isDealer) avatar.setStrokeStyle(3, 0xffc107, 0.9);
    else avatar.setStrokeStyle(2, 0xffffff, 0.5);
    
    letter.setText(pl.playerName ? pl.playerName.charAt(0).toUpperCase() : '?');
    
    // 显示身份标记（手牌数已由牌背直观呈现，不再拼在名字后）
    const dealer = pl.isDealer ? ' 🏮' : '';
    const afk = pl.afk ? ' 💤' : '';
    const off = pl.online === false ? ' 📴' : '';
    panel.setText(`${pl.playerName}${dealer}${afk}${off}`);
    // 头像下方显示本轮累计积分（参考斗地主方式，会话内跨局累计）
    scoreText.setText(`💰 ${store.sessionScores.get(pl.playerId) ?? 0}`);
    panel.setColor(isTurn ? '#ffd54f' : (rel === 0 ? '#ffe082' : '#ffffff'));
  }

  /** 中央回合提示（含倒计时秒数，最后 5 秒变红）
   *  位置在手牌上方 532、操作按钮（y≈582 顶边）之上：
   *  空文本时 setVisible(false)，避免 Phaser 空 Text 的 padding 背景残留灰色小块 */
  private renderTurnText() {
    const hide = () => this.turnText.setVisible(false);
    if (mjState.phase === 'ended' || mjState.players.length === 0 || !mjState.currentTurn) {
      hide();
      return;
    }
    const turnPlayer = mjState.players.find(p => p.playerId === mjState.currentTurn);
    if (!turnPlayer) {
      hide();
      return;
    }
    const isMe = turnPlayer.playerId === store.playerID;
    const urgent = this.turnSecondsLeft > 0 && this.turnSecondsLeft <= 5;
    const sec = this.turnSecondsLeft > 0 ? `  ${this.turnSecondsLeft}s` : '';
    this.turnText.setText(isMe ? `🎯 轮到你出牌${sec}` : `等待 ${turnPlayer.playerName}${sec}`);
    this.turnText.setStyle({
      ...FONT,
      fontSize: isMe ? '24px' : '20px',
      fontStyle: 'bold',
      color: urgent ? '#ff8a80' : (isMe ? '#ffd54f' : '#ffffff'),
      backgroundColor: '#000000cc',
      padding: { x: 16, y: 7 },
    });
    this.turnText.setVisible(true);
    // 阶段提示已移除：与中央回合提示重复，只保留中央通知
  }

  /** 启动回合倒计时（服务端超时后自动出牌/托管） */
  private startTurnCountdown(seconds: number) {
    this.timerEvent?.remove();
    this.timerEvent = undefined;
    this.turnSecondsLeft = seconds > 0 ? seconds : 0;
    if (this.turnSecondsLeft <= 0) return;
    this.timerEvent = this.time.addEvent({
      delay: 1000,
      loop: true,
      callback: () => {
        this.turnSecondsLeft = Math.max(0, this.turnSecondsLeft - 1);
        this.renderTurnText();
        if (this.turnSecondsLeft <= 0) {
          this.timerEvent?.remove();
          this.timerEvent = undefined;
        }
      },
    });
  }

  /** 根据玩家下标返回其相对自己的牌桌称呼：对家 / 下家 / 上家；自己返回 ''（无需称呼） */
  private seatRelation(idx: number): string {
    const n = mjState.players.length;
    if (n === 0 || mjState.myIndex < 0) return '';
    const rel = (idx - mjState.myIndex + n) % n;
    if (rel === 0) return '';   // 自己
    if (rel === 2) return '对家'; // 对面
    if (rel === 1) return '下家'; // 下一家行动者
    return '上家';               // rel === 3，上一家行动者
  }

  /** 腾讯风格动作印章：大字从该玩家头像旁弹出并渐隐 */
  private showActionStamp(playerId: string, text: string, color: string) {
    const n = mjState.players.length;
    if (n === 0 || mjState.myIndex < 0) return;
    const idx = mjState.players.findIndex(pl => pl.playerId === playerId);
    if (idx < 0) return;
    const rel = (idx - mjState.myIndex + n) % n;
    const base = AVATAR_POS[rel < 4 ? rel : 2];
    const pos = { x: base.x, y: base.y + (rel === 0 ? -80 : 70) };
    
    const stamp = this.add.text(pos.x, pos.y, text, {
      fontFamily: 'Arial', fontSize: '52px', fontStyle: 'bold', color,
      stroke: '#000000', strokeThickness: 8,
    }).setOrigin(0.5).setDepth(90).setScale(2).setAlpha(0);
    
    this.tweens.add({ targets: stamp, scale: 1, alpha: 1, duration: 200, ease: 'Back.Out' });
    this.tweens.add({
      targets: stamp, alpha: 0, delay: 1200, duration: 500,
      onComplete: () => stamp.destroy(),
    });
  }

  /** 胡牌全屏金色闪光特效 */
  private showWinEffect() {
    // 金色半透明遮罩
    const flash = this.add.rectangle(CANVAS_W / 2, CANVAS_H / 2, CANVAS_W, CANVAS_H, 0xffd700, 0)
      .setDepth(95);
    
    this.tweens.add({
      targets: flash,
      alpha: 0.4,
      duration: 150,
      yoyo: true,
      repeat: 3,
      onComplete: () => flash.destroy(),
    });
    
    // 金色粒子效果（简化版：多个小圆点飞散）
    for (let i = 0; i < 20; i++) {
      const angle = (i / 20) * Math.PI * 2;
      const dot = this.add.circle(CANVAS_W / 2, CANVAS_H / 2, 6 + Math.random() * 4, 0xffd700)
        .setDepth(96).setAlpha(0.8);
      const targetX = CANVAS_W / 2 + Math.cos(angle) * 200;
      const targetY = CANVAS_H / 2 + Math.sin(angle) * 150;
      this.tweens.add({
        targets: dot,
        x: targetX,
        y: targetY,
        alpha: 0,
        scale: 0.3,
        duration: 800,
        ease: 'Cubic.Out',
        onComplete: () => dot.destroy(),
      });
    }
  }

  /** 被碰/吃/杠的牌从弃牌池移除（牌被"拿走"的视觉效果） */
  private claimDiscardedTile(fromPlayerId: string, tile: number) {
    if (!fromPlayerId) return;
    const idx = mjState.players.findIndex(pl => pl.playerId === fromPlayerId);
    if (idx < 0) return;
    const pool = mjState.discardPools[idx];
    if (!pool || pool.length === 0) return;
    for (let i = pool.length - 1; i >= 0; i--) {
      if (pool[i] === tile) {
        pool.splice(i, 1);
        break;
      }
    }
    if (this.lastDiscarderIdx === idx) this.lastDiscarderIdx = -1;
  }

  /** 从手牌中移除 n 张指定牌（碰/杠后取走，单独放到面子区，不再留在手牌） */
  private removeTilesFromHand(hand: number[] | undefined, tile: number, n: number) {
    if (!hand) return;
    let removed = 0;
    for (let i = hand.length - 1; i >= 0 && removed < n; i--) {
      if (hand[i] === tile) {
        hand.splice(i, 1);
        removed++;
      }
    }
  }

  /** 构建操作按钮区 - 出牌在最右侧，整体水平居中，方便操作与查看 */
  private buildPlayPanel() {
    const anchorX = CANVAS_W / 2 + 130;
    this.playPanel = this.add.container(anchorX, 610).setDepth(40);

    // 出牌按钮（最右侧，最大最醒目绿色）
    this.discardBtn = this.createGradientButton(0, 0, 130, 55, '出牌', 0x2e7d32, 0x66bb6a, () => this.discardSelected());

    // 提示按钮（出牌左侧，始终可见）
    this.hintBtn = this.createGradientButton(-150, 0, 100, 48, '💡 提示', 0xe65100, 0xff9800, () => this.showHint());

    this.playPanel.add([this.discardBtn, this.hintBtn]);

    // 初始状态：隐藏出牌按钮
    this.discardBtn.setVisible(false);
  }

  /** 创建渐变光泽按钮（腾讯风格） */
  private createGradientButton(
    x: number, y: number, w: number, h: number, 
    text: string, color1: number, color2: number, 
    onClick: () => void
  ): Phaser.GameObjects.Container {
    const container = this.add.container(x, y);
    
    const g = this.add.graphics();
    // 底部阴影
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-w / 2 + 3, -h / 2 + 5, w, h, 12);
    // 主按钮渐变（模拟：用两层近似渐变）
    g.fillStyle(color1, 1);
    g.fillRoundedRect(-w / 2, -h / 2, w, h, 12);
    // 上层亮色渐变覆盖（模拟光泽）
    g.fillStyle(color2, 0.5);
    g.fillRoundedRect(-w / 2, -h / 2, w, h / 2, 12);
    // 边框
    g.lineStyle(2, 0xffffff, 0.9);
    g.strokeRoundedRect(-w / 2, -h / 2, w, h, 12);
    // 顶部高光
    g.lineStyle(1, 0xffffff, 0.5);
    g.strokeRoundedRect(-w / 2 + 3, -h / 2 + 2, w - 6, h / 3 - 2, 8);
    
    const interactive = this.add.rectangle(0, 0, w, h, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    
    const btnText = this.add.text(0, 0, text, { ...BUTTON_FONT, fontSize: text.length > 2 ? '18px' : '22px' })
      .setOrigin(0.5);
    
    container.add([g, interactive, btnText]);
    
    interactive.on('pointerover', () => {
      this.tweens.add({ targets: container, scale: 1.05, duration: 100 });
    });
    interactive.on('pointerout', () => {
      this.tweens.add({ targets: container, scale: 1, duration: 100 });
    });
    interactive.on('pointerdown', () => {
      container.setScale(0.95);
      onClick();
      this.time.delayedCall(100, () => container.setScale(1));
    });
    
    return container;
  }

  /** 构建动作按钮 - 腾讯风格：按优先级排列（胡>杠>碰），无吃 */
  private buildActionButtons() {
    this.actionContainer = this.add.container(CANVAS_W / 2, 410).setDepth(50).setVisible(false);
    
    const btnY = 0;
    const btnSpacing = 115;
    
    // 按优先级排列：胡 > 杠 > 碰
    // 胡（最大最醒目，红色）
    this.winBtn = this.createActionBtn('胡', 0xff1744, 0xff5252, 120, 65, -btnSpacing * 1.5, btnY, () => {
      if (mjState.actionAvail && mjState.canWin) {
        net.send(MsgTypes.MsgMjWin, { is_self_draw: mjState.selfDrew } satisfies MjWinPayload);
        this.hideActions();
      }
    });
    
    // 杠（第二，粉色）
    this.kongBtn = this.createActionBtn('杠', 0xff4081, 0xff80ab, 100, 58, -btnSpacing * 0.5, btnY, () => {
      if (mjState.actionAvail && mjState.canKong) {
        net.send(MsgTypes.MsgMjKong, { tile: mjState.actionTile } satisfies MjKongPayload);
        this.hideActions();
      }
    });
    
    // 碰（第三，金色）
    this.pongBtn = this.createActionBtn('碰', 0xffab00, 0xffd54f, 100, 58, btnSpacing * 0.5, btnY, () => {
      if (mjState.actionAvail && mjState.canPong) {
        net.send(MsgTypes.MsgMjPong, { tile: mjState.actionTile } satisfies MjPongPayload);
        this.hideActions();
      }
    });

    // 不胡/忽略：可胡时显示“不胡”（紧挨胡按钮）；碰/杠询问时显示“忽略”，
    // 无需等倒计时自动结束（与胡/杠槽位互斥，不会重叠）
    this.passBtn = this.createActionBtn('不胡', 0x546e7a, 0x90a4ae, 100, 58, -btnSpacing * 0.5, btnY, () => {
      if (mjState.actionAvail && (mjState.canWin || mjState.canPong || mjState.canKong) && !this.selfKongShown) {
        net.send(MsgTypes.MsgMjPass, {} satisfies MjPassPayload);
        this.hideActions();
      }
    });
    this.passBtnLabel = this.passBtn.list[2] as Phaser.GameObjects.Text;

    this.actionContainer.add([this.winBtn, this.kongBtn, this.pongBtn, this.passBtn]);
    
    // 倒计时提示
    this.actionCountdownText = this.add.text(0, -50, '', {
      ...SMALL_FONT, fontSize: '16px', color: '#ffcc80', fontStyle: 'bold',
      backgroundColor: '#00000099', padding: { x: 12, y: 5 },
    }).setOrigin(0.5);
    this.actionContainer.add(this.actionCountdownText);
  }

  /** 创建动作按钮（碰/杠/吃/胡）- 腾讯风格 */
  private createActionBtn(
    text: string, color1: number, color2: number,
    w: number, h: number, x: number, y: number,
    onClick: () => void
  ): Phaser.GameObjects.Container {
    const container = this.add.container(x, y);
    
    const g = this.add.graphics();
    // 阴影
    g.fillStyle(0x000000, 0.5);
    g.fillRoundedRect(-w / 2 + 4, -h / 2 + 6, w, h, 14);
    // 主色
    g.fillStyle(color1, 1);
    g.fillRoundedRect(-w / 2, -h / 2, w, h, 14);
    // 上半渐变
    g.fillStyle(color2, 0.6);
    g.fillRoundedRect(-w / 2, -h / 2, w, h * 0.45, 14);
    // 外边框
    g.lineStyle(3, 0xffffff, 1);
    g.strokeRoundedRect(-w / 2, -h / 2, w, h, 14);
    // 内高光
    g.lineStyle(1, 0xffffff, 0.4);
    g.strokeRoundedRect(-w / 2 + 4, -h / 2 + 4, w - 8, h / 2 - 4, 10);
    
    const interactive = this.add.rectangle(0, 0, w, h, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    
    const fontSize = text === '胡' ? '30px' : '26px';
    const btnText = this.add.text(0, 0, text, { 
      fontFamily: 'Arial', fontSize, fontStyle: 'bold', color: '#ffffff',
      stroke: '#000000', strokeThickness: 2,
    }).setOrigin(0.5);
    
    container.add([g, interactive, btnText]);
    
    // 弹出动画初始状态
    container.setScale(0);
    container.setAlpha(0);
    
    interactive.on('pointerover', () => {
      this.tweens.add({ targets: container, scale: 1.1, duration: 120 });
    });
    interactive.on('pointerout', () => {
      this.tweens.add({ targets: container, scale: 1, duration: 120 });
    });
    interactive.on('pointerdown', () => {
      this.tweens.add({ targets: container, scale: 0.9, duration: 80 });
      // 立即执行动作：不再延迟 80ms，避免期间状态被服务端推送或场景级点击重置
      onClick();
    });
    
    return container;
  }

  /** 构建退出按钮 - 渐变光泽样式 */
  private buildExitButton() {
    const exitContainer = this.add.container(CANVAS_W - 80, CANVAS_H - 30).setDepth(60);
    
    const g = this.add.graphics();
    // 阴影
    g.fillStyle(0x000000, 0.4);
    g.fillRoundedRect(-48 + 3, -18 + 5, 96, 36, 10);
    // 红色主色
    g.fillStyle(0xc62828, 1);
    g.fillRoundedRect(-48, -18, 96, 36, 10);
    // 上层光泽
    g.fillStyle(0xef5350, 0.5);
    g.fillRoundedRect(-48, -18, 96, 18, 10);
    // 边框
    g.lineStyle(2, 0xffffff, 0.8);
    g.strokeRoundedRect(-48, -18, 96, 36, 10);
    // 顶部高光
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
      mjState.reset();
      setFlowState(FlowState.LOBBY);
      this.scene.start('Lobby');
    });
  }

  /** 构建托管按钮 - 渐变光泽样式（与退出按钮统一风格） */
  private buildAfkToggle() {
    const afkContainer = this.add.container(CANVAS_W - 80, CANVAS_H - 76).setDepth(60);
    
    const bg = this.add.graphics();
    this.drawAfkBtnNormal(bg);
    
    const interactive = this.add.rectangle(0, 0, 110, 36, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    
    const txt = this.add.text(0, 0, '💤 托管', { fontFamily: 'Arial', fontSize: '15px', color: '#ffffff', fontStyle: 'bold' }).setOrigin(0.5);
    
    afkContainer.add([bg, interactive, txt]);
    
    interactive.on('pointerover', () => this.tweens.add({ targets: afkContainer, scale: 1.05, duration: 100 }));
    interactive.on('pointerout', () => this.tweens.add({ targets: afkContainer, scale: 1, duration: 100 }));
    interactive.on('pointerdown', () => {
      if (store.gameEnded) return;
      const isAfk = this.isMyAfk();
      // 本地先行切换按钮态（乐观更新），确保页面文案立即变为"取消托管"，
      // 不必等待服务端 MsgMjAfkChanged 网络往返；服务端回执幂等，再同步一次。
      this.setMyAfk(!isAfk);
      this.updateAfkButton();
      net.send(MsgTypes.MsgMjAfk, { afk: !isAfk } satisfies MjAfkPayload);
    });
    
    (this as any).afkBtnBg = bg;
    (this as any).afkBtnTxt = txt;
    (this as any).afkBtnContainer = afkContainer;
  }

  /** 绘制托管按钮普通状态（蓝色渐变光泽） */
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

  /** 绘制托管按钮激活状态（橙色渐变光泽） */
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

  /** 我的挂机状态（以 mjState 玩家数据为状态源，与 MsgMjAfkChanged 同步路径一致） */
  private isMyAfk(): boolean {
    return !!mjState.players[mjState.myIndex]?.afk;
  }

  private setMyAfk(afk: boolean) {
    const my = mjState.players[mjState.myIndex];
    if (my) my.afk = afk;
  }

  /** 更新托管按钮显示 */
  private updateAfkButton() {
    const isAfk = this.isMyAfk();
    const txt = (this as any).afkBtnTxt as Phaser.GameObjects.Text;
    const bg = (this as any).afkBtnBg as Phaser.GameObjects.Graphics;
    const container = (this as any).afkBtnContainer as Phaser.GameObjects.Container;
    
    if (txt && bg) {
      txt.setText(isAfk ? '取消托管' : '💤 托管');
      if (isAfk) {
        this.drawAfkBtnActive(bg);
      } else {
        this.drawAfkBtnNormal(bg);
      }
      
      // 激活时有轻微脉冲效果
      if (container) {
        this.tweens.killTweensOf(container);
        if (isAfk) {
          this.tweens.add({ targets: container, scale: 1.04, yoyo: true, repeat: -1, duration: 600, ease: 'Sine.InOut' });
        } else {
          container.setScale(1);
        }
      }
    }
  }

  private showActions(canPong: boolean, canKong: boolean, canWin: boolean, withPass = true) {
    const hasAction = canPong || canKong || canWin;

    // 先终止按钮上残留的显示/隐藏动画，避免旧的隐藏动画 onComplete 把新按钮设为不可见
    // （杠按钮偶发快速消失的根因之一）
    for (const btn of [this.winBtn, this.kongBtn, this.pongBtn, this.passBtn]) {
      this.tweens.killTweensOf(btn);
    }

    this.pongBtn.setVisible(canPong);
    this.kongBtn.setVisible(canKong);
    this.winBtn.setVisible(canWin);
    // 忽略/不胡：碰/杠/胡任一询问时均可主动放弃（本地自杠提示除外）
    const showPass = withPass && !this.selfKongShown && (canPong || canKong || canWin);
    this.passBtn.setVisible(showPass);
    if (showPass) {
      this.passBtnLabel?.setText(canWin ? '不胡' : '忽略');
      // 可胡时紧挨胡按钮；仅碰/杠时放在另一侧空槽位
      this.passBtn.setPosition(canWin ? -115 * 0.5 : 115 * 1.5, 0);
    }

    this.actionContainer.setVisible(hasAction);

    if (hasAction) {
      // 弹出动画：从 scale 0 弹到 1
      const visibleBtns: Phaser.GameObjects.Container[] = [];
      if (canWin) visibleBtns.push(this.winBtn);
      if (showPass) visibleBtns.push(this.passBtn);
      if (canKong) visibleBtns.push(this.kongBtn);
      if (canPong) visibleBtns.push(this.pongBtn);

      visibleBtns.forEach((btn, i) => {
        btn.setScale(0).setAlpha(0);
        this.tweens.add({
          targets: btn,
          scale: 1,
          alpha: 1,
          duration: 200,
          delay: i * 60,
          ease: 'Back.Out',
        });
      });
    }
  }

  private hideActions() {
    mjState.actionAvail = false;
    mjState.canPong = false;
    mjState.canKong = false;
    mjState.canWin = false;
    this.selfKongShown = false;

    // 收起动画（先终止残留动画，避免与显示动画互相覆盖）
    const btns = [this.winBtn, this.kongBtn, this.pongBtn, this.passBtn];
    btns.forEach(btn => {
      this.tweens.killTweensOf(btn);
      if (btn.visible) {
        this.tweens.add({
          targets: btn,
          scale: 0,
          alpha: 0,
          duration: 150,
          onComplete: () => btn.setVisible(false),
        });
      }
    });

    this.stopActionCountdown();
  }

  /** 停止动作倒计时：隐藏文字而非 setText('')，
   *  空 Text 仍会按 padding 渲染 backgroundColor，产生残留的灰色小方块 */
  private stopActionCountdown() {
    this.actionTimer?.remove();
    this.actionTimer = undefined;
    this.actionSecondsLeft = 0;
    this.actionCountdownText?.setVisible(false);
  }

  /** 启动动作倒计时 */
  private startActionCountdown(seconds: number) {
    this.actionTimer?.remove();
    this.actionTimer = undefined;
    this.actionSecondsLeft = seconds > 0 ? seconds : 0;
    this.updateActionCountdownText();
    if (this.actionSecondsLeft <= 0) return;
    this.actionTimer = this.time.addEvent({
      delay: 1000,
      loop: true,
      callback: () => {
        this.actionSecondsLeft = Math.max(0, this.actionSecondsLeft - 1);
        this.updateActionCountdownText();
        if (this.actionSecondsLeft <= 0) {
          this.actionTimer?.remove();
          this.actionTimer = undefined;
        }
      },
    });
  }

  private updateActionCountdownText() {
    if (!this.actionCountdownText) return;
    if (this.actionSecondsLeft > 0) {
      this.actionCountdownText.setText(`⏳ ${this.actionSecondsLeft}s 后自动处理`);
      this.actionCountdownText.setColor(this.actionSecondsLeft <= 5 ? '#ff8a80' : '#ffcc80');
      this.actionCountdownText.setVisible(true);
    } else {
      this.actionCountdownText.setVisible(false);
    }
  }

  /** 出牌：发送选中的牌 */
  private discardSelected() {
    if (mjState.myIndex < 0 || !mjState.players[mjState.myIndex]) return;
    if (mjState.selectedTile < 0) return;
    
    const hand = mjState.players[mjState.myIndex].hand;
    const tile = hand[mjState.selectedTile];
    
    // 出牌飞出动画
    const { startX, y } = this.handLayout();
    const tileX = startX + mjState.selectedTile * (TILE_W + 4) + TILE_W / 2;
    const tileY = y + TILE_H / 2;
    
    // 创建飞出的牌面效果
    const flyTile = this.add.container(tileX, tileY).setDepth(48);
    const g = this.add.graphics();
    const depth = 6;
    g.fillStyle(0xc9ba90, 1);
    g.fillRoundedRect(-TILE_W / 2, -TILE_H / 2 + depth, TILE_W, TILE_H - depth, 5);
    g.fillStyle(0xfaf5e6, 1);
    g.fillRoundedRect(-TILE_W / 2, -TILE_H / 2, TILE_W, TILE_H - depth, 5);
    g.lineStyle(2, 0xb0a585, 1);
    g.strokeRoundedRect(-TILE_W / 2, -TILE_H / 2, TILE_W, TILE_H - depth, 5);
    
    const label = this.add.text(0, 0, tileName(tile), {
      fontFamily: 'Arial', fontSize: '18px',
      color: '#' + tileColor(tile).toString(16).padStart(6, '0'),
    }).setOrigin(0.5);
    
    flyTile.add([g, label]);
    
    // 飞到弃牌区中央
    this.tweens.add({
      targets: flyTile,
      x: CANVAS_W / 2,
      y: 260,
      scale: 0.7,
      alpha: 0.6,
      duration: 300,
      ease: 'Cubic.In',
      onComplete: () => {
        flyTile.destroy();
        // 飞牌动画结束即发送，不等语音：画面与出牌进度即时推进，语音只保最新
        net.send(MsgTypes.MsgMjDiscard, { tile } satisfies MjDiscardPayload);
      },
    });
    
    mjState.selectedTile = -1;
    this.newTileIndex = -1;
    this.discardBtn.setVisible(false);
    this.hideSelfKong();
  }

  /** 提示：高亮建议打出的牌 */
  private showHint() {
    if (mjState.myIndex < 0 || !mjState.players[mjState.myIndex]) return;
    const hand = mjState.players[mjState.myIndex].hand;
    if (hand.length === 0) return;

    this.newTileIndex = -1;
    mjState.selectedTile = this.suggestDiscardIndex(hand);
    this.discardBtn.setVisible(true);
    this.render();

    // 提示闪光效果（对准上移选中后的牌面中心）
    const { startX, y } = this.handLayout();
    const hintX = startX + mjState.selectedTile * (TILE_W + 3) + TILE_W / 2;
    const hintY = (y - 16) + TILE_H / 2;
    
    const glow = this.add.rectangle(hintX, hintY, TILE_W + 8, TILE_H + 4, 0xffeb3b, 0.5)
      .setStrokeStyle(3, 0xffd700, 1)
      .setDepth(35);
    
    this.tweens.add({
      targets: glow,
      alpha: 0,
      scaleX: 1.5,
      scaleY: 1.3,
      duration: 600,
      ease: 'Cubic.Out',
      onComplete: () => glow.destroy(),
    });
  }

  /** 建议出牌索引 */
  private suggestDiscardIndex(hand: number[]): number {
    const count = new Map<number, number>();
    for (const t of hand) count.set(t, (count.get(t) ?? 0) + 1);
    
    const hasNeighbor = (t: number): boolean => {
      if (t >= MJ_HONOR_START) return false;
      const v = t % 9;
      if (v > 0 && hand.includes(t - 1)) return true;
      if (v < 8 && hand.includes(t + 1)) return true;
      return false;
    };
    const isIsolated = (t: number): boolean => (count.get(t) ?? 0) === 1 && !hasNeighbor(t);
    
    let idx = hand.findIndex(t => t >= MJ_HONOR_START && isIsolated(t));
    if (idx >= 0) return idx;
    idx = hand.findIndex(t => t < MJ_HONOR_START && (t % 9 === 0 || t % 9 === 8) && isIsolated(t));
    if (idx >= 0) return idx;
    idx = hand.findIndex(t => isIsolated(t));
    if (idx >= 0) return idx;
    return hand.length - 1;
  }

  /** 智能整理手牌并按花色和点数排序，同时追踪新摸到的牌位置 */
  private sortHandAndTrackNew(newTile: number) {
    if (mjState.myIndex < 0 || !mjState.players[mjState.myIndex]) return;

    const hand = mjState.players[mjState.myIndex].hand;
    if (hand.length <= 1) {
      this.newTileIndex = hand.length - 1;
      this.render();
      return;
    }

    hand.sort((a, b) => {
      const suitA = Math.floor(a / 9);
      const suitB = Math.floor(b / 9);
      if (suitA !== suitB) return suitA - suitB;
      const valA = a % 9;
      const valB = b % 9;
      return valA - valB;
    });

    // 找到新牌排序后的位置（取最后一个匹配的，因为可能有重复牌）
    this.newTileIndex = -1;
    for (let i = hand.length - 1; i >= 0; i--) {
      if (hand[i] === newTile) {
        this.newTileIndex = i;
        break;
      }
    }

    mjState.selectedTile = -1;
    this.discardBtn?.setVisible(false);
    this.render();
  }

  // --- 消息订阅 ---

  private subscribe() {
    this.unsubscribers.push(
      net.on(MsgTypes.MsgGameStart, (p: GameStartPayload) => {
        if (store.gameStartEpoch !== this.enterEpoch) return;
        this.onGameStart(p);
      }),
      net.on(MsgTypes.MsgMjDice, (p: MjDicePayload) => {
        if (store.gameStartEpoch !== this.enterEpoch) return;
        this.onDiceRolled(p);
      }),
      net.on(MsgTypes.MsgGameState, (p: GameStatePayload) => {
        if (store.gameStartEpoch !== this.enterEpoch) return;
        if (p.available && p.state) {
          this.onGameState(p.state);
        }
      }),
      net.on(MsgTypes.MsgMjTurn, (p: MjTurnPayload) => {
        mjState.currentTurn = p.player_id;
        mjState.meldNumber = p.meld_number;
        // 服务端仅在玩家摸完牌后才广播 turn：他人 turn 时其手牌数 +1，
        // 保证对手手牌背图形数量与真实手牌一致（修复只减不增导致归零的问题）
        if (p.player_id !== store.playerID) {
          const pl = mjState.players.find(x => x.playerId === p.player_id);
          if (pl && pl.hand.length === 0) pl.handCount++;
        }
        // 换回合时清除过期的动作按钮
        this.hideActions();
        // 只有轮到自己时才启动倒计时（bot 会快速自动出牌，不需要倒计时）
        if (p.player_id === store.playerID) {
          this.startTurnCountdown(p.timeout);
        } else {
          // 停止之前的倒计时
          this.timerEvent?.remove();
          this.timerEvent = undefined;
          this.turnSecondsLeft = 0;
        }
        this.render();
      }),
      net.on(MsgTypes.MsgMjDraw, (p: MjDrawPayload) => {
        if (mjState.myIndex >= 0 && mjState.players[mjState.myIndex]) {
          mjState.players[mjState.myIndex].hand.push(p.tile);
          mjState.wallRemain = p.wall_remain;
          mjState.selfDrew = true;

          // 摸牌飞入动画
          this.animateDrawTile(p.tile);

          // 摸牌后自动整理牌面，并记录新牌位置用于高亮
          this.sortHandAndTrackNew(p.tile);
          this.speakText(`摸${tileSpeech(p.tile)}`, 'draw-self');
        }
      }),
      net.on(MsgTypes.MsgMjDiscarded, (p: MjDiscardedPayload) => {
        // 画面即时应用 + 播报与牌同步发出：语音不再拖住下一拍，跑得慢了则丢旧保新
        mjState.selfDrew = false;
        // 别人出牌了，之前的动作按钮肯定过期了
        this.hideActions();
        const idx = mjState.players.findIndex(pl => pl.playerId === p.player_id);
        if (idx >= 0) {
          const hand = mjState.players[idx].hand;
          const ti = hand.indexOf(p.tile);
          if (ti >= 0) hand.splice(ti, 1);
          else if (mjState.players[idx].handCount > 0) mjState.players[idx].handCount--;

          if (!mjState.discardPools[idx]) mjState.discardPools[idx] = [];
          mjState.discardPools[idx].push(p.tile);
          this.lastDiscarderIdx = idx;

          if (p.player_id === store.playerID) {
            this.speakText(`打${tileSpeech(p.tile)}`, `discard-${p.player_id}`);
          } else {
            const rel = this.seatRelation(idx);
            this.speakText(rel ? `${rel}打${tileSpeech(p.tile)}` : `打${tileSpeech(p.tile)}`, `discard-${p.player_id}`);
          }

          this.render();
        }
      }),
      net.on(MsgTypes.MsgMjPongMade, (p: MjPongMadePayload) => {
        const idx = mjState.players.findIndex(pl => pl.playerId === p.player_id);
        if (idx >= 0) {
          const pl = mjState.players[idx];
          mjState.players[idx].melds.push({ type: 0, tile: p.tile, from: 0 });
          // 碰：从手牌取走 2 张，单独放面子区，不再留在手牌
          this.removeTilesFromHand(mjState.players[idx].hand, p.tile, 2);
          // 他人碰时同步手牌背数量（自己以 hand 数组为准）
          if (pl.hand.length === 0 && pl.handCount >= 2) pl.handCount -= 2;
          this.claimDiscardedTile(p.from, p.tile);
          this.showActionStamp(p.player_id, '碰!', '#ffcc00');

          if (p.player_id === store.playerID) {
            this.speakText('碰', `pong-${p.player_id}`);
          } else {
            const rel = this.seatRelation(idx);
            this.speakText(rel ? `${rel}碰` : '碰', `pong-${p.player_id}`);
          }

          this.render();
        }
      }),
      net.on(MsgTypes.MsgMjKongMade, (p: MjKongMadePayload) => {
        const idx = mjState.players.findIndex(pl => pl.playerId === p.player_id);
        if (idx >= 0) {
          const pl = mjState.players[idx];
          let kongCost = 0;
          if (p.is_add_kong) {
            // 补杠：将已碰面子升级为杠，并从手牌移除补入的 1 张
            const meld = mjState.players[idx].melds.find(m => m.type === 0 && m.tile === p.tile);
            if (meld) meld.type = 1;
            this.removeTilesFromHand(mjState.players[idx].hand, p.tile, 1);
            kongCost = 1;
          } else {
            const meldType = p.is_ankong ? 3 : 1;
            mjState.players[idx].melds.push({ type: meldType, tile: p.tile, from: 0 });
            // 杠：从手牌取走对应张数（明杠 3 / 暗杠 4），单独放面子区，不再留在手牌
            const n = p.is_ankong ? 4 : 3;
            this.removeTilesFromHand(mjState.players[idx].hand, p.tile, n);
            kongCost = n;
          }
          // 他人杠时同步手牌背数量（杠后摸牌由 turn 事件 +1 补回）
          if (pl.hand.length === 0 && pl.handCount >= kongCost) pl.handCount -= kongCost;
          if (p.from) this.claimDiscardedTile(p.from, p.tile);
          this.showActionStamp(p.player_id, '杠!', '#ff66cc');

          if (p.player_id === store.playerID) {
            this.speakText('杠', `kong-${p.player_id}`);
          } else {
            const rel = this.seatRelation(idx);
            this.speakText(rel ? `${rel}杠` : '杠', `kong-${p.player_id}`);
          }

          this.render();
        }
      }),
      net.on(MsgTypes.MsgMjActionAvail, (p: MjActionAvailPayload) => {
        // 安全检查：确保有动作可选
        if (mjState.myIndex < 0) return;
        if (!p.can_pong && !p.can_kong && !p.can_win) return;

        // 碰/杠/胡询问有服务端超时：画面本就即时应用，无需额外追平
        this.selfKongShown = false; // 服务端询问优先于本地自杠提示
        mjState.actionAvail = true;
        mjState.canPong = p.can_pong;
        mjState.canKong = p.can_kong;
        mjState.canWin = p.can_win;
        mjState.actionTile = p.tile;
        this.showActions(p.can_pong, p.can_kong, p.can_win);
        this.startActionCountdown(p.timeout);
      }),
      net.on(MsgTypes.MsgMjAfkChanged, (p: MjAfkChangedPayload) => {
        const pl = mjState.players.find(pl => pl.playerId === p.player_id);
        if (pl) pl.afk = p.afk;

        if (p.player_id === store.playerID) {
          this.updateAfkButton();
        }

        this.render();
      }),
      net.on(MsgTypes.MsgGameOver, (p: GameOverPayload) => {
        // 对局结束：丢弃未播完的旧语音（含打断当前播报），结算画面与胡牌播报同时到位
        this.clearSpeechQueue();
        this.gameEndedNormally = true;
        mjState.currentTurn = '';
        this.turnSecondsLeft = 0;
        this.timerEvent?.remove();
        this.timerEvent = undefined;
        this.stopActionCountdown();

        // 累计本轮积分（会话内跨局，头像下方展示）
        for (const s of p.scores || []) {
          store.sessionScores.set(s.player_id, (store.sessionScores.get(s.player_id) ?? 0) + s.score);
        }
        
        // 胡! 印章 + 特效
        if (p.winner_id) {
          this.showActionStamp(p.winner_id, '🎉 胡!', '#ff1744');
          
          const myIdx = mjState.myIndex;
          const isWin = myIdx >= 0 && mjState.players[myIdx]?.playerId === p.winner_id;
          if (isWin) this.showWinEffect();
        }
        
        const myIdx = mjState.myIndex;
        const isWin = p.winner_id && myIdx >= 0 && mjState.players[myIdx]?.playerId === p.winner_id;
        if (isWin) {
          this.speakText('胡了！你赢了！', 'win-self');
        } else if (p.winner_id) {
          const winnerIdx = mjState.players.findIndex(pl => pl.playerId === p.winner_id);
          const rel = winnerIdx >= 0 ? this.seatRelation(winnerIdx) : '';
          this.speakText(rel ? `${rel}胡了` : '对方胡了', `win-${p.winner_id}`);
        } else {
          this.speakText('流局', 'draw-game');
        }
        
        this.showGameOver(p);
      }),
      net.on(MsgTypes.MsgStatsResult, (p: StatsResultPayload) => {
        // 结算落库后服务端推送权威统计：人机房积分经机器人重分配，需对齐累计积分/排名显示
        //（此前麻将场景从不更新 store.score，对局中顶部积分一直停留在进局时值）
        if (p.player_id !== store.playerID) return;
        store.score = p.score;
        store.rank = p.rank;
        this.scoreText.setText(`🎯 ${store.score}  🏆 ${store.rank || '—'}`);
      }),
      net.on(MsgTypes.MsgGameSyncResult, (p: GameSyncResultPayload) => {
        if (this.gameEndedNormally) return;
        if (p.status === 'active') return;
        console.warn(`[MahjongScene] sync failed: status=${p.status}, going to lobby`);
        this.goToLobby();
      }),
      net.on(MsgTypes.MsgError, (p: ErrorPayload) => {
        console.warn('[Mahjong] 错误:', p.code, p.message);
      }),
      net.on(MsgTypes.MsgMaintenancePush, (p: MaintenancePayload) => {
        store.maintenance = p.maintenance;
        if (p.maintenance) this.showMaintenanceOverlay();
        else this.hideMaintenanceOverlay();
      }),
    );
  }

  /** 摸牌飞入动画 */
  private animateDrawTile(tile: number) {
    const { startX, y } = this.handLayout();
    const handCount = mjState.myIndex >= 0 && mjState.players[mjState.myIndex]
      ? mjState.players[mjState.myIndex].hand.length : 0;
    // 新牌会被 sortHand 排序，所以动画后位置可能变，但这里先做一个简单的飞入效果
    const targetX = startX + Math.max(0, handCount - 1) * (TILE_W + 4) + TILE_W / 2;
    const targetY = y;
    
    // 从牌墙位置飞入
    const flyX = CANVAS_W / 2;
    const flyY = 85;
    
    const flyTile = this.add.container(flyX, flyY).setDepth(48);
    const g = this.add.graphics();
    const depth = 6;
    g.fillStyle(0xc9ba90, 1);
    g.fillRoundedRect(-TILE_W / 2, -TILE_H / 2 + depth, TILE_W, TILE_H - depth, 5);
    g.fillStyle(0xfaf5e6, 1);
    g.fillRoundedRect(-TILE_W / 2, -TILE_H / 2, TILE_W, TILE_H - depth, 5);
    g.lineStyle(2, 0xb0a585, 1);
    g.strokeRoundedRect(-TILE_W / 2, -TILE_H / 2, TILE_W, TILE_H - depth, 5);
    
    const label = this.add.text(0, 0, tileName(tile), {
      fontFamily: 'Arial', fontSize: '18px',
      color: '#' + tileColor(tile).toString(16).padStart(6, '0'),
    }).setOrigin(0.5);
    
    flyTile.add([g, label]);
    flyTile.setScale(0.6);
    
    this.tweens.add({
      targets: flyTile,
      x: targetX,
      y: targetY,
      scale: 1,
      duration: 350,
      ease: 'Back.Out',
      onComplete: () => {
        flyTile.destroy();
        this.render();
      },
    });
  }

  private onGameStart(_p: GameStartPayload) {
    this.stateReceived = true;
    this.lastDiscarderIdx = -1;
    // 新一局开始：清掉上一局可能残留的播报，防止跨局串扰
    this.clearSpeechQueue();
  }

  /** 开局掷骰：展示各家点数并突出庄家（先手） */
  private onDiceRolled(p: MjDicePayload) {
    const dealerName = mjState.players[p.dealer]?.playerName || `玩家${p.dealer + 1}`;
    const valueText = p.values.map((v, i) =>
      `${mjState.players[i]?.playerName || `玩家${i + 1}`} ${v}`
    ).join('　');
    const title = `${valueText}\n🎲 ${dealerName} 掷出最大点，庄家先手！`;

    const bg = this.add.rectangle(CANVAS_W / 2, 140, 560, 84, 0x000000, 0.72)
      .setStrokeStyle(2, 0xffd700).setDepth(90);
    const label = this.add.text(CANVAS_W / 2, 140, title, {
      fontFamily: 'Arial', fontSize: '18px', color: '#ffe082',
      align: 'center', lineSpacing: 6,
    }).setOrigin(0.5).setDepth(91);
    this.tweens.add({
      targets: [bg, label],
      alpha: 0,
      delay: 2600,
      duration: 500,
      onComplete: () => { bg.destroy(); label.destroy(); },
    });
  }

  private onGameState(stateRaw: unknown) {
    this.stateReceived = true;
    this.lastDiscarderIdx = -1;
    // 全量状态同步（重连/恢复局）：画面直接对齐服务端最新牌局，未播的旧语音已无意义
    this.clearSpeechQueue();
    try {
      const state = stateRaw as Record<string, unknown>;
      if (state && Array.isArray(state.hand)) {
        if (mjState.myIndex >= 0 && mjState.players[mjState.myIndex]) {
          mjState.players[mjState.myIndex].hand = state.hand as number[];
          mjState.players[mjState.myIndex].melds = ((state.melds as Array<{ type: number; tile: number; from: number }>) || []).map(m => ({ type: m.type, tile: m.tile, from: m.from }));
          mjState.wallRemain = (state.wall_remain as number) || 0;
          this.render();
        }
      } else if (state && Array.isArray(state.players)) {
        const dto = state as unknown as import('../../platform/protocol').MjGameStateDTO;
        mjState.restore(dto);
        this.render();
      }
    } catch (e) {
      console.error('[Mahjong] 状态解析失败:', e);
    }
  }

  private goToLobby() {
    this.gameEndedNormally = true;
    store.reset();
    mjState.reset();
    setFlowState(FlowState.LOBBY);
    this.scene.start('Lobby');
  }

  // --- 渲染 ---

  private render() {
    for (const t of this.dynTexts) t.destroy();
    this.dynTexts = [];
    this.handGfx.clear();
    this.discardGfx.clear();
    this.meldGfx.clear();
    this.oppGfx.clear();

    if (mjState.players.length === 0) {
      for (let r = 0; r < 4; r++) {
        this.avatars[r]?.setVisible(false);
        this.avatarLetters[r]?.setVisible(false);
        this.seatPanels[r]?.setVisible(false);
        this.turnRings[r]?.setVisible(false);
      }
      this.renderTurnText();
      return;
    }

    const myIdx = mjState.myIndex >= 0 ? mjState.myIndex : 0;
    const n = mjState.players.length;

    for (let r = n; r < 4; r++) {
      this.avatars[r]?.setVisible(false);
      this.avatarLetters[r]?.setVisible(false);
      this.seatPanels[r]?.setVisible(false);
      this.turnRings[r]?.setVisible(false);
    }

    this.scoreText.setText(`🎯 ${store.score}  🏆 ${store.rank || '—'}`);
    this.wallText.setText(`🀄 剩余 ${mjState.wallRemain}`);

    this.renderTurnText();
    this.updateAfkButton();

    const myPlayer = mjState.players[myIdx];

    if (myPlayer) {
      this.renderMyHand(myPlayer.hand);
    }

    // 统一渲染所有面子牌（集中在弃牌区周围）
    this.renderAllMelds(myIdx, n);

    for (let i = 0; i < n; i++) {
      const relPos = (i - myIdx + n) % n;
      const pl = mjState.players[i];
      this.renderSeatPanel(relPos, pl);
      if (i === myIdx) {
        if (mjState.discardPools[i]) {
          this.renderDiscardPool(mjState.discardPools[i], 0, i === this.lastDiscarderIdx);
        }
        continue;
      }
      this.renderOpponent(pl, relPos);
      if (mjState.discardPools[i]) {
        this.renderDiscardPool(mjState.discardPools[i], relPos, i === this.lastDiscarderIdx);
      }
    }
  }

  /** 手牌布局 */
  private handLayout(): { startX: number; y: number } {
    const hand = mjState.myIndex >= 0 && mjState.players[mjState.myIndex]
      ? mjState.players[mjState.myIndex].hand : [];
    const totalW = hand.length * (TILE_W + 3);
    return { startX: (CANVAS_W - totalW) / 2, y: CANVAS_H - 80 };
  }

  private renderMyHand(hand: number[]) {
    const g = this.handGfx;
    const { startX, y } = this.handLayout();

    for (let i = 0; i < hand.length; i++) {
      const x = startX + i * (TILE_W + 3);
      const t = hand[i];
      const selected = mjState.selectedTile === i;
      const isNew = this.newTileIndex === i && !selected;
      const ty = selected ? y - 16 : (isNew ? y - 8 : y);
      this.drawTile(g, x, ty, t, TILE_W, TILE_H, selected, isNew);
    }
  }

  /** 统一渲染所有面子牌 - 各家碰/杠牌位于其手牌前方的独立区域，与中央弃牌河区分离 */
  private renderAllMelds(myIdx: number, n: number) {
    const g = this.meldGfx;

    for (let i = 0; i < n; i++) {
      const relPos = (i - myIdx + n) % n;
      const pl = mjState.players[i];
      if (!pl || pl.melds.length === 0) continue;

      let regionX: number, regionY: number, regionW: number, regionH: number;
      let labelYOffset = 0;

      // 碰/杠面子区：位于各家手牌前方，与中央河区（弃牌池）分离，
      // 保证面子牌完整显示不被弃牌/按钮遮挡
      // relPos 0=自己, 1=右家, 2=对家, 3=左家
      if (relPos === 0) {
        // 自己：中央河区下方、自己手牌上方
        regionW = 440;
        regionH = 46;
        regionX = 420;
        regionY = 498;
        labelYOffset = -12;
      } else if (relPos === 2) {
        // 对家：手牌下方、中央河区上方
        regionW = 440;
        regionH = 46;
        regionX = 420;
        regionY = 96;
        labelYOffset = -12;
      } else if (relPos === 3) {
        // 左家：手牌右侧、中央河区左侧（河区收窄后加宽，容纳更多碰杠组）
        regionW = 180;
        regionH = 330;
        regionX = 54;
        regionY = 152;
        labelYOffset = -12;
      } else {
        // 右家：手牌左侧、中央河区右侧（与左家对称）
        regionW = 180;
        regionH = 330;
        regionX = CANVAS_W - 234;
        regionY = 152;
        labelYOffset = -12;
      }
      
      // 绘制区域背景框
      g.fillStyle(0x000000, 0.25);
      g.fillRoundedRect(regionX, regionY, regionW, regionH, 8);
      g.lineStyle(1.5, 0xffd700, 0.45);
      g.strokeRoundedRect(regionX, regionY, regionW, regionH, 8);
      
      // 区域标签（玩家名缩写）
      const label = this.add.text(regionX + regionW / 2, regionY + labelYOffset, 
        pl.playerName.charAt(0) + (relPos === 0 ? '(我)' : ''), 
        { fontFamily: 'Arial', fontSize: '10px', color: '#ffd700', fontStyle: 'bold' }
      ).setOrigin(0.5);
      this.dynTexts.push(label);
      
      // 在区域内渲染面子牌
      this.renderMeldsInRegion(g, pl.melds, regionX, regionY, regionW, regionH, relPos);
    }
  }

  /** 在指定区域内渲染一组面子牌 */
  private renderMeldsInRegion(
    g: Phaser.GameObjects.Graphics,
    melds: Array<{ type: number; tile: number; from: number }>,
    rx: number, ry: number, rw: number, rh: number,
    relPos: number,
  ) {
    const isVertical = (relPos === 1 || relPos === 3);
    const tileW = SMALL_TILE_W - 2;
    const tileH = SMALL_TILE_H - 4;
    const gap = 1;
    const groupGap = 5;
    
    // 计算总尺寸（推倒胡规则：无吃牌，仅碰/杠）
    const groups: Array<{ tiles: number[]; type: number }> = [];
    for (const meld of melds) {
      const group: number[] = [];
      const count = (meld.type === 1 || meld.type === 3) ? 4 : 3;
      for (let j = 0; j < count; j++) group.push(meld.tile);
      groups.push({ tiles: group, type: meld.type });
    }
    
    if (isVertical) {
      // 列布局：每组占一列（组内纵向排列），列间横向推进——
      // 加宽后的左右家区域可并排容纳多组碰/杠
      let curX = rx + 4;
      for (const grp of groups) {
        const colH = grp.tiles.length * (tileH + gap) - gap;
        if (curX + tileW > rx + rw - 2) break; // 列宽不够则停止，避免溢出
        let curY = ry + Math.max(2, (rh - colH) / 2);
        for (const tile of grp.tiles) {
          this.drawTile(g, curX, curY, tile, tileW, tileH, false);
          curY += tileH + gap;
        }
        curX += tileW + groupGap;
      }
    } else {
      // 水平布局：沿X轴排列各组
      // 先计算总宽度，居中
      let totalW = 0;
      for (const grp of groups) {
        totalW += grp.tiles.length * (tileW + gap) - gap + groupGap;
      }
      let curX = rx + Math.max(2, (rw - totalW) / 2);
      const curY = ry + (rh - tileH) / 2;
      
      for (const grp of groups) {
        for (const tile of grp.tiles) {
          this.drawTile(g, curX, curY, tile, tileW, tileH, false);
          curX += tileW + gap;
        }
        curX += groupGap;
      }
    }
  }

  private renderOpponent(pl: { playerName: string; handCount: number; melds: Array<{ type: number; tile: number; from: number }>; isDealer: boolean; afk: boolean }, relPos: number) {
    const g = this.oppGfx;
    const step = 13;
    
    if (relPos === 2) {
      const w = pl.handCount * (SMALL_TILE_W + 2);
      const x0 = CANVAS_W / 2 - w / 2;
      for (let i = 0; i < pl.handCount; i++) {
        this.drawTileBack(g, x0 + i * (SMALL_TILE_W + 2), 8);
      }
    } else if (relPos === 1 || relPos === 3) {
      const x = relPos === 3 ? 14 : CANVAS_W - 14 - SMALL_TILE_W;
      const totalH = Math.max(0, pl.handCount - 1) * step + SMALL_TILE_H;
      const y0 = 260 - totalH / 2;
      for (let i = 0; i < pl.handCount; i++) {
        this.drawTileBack(g, x, y0 + i * step);
      }
    }
  }

  /** 牌背 - 经典蓝色格子纹 */
  private drawTileBack(g: Phaser.GameObjects.Graphics, x: number, y: number) {
    const depth = 3;
    // 底部厚度
    g.fillStyle(0x1a237e, 1);
    g.fillRoundedRect(x, y + depth, SMALL_TILE_W, SMALL_TILE_H - depth, 3);
    // 牌背主色（深蓝）
    g.fillStyle(0x283593, 1);
    g.fillRoundedRect(x, y, SMALL_TILE_W, SMALL_TILE_H - depth, 3);
    // 边框
    g.lineStyle(1, 0x5c6bc0, 1);
    g.strokeRoundedRect(x, y, SMALL_TILE_W, SMALL_TILE_H - depth, 3);
    // 中心装饰格子（简化版：一个小方块）
    const cx = x + SMALL_TILE_W / 2;
    const cy = y + SMALL_TILE_H / 2 - 1;
    g.fillStyle(0x3949ab, 1);
    g.fillRoundedRect(cx - 6, cy - 6, 12, 12, 2);
    g.lineStyle(0.5, 0x7986cb, 1);
    g.strokeRoundedRect(cx - 6, cy - 6, 12, 12, 2);
  }

  private renderDiscardPool(pool: number[], relPos: number, isLatestPool = false) {
    const g = this.discardGfx;

    // 所有玩家弃牌统一放入桌面正中央河区（250,152,780,330），
    // 以中心线（640,317）分四象限：对家左上、右家右上、自己右下、左家左下
    let regionX: number, regionY: number;
    switch (relPos) {
      case 0:
        regionX = 646; regionY = 323; // 自己：右下象限
        break;
      case 1:
        regionX = 646; regionY = 156; // 右家：右上象限
        break;
      case 2:
        regionX = 254; regionY = 156; // 对家：左上象限
        break;
      case 3:
      default:
        regionX = 254; regionY = 323; // 左家：左下象限
        break;
    }
    const cols = 12; // 象限宽约 386px，每行 12 张
    const tileStepX = SMALL_TILE_W + 2;
    const tileStepY = SMALL_TILE_H + 2;

    for (let i = 0; i < pool.length; i++) {
      const col = i % cols;
      const row = Math.floor(i / cols);
      const x = regionX + col * tileStepX;
      const y = regionY + row * tileStepY;
      const isLatest = isLatestPool && i === pool.length - 1;
      this.drawSmallTile(g, x, y, pool[i], isLatest);
    }
  }

  /** 3D 立体牌面 - 增强版（腾讯风格） */
  private drawTile(g: Phaser.GameObjects.Graphics, x: number, y: number, tile: number, w: number, h: number, selected: boolean, isNew = false) {
    const depth = Math.max(4, Math.round(h * 0.12));
    const faceH = h - depth;

    // 底部厚度（侧面，用深色）
    g.fillStyle(selected ? 0xb8960f : (isNew ? 0x7a8a6a : 0x9e8e6a), 1);
    g.fillRoundedRect(x, y + depth, w, faceH, 4);
    // 右侧厚度
    g.fillStyle(selected ? 0x9a7d0a : (isNew ? 0x5a6a4a : 0x7d6f52), 1);
    g.fillRoundedRect(x + w - 2, y + depth, 2, faceH, 1);

    // 牌面主体（象牙白）
    g.fillStyle(selected ? 0xfffde7 : (isNew ? 0xf0fff0 : 0xfaf5e6), 1);
    g.fillRoundedRect(x, y, w, faceH, 4);

    // 顶部高光条（模拟光泽）
    g.fillStyle(selected ? 0xffffff : 0xffffff, selected ? 0.4 : 0.25);
    g.fillRoundedRect(x + 2, y + 1, w - 4, Math.max(3, faceH * 0.12), 3);

    // 边框
    g.lineStyle(selected ? 2.5 : 1.5, selected ? 0xffc107 : (isNew ? 0x4fc3f7 : 0xb0a585), 1);
    g.strokeRoundedRect(x, y, w, faceH, 4);

    // 选中时额外金色光晕边框
    if (selected) {
      g.lineStyle(1.5, 0xffeb3b, 0.7);
      g.strokeRoundedRect(x - 2, y - 2, w + 4, h + 2, 5);
    }

    // 新牌蓝色高亮标记
    if (isNew) {
      g.lineStyle(2, 0x29b6f6, 0.8);
      g.strokeRoundedRect(x - 1, y - 1, w + 2, h, 5);
    }

    const name = tileName(tile);
    const color = tileColor(tile);
    const hex = '#' + color.toString(16).padStart(6, '0');
    const fontSize = w >= 40 ? '18px' : '15px';
    const label = this.add.text(x + w / 2, y + faceH / 2, name, { fontFamily: 'Arial', fontSize, color: hex })
      .setOrigin(0.5)
      .setName(`tile_${tile}`);
    this.dynTexts.push(label);
  }

  /** 小牌（弃牌池/对家面子） */
  private drawSmallTile(g: Phaser.GameObjects.Graphics, x: number, y: number, tile: number, highlight = false) {
    const depth = 3;
    const faceH = SMALL_TILE_H - depth;
    
    g.fillStyle(highlight ? 0xb8960f : 0xc9ba90, 1);
    g.fillRoundedRect(x, y + depth, SMALL_TILE_W, faceH, 2);
    g.fillStyle(highlight ? 0xfffde7 : 0xfaf5e6, 1);
    g.fillRoundedRect(x, y, SMALL_TILE_W, faceH, 2);
    // 小高光
    g.fillStyle(0xffffff, 0.2);
    g.fillRoundedRect(x + 1, y + 1, SMALL_TILE_W - 2, 3, 1);
    g.lineStyle(1, highlight ? 0xffc107 : 0xb0a585, 1);
    g.strokeRoundedRect(x, y, SMALL_TILE_W, faceH, 2);
    
    if (highlight) {
      g.lineStyle(2.5, 0xffc107, 1);
      g.strokeRoundedRect(x - 2, y - 2, SMALL_TILE_W + 4, SMALL_TILE_H + 2, 3);
      // 最新弃牌闪光效果
      g.lineStyle(1, 0xffffff, 0.6);
      g.strokeRoundedRect(x - 1, y - 1, SMALL_TILE_W + 2, SMALL_TILE_H, 3);
    }

    const name = tileName(tile);
    const color = tileColor(tile);
    const hex = '#' + color.toString(16).padStart(6, '0');
    const label = this.add.text(x + SMALL_TILE_W / 2, y + faceH / 2, name, { fontFamily: 'Arial', fontSize: '13px', color: hex })
      .setOrigin(0.5);
    this.dynTexts.push(label);
  }

  // --- 交互 ---

  private onClick(pointer: Phaser.Input.Pointer) {
    if (mjState.myIndex < 0 || !mjState.players[mjState.myIndex]) return;
    const myPlayer = mjState.players[mjState.myIndex];

    const hand = myPlayer.hand;
    const { startX, y } = this.handLayout();

    if (pointer.y >= y - 20 && pointer.y <= y + TILE_H + 6) {
      for (let i = 0; i < hand.length; i++) {
        const x = startX + i * (TILE_W + 3);
        if (pointer.x >= x && pointer.x <= x + TILE_W) {
          // 鼠标点击只能选中/取消选中，出牌仅通过"出牌"按钮（腾讯麻将交互）
          if (mjState.selectedTile === i) {
            mjState.selectedTile = -1;
            this.newTileIndex = -1;
            this.discardBtn.setVisible(false);
            this.hideSelfKong();
          } else {
            mjState.selectedTile = i;
            this.newTileIndex = -1;
            this.discardBtn.setVisible(true);
            // 轮到自己且该牌可自杠（暗杠/补杠）时，弹出杠按钮
            if (this.canSelfKongTile(hand[i])) {
              this.showSelfKong(hand[i]);
            } else {
              this.hideSelfKong();
            }
          }
          this.render();
          return;
        }
      }
    }

    mjState.selectedTile = -1;
    this.newTileIndex = -1;
    this.discardBtn.setVisible(false);
    this.hideSelfKong();
    this.render();
  }

  /** 该牌是否可自杠：4 张暗杠，或已碰该牌且手牌持有第 4 张可补杠（仅自己回合） */
  private canSelfKongTile(tile: number): boolean {
    if (mjState.myIndex < 0 || !mjState.players[mjState.myIndex]) return false;
    if (mjState.currentTurn !== store.playerID) return false;
    // 服务端询问（碰/杠/胡）期间不可自杠
    if (mjState.actionAvail && !this.selfKongShown) return false;
    const me = mjState.players[mjState.myIndex];
    const cnt = me.hand.filter(t => t === tile).length;
    if (cnt >= 4) return true; // 暗杠
    return cnt >= 1 && me.melds.some(m => m.type === 0 && m.tile === tile); // 补杠
  }

  /** 展示自杠按钮（复用杠按钮，弹出于动作区） */
  private showSelfKong(tile: number) {
    this.selfKongShown = true;
    mjState.actionAvail = true;
    mjState.canPong = false;
    mjState.canKong = true;
    mjState.canWin = false;
    mjState.actionTile = tile;
    // 本地自杠提示无“忽略”选项
    this.showActions(false, true, false, false);
  }

  /** 收起自杠按钮（不影响服务端真实询问） */
  private hideSelfKong() {
    if (this.selfKongShown) {
      this.hideActions();
    }
  }

  // --- 游戏结束 ---

  private showGameOver(payload: GameOverPayload) {
    this.hideActions();
    mjState.phase = 'ended';
    this.newTileIndex = -1;
    this.render();

    const myIdx = mjState.myIndex;
    const isWin = payload.winner_id && myIdx >= 0 && mjState.players[myIdx]?.playerId === payload.winner_id;

    // 解析 extra 中的手牌数据
    let playerHands: Array<{ player_id: string; player_name: string; hand: number[]; melds: Array<{ type: number; tile: number; from: number }> }> = [];
    if (payload.extra) {
      try {
        const extra = typeof payload.extra === 'string' ? JSON.parse(payload.extra) : payload.extra;
        if (extra && Array.isArray(extra.player_hands)) {
          playerHands = extra.player_hands;
        }
      } catch { /* ignore */ }
    }

    const container = this.add.container(CANVAS_W / 2, CANVAS_H / 2).setDepth(80);

    // 背景遮罩
    const mask = this.add.rectangle(0, 0, CANVAS_W, CANVAS_H, 0x000000, 0.6);
    container.add(mask);

    // 结算面板（加高以容纳手牌展示）
    const panelW = 560;
    const panelH = playerHands.length > 0 ? 580 : 380;
    const panel = this.add.graphics();
    panel.fillStyle(0x000000, 0.5);
    panel.fillRoundedRect(-panelW / 2 + 6, -panelH / 2 + 8, panelW, panelH, 20);
    panel.fillStyle(0x1a1a2e, 1);
    panel.fillRoundedRect(-panelW / 2, -panelH / 2, panelW, panelH, 20);
    panel.lineStyle(3, isWin ? 0xffd700 : 0xff6666, 1);
    panel.strokeRoundedRect(-panelW / 2, -panelH / 2, panelW, panelH, 20);
    panel.lineStyle(1, 0xffffff, 0.2);
    panel.strokeRoundedRect(-panelW / 2 + 4, -panelH / 2 + 4, panelW - 8, panelH - 8, 16);
    container.add(panel);

    const title = isWin ? '🎉 恭喜胡牌！' : (payload.winner_id ? '😔 很遗憾' : '流局');
    const titleColor = isWin ? '#ffd700' : (payload.winner_id ? '#ff6666' : '#ffffff');
    container.add(this.add.text(0, -panelH / 2 + 40, title, {
      fontFamily: 'Arial', fontSize: '28px', fontStyle: 'bold', color: titleColor,
      stroke: '#000000', strokeThickness: 3,
    }).setOrigin(0.5));

    // 得分列表
    let yPos = -panelH / 2 + 80;
    container.add(this.add.text(-panelW / 2 + 30, yPos, '玩家', {
      ...FONT, fontSize: '14px', color: '#aaaacc', fontStyle: 'bold',
    }));
    container.add(this.add.text(panelW / 2 - 30, yPos, '得分', {
      ...FONT, fontSize: '14px', color: '#aaaacc', fontStyle: 'bold',
    }).setOrigin(1, 0));
    yPos += 22;

    const line = this.add.graphics();
    line.lineStyle(1, 0xffffff, 0.2);
    line.lineBetween(-panelW / 2 + 20, yPos, panelW / 2 - 20, yPos);
    container.add(line);
    yPos += 12;

    payload.scores.forEach((s) => {
      const pl = mjState.players.find(p => p.playerId === s.player_id);
      const isWinner = s.player_id === payload.winner_id;
      const isMe = myIdx >= 0 && s.player_id === mjState.players[myIdx]?.playerId;

      const prefix = isWinner ? '👑 ' : '';
      const name = `${prefix}${pl?.playerName || '未知'}${isMe ? ' (我)' : ''}`;
      const scoreColor = s.score > 0 ? '#4caf50' : (s.score < 0 ? '#f44336' : '#ffffff');
      const scoreText = `${s.score > 0 ? '+' : ''}${s.score}`;

      container.add(this.add.text(-panelW / 2 + 30, yPos, name, {
        ...FONT, fontSize: '14px', color: isWinner ? '#ffd700' : '#ffffff',
      }));
      container.add(this.add.text(panelW / 2 - 30, yPos, scoreText, {
        ...FONT, fontSize: '16px', color: scoreColor, fontStyle: 'bold',
      }).setOrigin(1, 0));

      yPos += 24;
    });

    // 手牌展示区
    if (playerHands.length > 0) {
      yPos += 8;
      const sepLine = this.add.graphics();
      sepLine.lineStyle(1, 0xffd700, 0.3);
      sepLine.lineBetween(-panelW / 2 + 20, yPos, panelW / 2 - 20, yPos);
      container.add(sepLine);
      yPos += 10;

      const tileW = 24;
      const tileH = 32;
      const tileGap = 1;

      for (const ph of playerHands) {
        const isMeHand = myIdx >= 0 && ph.player_id === mjState.players[myIdx]?.playerId;
        const nameLabel = `${ph.player_name}${isMeHand ? '(我)' : ''}`;
        container.add(this.add.text(-panelW / 2 + 30, yPos, nameLabel, {
          ...FONT, fontSize: '12px', color: '#aaaacc',
        }));

        // 渲染手牌（服务端已排序，前端再保险排序一次：数牌从小到大、字牌在后）
        const handStartX = -panelW / 2 + 30;
        const handY = yPos + 16;
        const handGfx = this.add.graphics();
        container.add(handGfx);

        const sortedHand = ph.hand ? [...ph.hand].sort((a, b) => a - b) : [];
        if (sortedHand.length > 0) {
          for (let ti = 0; ti < sortedHand.length; ti++) {
            const tx = handStartX + ti * (tileW + tileGap);
            const t = sortedHand[ti];
            const depth = 3;
            const faceH = tileH - depth;
            handGfx.fillStyle(0xc9ba90, 1);
            handGfx.fillRoundedRect(tx, handY + depth, tileW, faceH, 2);
            handGfx.fillStyle(0xfaf5e6, 1);
            handGfx.fillRoundedRect(tx, handY, tileW, faceH, 2);
            handGfx.lineStyle(1, 0xb0a585, 1);
            handGfx.strokeRoundedRect(tx, handY, tileW, faceH, 2);

            const name = tileName(t);
            const color = tileColor(t);
            const hex = '#' + color.toString(16).padStart(6, '0');
            container.add(this.add.text(tx + tileW / 2, handY + faceH / 2, name, {
              fontFamily: 'Arial', fontSize: '11px', color: hex,
            }).setOrigin(0.5));
          }
        } else {
          container.add(this.add.text(handStartX, handY + 4, '已胡/无手牌', {
            ...FONT, fontSize: '11px', color: '#666666',
          }));
        }

        yPos += tileH + 28;
      }
    }

    // 按钮区域
    const btnY = panelH / 2 - 45;
    const btnW = 130;
    const btnH = 44;
    const btnGap = 15;
    
    // 复盘按钮
    const replayBtnX = -btnW / 2 - btnGap / 2;
    const replayBtnGfx = this.add.graphics();
    replayBtnGfx.fillStyle(0x1976d2, 1);
    replayBtnGfx.fillRoundedRect(replayBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    replayBtnGfx.lineStyle(2, 0xffffff, 0.8);
    replayBtnGfx.strokeRoundedRect(replayBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    container.add(replayBtnGfx);
    
    const replayBtnInteractive = this.add.rectangle(replayBtnX, btnY, btnW, btnH, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    container.add(replayBtnInteractive);
    
    const replayBtnText = this.add.text(replayBtnX, btnY, '📊 复盘', {
      ...FONT, fontSize: '16px', fontStyle: 'bold',
    }).setOrigin(0.5);
    container.add(replayBtnText);
    
    replayBtnInteractive.on('pointerover', () => {
      replayBtnGfx.clear();
      replayBtnGfx.fillStyle(0x2196f3, 1);
      replayBtnGfx.fillRoundedRect(replayBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
      replayBtnGfx.lineStyle(2, 0xffffff, 0.8);
      replayBtnGfx.strokeRoundedRect(replayBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    });
    replayBtnInteractive.on('pointerout', () => {
      replayBtnGfx.clear();
      replayBtnGfx.fillStyle(0x1976d2, 1);
      replayBtnGfx.fillRoundedRect(replayBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
      replayBtnGfx.lineStyle(2, 0xffffff, 0.8);
      replayBtnGfx.strokeRoundedRect(replayBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    });
    replayBtnInteractive.on('pointerdown', () => {
      this.showReplayDialog(payload);
    });
    
    // 返回房间按钮
    const returnBtnX = btnW / 2 + btnGap / 2;
    const btn = this.add.graphics();
    btn.fillStyle(isWin ? 0x2e7d32 : 0x1565c0, 1);
    btn.fillRoundedRect(returnBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    btn.lineStyle(2, 0xffffff, 0.8);
    btn.strokeRoundedRect(returnBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    container.add(btn);

    const btnInteractive = this.add.rectangle(returnBtnX, btnY, btnW, btnH, 0xffffff, 0)
      .setInteractive({ useHandCursor: true });
    container.add(btnInteractive);

    const btnText = this.add.text(returnBtnX, btnY, '返回房间', {
      ...FONT, fontSize: '16px', fontStyle: 'bold',
    }).setOrigin(0.5);
    container.add(btnText);
    
    btnInteractive.on('pointerover', () => {
      btn.clear();
      btn.fillStyle(isWin ? 0x388e3c : 0x1976d2, 1);
      btn.fillRoundedRect(returnBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
      btn.lineStyle(2, 0xffffff, 0.8);
      btn.strokeRoundedRect(returnBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    });
    btnInteractive.on('pointerout', () => {
      btn.clear();
      btn.fillStyle(isWin ? 0x2e7d32 : 0x1565c0, 1);
      btn.fillRoundedRect(returnBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
      btn.lineStyle(2, 0xffffff, 0.8);
      btn.strokeRoundedRect(returnBtnX - btnW / 2, btnY - btnH / 2, btnW, btnH, 12);
    });
    btnInteractive.on('pointerdown', () => {
      // 返回房间：重置游戏状态并进入房间场景
      store.resetGame();
      setFlowState(FlowState.ROOM);
      this.scene.start('Room');
    });
    
    // 弹入动画
    container.setScale(0.5).setAlpha(0);
    this.tweens.add({
      targets: container,
      scale: 1,
      alpha: 1,
      duration: 300,
      ease: 'Back.Out',
    });
  }
  
  /** 显示复盘报告对话框 */
  private async showReplayDialog(_payload: GameOverPayload) {
    const roomCode = store.roomCode || '';
    if (!roomCode) {
      this.toast('无法获取房间信息');
      return;
    }

    try {
      const reportText = await fetchLatestReplayText('mahjong', roomCode, store.playerID || undefined);
      const { showRuleModal } = await import('../../platform/ui/modal');
      showRuleModal(this, {
        title: '📊 麻将对局复盘',
        content: reportText,
        width: 800,
        height: 600,
      });
    } catch (err) {
      console.error('加载复盘报告失败:', err);
      this.toast(err instanceof Error && err.message === REPLAY_NOT_READY ? REPLAY_NOT_READY : '加载复盘报告失败，请稍后重试');
    }
  }

  // --- 维护 ---

  private showMaintenanceOverlay() {
    if (this.maintenanceOverlay) return;
    const c = this.add.container(0, 0);
    c.add(this.add.rectangle(CANVAS_W / 2, CANVAS_H / 2, CANVAS_W, CANVAS_H, 0x000000, 0.7));
    const msg = '系统维护中';
    c.add(this.add.text(CANVAS_W / 2, CANVAS_H / 2, msg, { ...FONT, fontSize: '24px' }).setOrigin(0.5));
    c.setDepth(100);
    this.maintenanceOverlay = c;
  }

  private hideMaintenanceOverlay() {
    this.maintenanceOverlay?.destroy();
    this.maintenanceOverlay = undefined;
  }
}