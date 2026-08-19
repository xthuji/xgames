import Phaser from 'phaser';
import { net } from '../net/ws';
import { store } from '../state/store';
import { validateAndFixSceneState } from '../utils/sceneSync';
import { setFlowState, FlowState, isInGameFlow } from '../flow/gameFlow';
import { currentGame, currentGameSceneKey } from '../registry';
import {
  MsgTypes,
  AddBotPayload,
  ErrorPayload,
  GameStartPayload,
  GameSyncResultPayload,
  PlayerJoinedPayload,
  PlayerLeftPayload,
  PlayerReadyPayload,
} from '../protocol';
const FONT = { fontFamily: 'Arial', fontSize: '20px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;

/** 座位卡片基础尺寸 */
const SEAT_CARD_W = 210;
const SEAT_CARD_H = 176;
/** 每行最多席位；超过则自动折为多行 */
const MAX_SEAT_PER_ROW = 5;

/** 多行时的各行中心 Y（标题下方、底部按钮上方纵向均匀布点，保证卡片互不重叠） */
function seatRowYs(rows: number): number[] {
  if (rows <= 1) return [360];
  const top = 300;
  const step = 180;
  return Array.from({ length: rows }, (_, r) => top + r * step);
}

/**
 * 座位布局：席位并排成一行，席位较多时自动折成多行，并对每行水平居中。
 * 每行不超过 MAX_SEAT_PER_ROW 个；均分各行席位并尽量均衡。
 */
function seatLayout(count: number): Array<[number, number]> {
  const perRow = Math.min(MAX_SEAT_PER_ROW, count);
  const rows = Math.ceil(count / perRow);
  const base = Math.floor(count / rows);
  const extra = count % rows;
  const rowYs = seatRowYs(rows);
  const out: Array<[number, number]> = [];
  for (let r = 0; r < rows; r++) {
    // 均分：前 extra 行多分 1 个，保证各行席位尽量均衡
    const n = base + (r < extra ? 1 : 0);
    const rowW = n * SEAT_CARD_W;
    const startX = Math.round((1280 - rowW) / 2) + Math.round(SEAT_CARD_W / 2);
    const rowY = rowYs[Math.min(r, rowYs.length - 1)];
    for (let c = 0; c < n; c++) {
      out.push([startX + c * SEAT_CARD_W, rowY]);
    }
  }
  return out;
}

/** RoomScene 房间等待：三座位布局、准备状态、房号展示、退出 */
export class RoomScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];
  private seatCount = 0;
  private seatPositions: Array<[number, number]> = [];
  private seatCards: Phaser.GameObjects.Graphics[] = [];
  private seatAvatars: Phaser.GameObjects.Arc[] = [];
  private seatAvatarTexts: Phaser.GameObjects.Text[] = [];
  private seatNames: Phaser.GameObjects.Text[] = [];
  private seatStatus: Phaser.GameObjects.Text[] = [];
  private actionBtn!: Phaser.GameObjects.Rectangle;
  private actionLabel!: Phaser.GameObjects.Text;
  private ready = false;
  private readyTimestamp = 0; // 记录准备时间，用于超时重试

  constructor() {
    super('Room');
  }

  create() {
    const gdef = currentGame();
    // ── 重置场景实例残留状态 ──
    // Phaser 场景实例是单例复用的，scene.start 重启时只重跑 create()，
    // 实例字段不会自动复位：
    //   - 上轮残留 ready=true → autoStartBotRoom 被直接跳过，永不发 MsgReady，
    //     人机房返回房间后无法自动开局；
    //   - 残留 readyTimestamp → update() 兑底误判“已准备超 3 秒”，
    //     立刻 forceEnterGame 进入不存在的对局后被踢回大厅；
    //   - 座位相关数组累积 → renderSeats 写入已销毁的旧对象，座位信息不刷新。
    this.ready = false;
    this.readyTimestamp = 0;
    this.seatCount = 0;
    this.seatPositions = [];
    this.seatCards = [];
    this.seatAvatars = [];
    this.seatAvatarTexts = [];
    this.seatNames = [];
    this.seatStatus = [];

    // ── 关键修复：对局活跃时 Room 直通 Game，避免 Room 覆盖 Game ──
    // 人机练习时 MsgRoomJoined(→Room) 与 MsgGameStart 快速连发，且都可能被 LobbyScene
    // 在同一帧处理。Phaser 的 scene.start('Game') 只会停止"调用方"场景（Lobby），
    // 不会停止已被 MsgRoomJoined 排队/启动的 Room，导致 Room 与 Game 同时活跃、
    // Room 覆盖在 Game 之上，用户看到的是房间等待界面而非对局画面。
    // 因此 Room 一旦在对局已活跃时被创建，必须立即停止自身并确保进入 Game。
    if (store.inGame) {
      setFlowState(FlowState.GAME);
      // 关键：isActive 只认 RUNNING。若对局场景已排队/正在创建（PENDING~CREATING），
      // 再 scene.start 会二次入队，processQueue 把已运行的对局场景停掉重启，
      // 其 shutdown 在 stateReceived 前发 MsgLeaveGame，误杀服务端刚开的对局。
      const gameKey = currentGameSceneKey();
      const gameStatus = this.scene.getStatus(gameKey);
      const gameIncoming = gameStatus !== Phaser.Scenes.SHUTDOWN
        && gameStatus !== Phaser.Scenes.DESTROYED;
      if (gameIncoming) {
        this.scene.stop('Room'); // 对局场景已排队/创建/运行中，停止 Room 自身即可
      } else {
        this.scene.start(gameKey); // 停止 Room（this）并启动对局场景
      }
      return;
    }

    // ── 流程状态机：标记进入房间 ──
    // 关键：如果 flow 已在 GAME 流程中（MsgGameStart 已被上一场景处理），
    // 不能设为 ROOM，否则 setFlowState(GAME→ROOM) 会触发 registerFlowCleanup
    // → net.clearPending() 清空当前局的关键缓冲消息（MsgDealCards 等）
    if (!isInGameFlow()) {
      setFlowState(FlowState.ROOM);
    }

    // ── 同步检测：向服务端查询真实状态，修正 UI 与后台的不一致 ──
    // 覆盖场景：GameScene 异常回退时，服务端对局可能仍在运行
    // 检测到 "active" 后自动转入 GameScene
    // 注意：如果已进入对局流程（MsgGameStart 已到达），跳过同步避免重复触发 forceEnterGame
    if (!isInGameFlow()) {
      net.send(MsgTypes.MsgGameSync);
    }

    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);

    // 侧边栏已由外壳页面（shell.ts，纯 DOM）提供，与游戏画布物理隔离

    this.add.text(640, 60, `房间 ${store.roomCode}`, { ...FONT, fontSize: '40px', color: '#ffd54f' })
      .setOrigin(0.5);

    const shareUrl = `${location.origin}${location.pathname}?room=${store.roomCode}`;
    const shareBtn = this.add.rectangle(960, 60, 180, 40, 0x1976d2).setStrokeStyle(1, 0xffffff, 0.5)
      .setInteractive({ useHandCursor: true });
    this.add.text(960, 60, '📤 分享房间', { ...FONT, fontSize: '18px' }).setOrigin(0.5);
    shareBtn.on('pointerdown', () => {
      navigator.clipboard?.writeText(shareUrl).then(() => {
        this.showToast('邀请链接已复制 ✨');
      }).catch(() => {
        this.showToast(`房间链接: ${shareUrl}`);
      });
    });

    this.add.text(640, 108, '点击房号可复制房间号', { ...FONT, fontSize: '14px', color: '#aaaaaa' }).setOrigin(0.5);
    const roomBg = this.add.rectangle(640, 60, 300, 54, 0x000000, 0.3).setInteractive({ useHandCursor: true });
    roomBg.on('pointerdown', () => {
      navigator.clipboard?.writeText(store.roomCode).then(() => this.showToast('房间号已复制')).catch(() => {});
    });

    // 座位（按当前游戏的最大人数布局：并排成行，席位多时自动折行）
    const seats = seatLayout(gdef.maxPlayers);
    this.seatCount = seats.length;
    this.seatPositions = seats;
    for (let i = 0; i < seats.length; i++) {
      const [x, y] = seats[i];
      const card = this.add.graphics().setDepth(1);
      const avatar = this.add.circle(x, y - 46, 34, 0x455a64).setStrokeStyle(2, 0xffffff, 0.6).setDepth(2);
      const avatarText = this.add.text(x, y - 46, '?', { ...FONT, fontSize: '26px', color: '#ffffff' }).setOrigin(0.5).setDepth(3);
      const name = this.add.text(x, y - 6, '', { ...FONT, fontSize: '19px', color: '#ffffff' }).setOrigin(0.5).setDepth(3);
      const status = this.add.text(x, y + 32, '', { ...FONT, fontSize: '15px' }).setOrigin(0.5).setDepth(3);
      this.seatCards.push(card);
      this.seatAvatars.push(avatar);
      this.seatAvatarTexts.push(avatarText);
      this.seatNames.push(name);
      this.seatStatus.push(status);
    }

    // ── 底部按钮栏：根据房主身份和游戏能力动态排列 ──
    const isCreator = store.roomCreatorID === store.playerID;
    let btnY = 620;

    // 准备按钮（所有人可见）
    const startX = 380;
    this.actionBtn = this.add.rectangle(startX, btnY, 110, 42, 0x2e7d32).setStrokeStyle(1, 0xffffff, 0.4)
      .setInteractive({ useHandCursor: true });
    this.actionLabel = this.add.text(startX, btnY, '准 备', { ...FONT, fontSize: '20px' }).setOrigin(0.5);
    this.actionBtn.on('pointerdown', () => {
      this.ready = !this.ready;
      this.readyTimestamp = this.ready ? this.time.now : 0;
      net.send(this.ready ? MsgTypes.MsgReady : MsgTypes.MsgCancelReady);
      this.updateActionButton();
    });
    this.updateActionButton();

    // 退出按钮（所有人可见）
    const exitX = startX + 130;
    const exitBtn = this.add.rectangle(exitX, btnY, 110, 42, 0x757575).setStrokeStyle(1, 0xffffff, 0.4)
      .setInteractive({ useHandCursor: true });
    this.add.text(exitX, btnY, '退 出', { ...FONT, fontSize: '20px' }).setOrigin(0.5);
    exitBtn.on('pointerdown', () => {
      net.send(MsgTypes.MsgLeaveRoom);
      store.reset();
      this.scene.start('Lobby');
    });

    // 关闭房间按钮：仅房主可见（关闭整个房间并通知所有人）
    let nextX = exitX + 130;
    if (isCreator) {
      const closeBtn = this.add.rectangle(nextX, btnY, 130, 42, 0xc62828).setStrokeStyle(1, 0xffffff, 0.4)
        .setInteractive({ useHandCursor: true });
      this.add.text(nextX, btnY, '🚪 关闭', { ...FONT, fontSize: '18px' }).setOrigin(0.5);
      closeBtn.on('pointerdown', () => {
        this.showToast('正在关闭房间…');
        net.send(MsgTypes.MsgCloseRoom);
        // 房主关闭房间后直接回大厅（后端会通知其他人）
        store.reset();
        setFlowState(FlowState.LOBBY);
        this.scene.start('Lobby');
      });
      nextX += 150;
    }

    // 添加机器人：仅房间创建人可操作（服务端校验），手动占用空席位（入座即准备），
    // 满席位且全员就绪自动开局；房间已满、非房主或游戏不支持机器人时隐藏按钮。
    if (gdef.supportsBots && store.players.length < gdef.maxPlayers && isCreator) {
      const botBtn = this.add.rectangle(nextX, btnY, 140, 42, 0x6a1b9a).setStrokeStyle(1, 0xffffff, 0.4)
        .setInteractive({ useHandCursor: true });
      this.add.text(nextX, btnY, '🤖 添加机器人', { ...FONT, fontSize: '17px' }).setOrigin(0.5);
      botBtn.on('pointerdown', () => {
        if (currentGame().hasDifficulty) {
          this.showAddBotDialog();
        } else {
          net.send(MsgTypes.MsgAddBot);
        }
      });
    }

    // 确保语音合成在场景切换时被取消（避免上一局结算语音残留）
    try { window.speechSynthesis?.cancel(); } catch { /* ignore */ }

    // 人机房返回房间后，若 MsgReady 此前因竞态被服务端拒绝（ErrGameStarted），
    // 这里补一次延迟自动准备，确保能进入下一轮
    this.autoStartBotRoom();

    this.subscribe();
    this.renderSeats();

    // URL 路由：标记当前场景
    if (location.hash !== '#/room') location.hash = '/room';

    // ── UI-游戏状态一致性检查 ──
    // 注意：如果 flow 已在 GAME 流程中（MsgGameStart 已被 LobbyScene/RoomScene 处理），
    // 跳过一致性检查。因为此时 RoomScene 只是帧间过渡，
    // validateAndFixSceneState 会误判"Room 场景 + GAME flow"为异常并清空所有数据。
    if (!isInGameFlow()) {
      validateAndFixSceneState('Room');
    }

    // Phaser 3.90 不会自动调用场景子类的 shutdown()，需手动接线，
    // 否则切场景时订阅永不清理，消息被残留实例重复处理。
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());
  }

  shutdown() {
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
  }

  update(time: number, _delta: number) {
    // 兑底：已准备超过 3 秒但仍未进入游戏 → 强制重试
    // 覆盖场景：scene.start('Game') 不生效、GameScene.create() 异常后回退等
    if (this.ready && this.readyTimestamp > 0 && !store.inGame
        && time - this.readyTimestamp > 3000
        && store.players.some((pl) => pl.is_bot)) {
      console.warn('[RoomScene] 兑底：已准备但 3s 未进入游戏，强制重试');
      this.forceEnterGame();
    }
  }

  private updateActionButton() {
    if (this.ready) {
      this.actionBtn.setFillStyle(0xf57c00);
      this.actionLabel.setText('取消准备');
    } else {
      this.actionBtn.setFillStyle(0x2e7d32);
      this.actionLabel.setText('准 备');
    }
  }

  private showToast(msg: string) {
    const t = this.add.text(640, 200, msg, { ...FONT, color: '#ffd54f', backgroundColor: '#000000cc', padding: { x: 12, y: 6 } })
      .setOrigin(0.5).setDepth(100);
    this.tweens.add({ targets: t, alpha: 0, y: 160, duration: 800, delay: 1200, onComplete: () => t.destroy() });
  }

  /** 添加机器人难度选择弹窗：难度只影响机器人记牌完整度，出牌策略完全一致 */
  private showAddBotDialog() {
    const dlg = this.add.container(640, 360).setDepth(200);
    const overlay = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.5).setInteractive();
    const bg = this.add.rectangle(0, 0, 500, 280, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    const title = this.add.text(0, -105, '🤖 机器人难度（仅影响记牌完整度）', { ...FONT, fontSize: '20px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 }).setOrigin(0.5);

    const levels: Array<[string, string, number, string]> = [
      ['normal', '🙂 正常：记关键牌', 0x1e88e5, '记忆 A/2/大小王'],
      ['hard', '🤓 专家：全量记牌', 0xfb8c00, '记住所有已出牌'],
    ];
    const buttons: Phaser.GameObjects.GameObject[] = [overlay, bg, title];
    levels.forEach(([key, label, color, desc], i) => {
      const row = this.add.container(0, -30 + i * 72);
      const btn = this.add.rectangle(0, 0, 420, 58, color, 0.85)
        .setStrokeStyle(2, 0xffffff, 0.4)
        .setInteractive({ useHandCursor: true });
      btn.on('pointerover', () => { btn.setFillStyle(color, 1); btn.setScale(1.02); });
      btn.on('pointerout', () => { btn.setFillStyle(color, 0.85); btn.setScale(1); });
      const t = this.add.text(-180, -8, label, { ...FONT, fontSize: '20px', stroke: '#000000', strokeThickness: 3, fontStyle: 'bold' }).setOrigin(0, 0.5);
      const d = this.add.text(-180, 14, desc, { ...FONT, fontSize: '14px', color: '#e0e0e0' }).setOrigin(0, 0.5);
      row.add([btn, t, d]);
      btn.on('pointerdown', () => {
        dlg.destroy();
        net.send(MsgTypes.MsgAddBot, { difficulty: key } satisfies AddBotPayload);
      });
      buttons.push(row);
    });

    const close = this.add.text(225, -120, '✕', { ...FONT, fontSize: '24px', color: '#ffffff' })
      .setInteractive({ useHandCursor: true });
    close.on('pointerdown', () => dlg.destroy());
    buttons.push(close);
    dlg.add(buttons);
  }

  private renderSeats() {
    for (let seat = 0; seat < this.seatCount; seat++) {
      const [x, y] = this.seatPositions[seat];
      const card = this.seatCards[seat];
      const avatar = this.seatAvatars[seat];
      const avatarText = this.seatAvatarTexts[seat];
      const name = this.seatNames[seat];
      const status = this.seatStatus[seat];
      if (!card || !name) continue;

      const cx = x - SEAT_CARD_W / 2;
      const cy = y - SEAT_CARD_H / 2;
      const p = store.players.find((pl) => pl.seat === seat);

      card.clear();
      if (!p) {
        // 空席位：深色半透明卡片
        card.fillStyle(0x14172b, 0.7);
        card.fillRoundedRect(cx, cy, SEAT_CARD_W, SEAT_CARD_H, 16);
        card.lineStyle(1.5, 0xffffff, 0.22);
        card.strokeRoundedRect(cx, cy, SEAT_CARD_W, SEAT_CARD_H, 16);
        avatar.setFillStyle(0x2c3350);
        avatarText.setText('?');
        name.setText('空 位');
        status.setText('等待加入…');
        status.setColor('#888888');
        continue;
      }

      const ready = p.ready;
      // 已入座：深绿（未准备）/ 亮绿（已准备）卡片
      card.fillStyle(ready ? 0x1b5e20 : 0x263238, 0.92);
      card.fillRoundedRect(cx, cy, SEAT_CARD_W, SEAT_CARD_H, 16);
      card.lineStyle(2, ready ? 0x66bb6a : 0xffffff, ready ? 0.9 : 0.4);
      card.strokeRoundedRect(cx, cy, SEAT_CARD_W, SEAT_CARD_H, 16);
      // 顶部描金细边美化
      card.lineStyle(1, 0xffd54f, ready ? 0.5 : 0.25);
      card.strokeRoundedRect(cx + 5, cy + 5, SEAT_CARD_W - 10, 12, 6);

      // 头像与首字
      avatar.setFillStyle(p.is_bot ? 0x6a1b9a : (ready ? 0x2e7d32 : 0x1976d2));
      avatarText.setText((p.name || '?').trim().charAt(0) || '?');
      name.setText(p.name);
      status.setText(`${p.is_bot ? '🤖 机器人' : ''}${ready ? '✔ 已准备' : '未准备'}`);
      status.setColor(ready ? '#a5d6a7' : '#ffd54f');
    }
  }

  private subscribe() {
    const on = (t: Parameters<typeof net.on>[0], h: Parameters<typeof net.on>[1]) => this.unsubscribers.push(net.on(t, h));

    on(MsgTypes.MsgPlayerJoined, (p: PlayerJoinedPayload) => {
      store.players.push(p.player);
      this.renderSeats();
    });
    on(MsgTypes.MsgPlayerLeft, (p: PlayerLeftPayload) => {
      store.players = store.players.filter((pl) => pl.id !== p.player_id);
      this.renderSeats();
    });
    on(MsgTypes.MsgPlayerReady, (p: PlayerReadyPayload) => {
      const pl = store.player(p.player_id);
      if (pl) pl.ready = p.ready;
      this.renderSeats();
    });
    on(MsgTypes.MsgError, (p: ErrorPayload) => {
      this.showToast(p.message);
      // 房主关闭房间 → 所有人回大厅
      if (p.message.includes('关闭房间')) {
        store.reset();
        setFlowState(FlowState.LOBBY);
        this.scene.start('Lobby');
        return;
      }
      // 人机房返回房间时若 MsgReady 因竞态被拒（服务端尚未复位），
      // 先复位本地准备标记再重试，否则 autoStartBotRoom 会被残留的 ready=true 直接跳过
      if (this.ready) {
        this.ready = false;
        this.readyTimestamp = 0;
        this.updateActionButton();
      }
      this.autoStartBotRoom();
    });
    on(MsgTypes.MsgGameStart, (p: GameStartPayload) => {
      // ── 递增对局代次，过滤过期重放 ──
      // 仅在当前未进入对局流程时递增，避免与 LobbyScene 同时活跃时双重递增
      if (!isInGameFlow()) store.gameStartEpoch++;

      // 防御性修正：服务端保证真人永远不是机器人，但若因任何异常导致 game_start
      // 把当前玩家标为 is_bot，本地纠正，避免 UI 把真人当机器人、隐藏决策面板
      for (const pl of p.players) {
        if (pl.id === store.playerID && pl.is_bot) {
          console.warn('[game_start] 当前玩家被错误标记为机器人，本地修正');
          pl.is_bot = false;
        }
      }
      store.players = p.players;
      // 新对局开始，清除上一局残留的挂机状态，确保用户可以自由操作
      store.afkPlayers.clear();
      store.gameEnded = false; // 重置对局结束标志，允许处理新对局的挂机变更
      store.inGame = true; // 标记对局活跃
      // 注意：不在此处设 stateReceived=true，MsgGameStart 仅含玩家列表，
      // 完整对局状态需等 MsgGameState 到达后才算完整
      // ── 流程状态机：进入对局（防止重复触发导致 GameScene 二次创建）──
      if (!isInGameFlow()) {
        setFlowState(FlowState.GAME);
        this.forceEnterGame();
      }
    });

    // ── 同步检测响应：根据服务端真实状态自动修正 UI ──
    on(MsgTypes.MsgGameSyncResult, (p: GameSyncResultPayload) => {
      if (p.status === 'active') {
        // 服务端对局仍在运行，但 UI 在 RoomScene → 转入 GameScene 恢复对局
        // 防止重复触发：如果已在对局流程中（MsgGameStart 已处理），跳过
        if (isInGameFlow()) {
          console.log('[RoomScene] 同步检测 active 但已在对局流程中，跳过');
          return;
        }
        console.warn('[RoomScene] 同步检测：服务端对局 active，自动转入 GameScene');
        store.gameEnded = false;
        store.inGame = true; // 关键：标记对局活跃，防止 GameScene shutdown 误发 MsgLeaveGame
        // ── 流程状态机：进入对局 ──
        setFlowState(FlowState.GAME);
        this.forceEnterGame();
      } else if (p.status === 'none') {
        // 服务端无房间 → 回退大厅
        console.warn('[RoomScene] 同步检测：服务端无房间，回退大厅');
        store.reset();
        setFlowState(FlowState.LOBBY);
        this.scene.start('Lobby');
      }
      // status === 'waiting' → 正常，房间等待中，无需处理
    });
  }

  /** 强制进入对局场景 */
  private forceEnterGame() {
    // 防御性停止 Lobby，确保进入对局场景后仅它活跃（Room 由 scene.start 自动停止）
    this.scene.stop('Lobby');
    this.scene.start(currentGameSceneKey());
  }

  /** 人机房：若当前未对局且未准备，延迟 1.5s 自动发 MsgReady 开启下一轮。
   *  延迟足够长让用户看到 RoomScene，避免"点返回房间无反应"的错觉。 */
  private autoStartBotRoom(): void {
    if (!store.players.some((pl) => pl.is_bot)) return;
    if (store.inGame) return;
    if (this.ready) return;
    this.time.delayedCall(1500, () => {
      if (store.inGame) return; // 已开局
      if (!store.players.some((pl) => pl.is_bot)) return;
      if (this.ready) return; // 用户已手动准备
      this.ready = true;
      this.readyTimestamp = this.time.now;
      net.send(MsgTypes.MsgReady);
      this.updateActionButton();
    });
  }
}