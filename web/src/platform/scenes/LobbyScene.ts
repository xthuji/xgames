import Phaser from 'phaser';
import { net, hydrateIdentity } from '../net/ws';
import { store } from '../state/store';
import { safePlay } from '../ui/audio';
import { showGameRules } from '../ui/modal';
import { getGameRule, getGameName } from '../rules/gameRules';
import { validateAndFixSceneState } from '../utils/sceneSync';
import { setFlowState, FlowState, isInGameFlow } from '../flow/gameFlow';
import { currentGame, currentGameSceneKey, getGame, restoreCurrentGameState } from '../registry';
import {
  MsgTypes,
  ConnectedPayload,
  CreateRoomPayload,
  ErrorPayload,
  GameStartPayload,
  GetLeaderboardPayload,
  GetRoomListPayload,
  GetStatsPayload,
  JoinRoomPayload,
  LeaderboardResultPayload,
  MaintenancePayload,
  OnlineCountPayload,
  PracticeMatchPayload,
  ProfileUpdatedPayload,
  ReconnectedPayload,
  RecreateRoomPayload,
  RoomCreatedPayload,
  RoomExistsData,
  RoomJoinedPayload,
  RoomListResultPayload,
  StatsResultPayload,
  UpdateProfilePayload,
} from '../protocol';

const FONT = { fontFamily: 'Arial', fontSize: '20px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;
const FONT_BOLD = { fontFamily: 'Arial', fontSize: '26px', color: '#ffe082', stroke: '#1a1a00', strokeThickness: 4, fontStyle: 'bold' } as Phaser.Types.GameObjects.Text.TextStyle;
const FONT_TITLE = { fontFamily: 'Arial', fontSize: '22px', color: '#ffffff', stroke: '#000000', strokeThickness: 3, fontStyle: 'bold' } as Phaser.Types.GameObjects.Text.TextStyle;

/** LobbyScene 大厅：侧边栏 + 建房/人机、房间列表、排行榜、统计 */
export class LobbyScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];
  private onlineText!: Phaser.GameObjects.Text;
  private nameText!: Phaser.GameObjects.Text;
  private soundText!: Phaser.GameObjects.Text;
  private roomListContainer!: Phaser.GameObjects.Container;
  private boardContainer!: Phaser.GameObjects.Container;
  private boardType = 'total';
  private maintenanceOverlay?: Phaser.GameObjects.Container;
  /** 待重建的建房参数：用户确认关闭旧房间后发送 recreate_room（单请求完成关闭+新建） */
  private pendingRecreate?: { gameID: string; practice: boolean; difficulty?: string };

  constructor() {
    super('Lobby');
  }

  create() {
    // 单机游戏没有大厅形态：当前游戏为单机时直接进入其对局场景，
    // 避免携带后端未知的 game_id 拉取房间列表/排行榜/统计导致报错刷屏
    if (currentGame().singlePlayer) {
      this.scene.start(currentGameSceneKey());
      return;
    }

    // ── 进入大厅时清理残留房间/对局状态 ──
    // 切换游戏、重连恢复、或上一局异常退出后，store 可能残留 roomCode + players，
    // 导致后续操作（如切换游戏后缓冲消息重放）误触发进入房间。
    // 大厅是"无房间"状态，用户需主动创建/加入房间。
    // 注意：仅当 roomCode 与 players 同时残留时才清理，
    // 单独 roomCode 可能来自 MsgReconnected 重连恢复（需保留供用户手动加入）。
    if (store.roomCode && store.players.length > 0) {
      store.reset();
    }
    // 清除网络层缓冲的关键消息（如上一局残留的 MsgRoomCreated/MsgRoomJoined），
    // 避免新大厅 subscribe() 时重放过期消息导致自动进入房间
    net.clearPending();
    // 身份兑底恢复：connected/reconnected 可能在此前无订阅者时到达且缓冲已被清理，
    // 用网络层缓存的最近连接身份恢复 store.playerID，避免面板/守卫全面失效
    hydrateIdentity();

    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    safePlay(this, 'music_room', { loop: true, volume: 0.4 });

    // 侧边栏已由外壳页面（shell.ts，纯 DOM）提供，与游戏画布物理隔离
    this.buildTopBar();
    this.buildActionButtons();
    this.buildSoundToggle();
    this.buildRoomList();
    this.buildLeaderboard();
    this.subscribe();

    // 初始拉取（均携带当前游戏，服务端缺省兼容旧行为）
    const gameID = currentGame().id;
    net.send(MsgTypes.MsgGetOnlineCount);
    net.send(MsgTypes.MsgGetRoomList, {} satisfies GetRoomListPayload); // 跨游戏展示所有已创建房间
    net.send(MsgTypes.MsgGetLeaderboard, { game_id: gameID, type: this.boardType, offset: 0, limit: 10 } satisfies GetLeaderboardPayload);
    net.send(MsgTypes.MsgGetStats, { game_id: gameID } satisfies GetStatsPayload);
    net.send(MsgTypes.MsgGetMaintenanceStatus);

    // 定时刷新房间列表（实时展示已创建房间，5s）
    this.time.addEvent({
      delay: 5000,
      loop: true,
      callback: () => net.send(MsgTypes.MsgGetRoomList, {} satisfies GetRoomListPayload),
    });

    // URL 参数自动加入房间
    const urlRoom = new URLSearchParams(location.search).get('room');
    if (urlRoom) {
      this.time.delayedCall(500, () => {
        this.toast('正在加入房间 ' + urlRoom);
        net.send(MsgTypes.MsgJoinRoom, { room_code: urlRoom } satisfies JoinRoomPayload);
      });
    }

    if (store.maintenance) this.showMaintenanceOverlay();

    // URL 路由：标记当前场景
    if (location.hash !== '#/lobby') location.hash = '/lobby';

    // ── 流程状态机：标记进入大厅 ──
    setFlowState(FlowState.LOBBY);

    // ── UI-游戏状态一致性检查 ──
    validateAndFixSceneState('Lobby');

    // Phaser 3.90 不会自动调用场景子类的 shutdown()，需手动接线：
    // 否则切场景时订阅/音效永不清理，handler 持续累积导致消息被重复处理。
    // restart 时 SHUTDOWN 在 create() 之前触发，故每次 create 注册不会重复。
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());
  }

  shutdown() {
    this.sound.stopAll();
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
  }

  // --- 侧边栏与浏览器按钮已迁至外壳页面（shell.ts） ---

  // --- 顶部栏 ---

  private buildTopBar() {
    const leftX = 20;
    // 左上角：游戏图标 + 名称（与单机游戏场景风格一致）
    const g = currentGame();
    this.add.text(leftX, 14, `${g.icon} ${g.name}`, { ...FONT_BOLD, fontSize: '22px', fontStyle: 'bold' });

    // 玩家昵称行（游戏名称下方）
    this.nameText = this.add.text(leftX, 44, '连接中…', { ...FONT, fontSize: '18px', color: '#ffe082' })
      .setInteractive({ useHandCursor: true });
    this.nameText.on('pointerover', () => {
      this.nameText.setColor('#fff176');
      this.nameText.setStyle({ ...this.nameText.style, strokeThickness: 5 });
    });
    this.nameText.on('pointerout', () => {
      this.nameText.setColor('#ffe082');
      this.nameText.setStyle({ ...this.nameText.style, strokeThickness: 4 });
    });
    this.nameText.on('pointerdown', () => this.editNickname());

    this.onlineText = this.add.text(1260, 14, '在线 0', {
      ...FONT_TITLE, color: '#81d4fa', fontSize: '18px',
    }).setOrigin(1, 0);
  }

  private editNickname() {
    const name = window.prompt('修改昵称（1-20字符）：', store.playerName);
    if (name === null) return;
    const trimmed = name.trim();
    if (!trimmed || trimmed.length > 20) {
      this.toast('昵称长度需在 1-20 字符之间');
      return;
    }
    net.send(MsgTypes.MsgUpdateProfile, { name: trimmed } satisfies UpdateProfilePayload);
  }

  // --- 在外部浏览器打开（已迁至外壳页面侧边栏，见 shell.ts）---

  // --- 操作按钮 ---

  private buildActionButtons() {
    // 按钮左缘距画布左边界 10px（无侧边栏遮挡，无需额外预留）
    const baseX = 120;
    const mk = (y: number, color: number, label: string, cb: () => void, enabled = true) => {
      const bg = this.add.rectangle(baseX, y, 220, 58, color)
        .setStrokeStyle(3, 0xffffff, 0.8)
        .setInteractive({ useHandCursor: enabled });
      bg.on('pointerover', () => bg.setFillStyle(color + 0x222222));
      bg.on('pointerout', () => bg.setFillStyle(color));
      if (enabled) {
        bg.on('pointerdown', cb);
      } else {
        bg.setAlpha(0.5);
      }
      this.add.text(baseX, y, label, { ...FONT_BOLD, fontSize: '22px', color: '#fffde7', fontStyle: 'bold' }).setOrigin(0.5);
    };
    mk(130, 0x2e7d32, '🏠 创建房间', () => {
      if (store.maintenance) return this.toast('维护中，暂不开放');
      const gameID = currentGame().id;
      // 记录建房参数：遇旧房间冲突时经弹窗确认后以 recreate_room 单请求完成关闭+新建
      this.pendingRecreate = { gameID, practice: false };
      net.send(MsgTypes.MsgCreateRoom, { game_id: gameID } satisfies CreateRoomPayload);
    });
    mk(210, 0x6a1b9a, '🤖 人机练习', () => {
      if (store.maintenance) return this.toast('维护中，暂不开放');
      const g = currentGame();
      if (!g.supportsBots) return this.toast('当前游戏暂不支持人机练习');
      if (g.hasDifficulty) {
        this.showDifficultyDialog();
      } else {
        this.pendingRecreate = { gameID: g.id, practice: true };
        net.send(MsgTypes.MsgPracticeMatch, { game_id: g.id } satisfies PracticeMatchPayload);
      }
    });
    mk(290, 0x37474f, '📊 个人统计', () => net.send(MsgTypes.MsgGetStats, { game_id: currentGame().id } satisfies GetStatsPayload));
    // 规则按钮：与创建房间/人机练习等左侧操作按钮并排摆放
    mk(370, 0x0288d1, '📖 游戏规则', () => this.showRulesDialog());
  }

  private showRulesDialog() {
    const gameId = currentGame().id;
    const rules = getGameRule(gameId);
    const gameName = getGameName(gameId);

    if (!rules) {
      this.toast('暂无该游戏的规则说明');
      return;
    }

    showGameRules(this, gameName, rules);
  }

  private buildSoundToggle() {
    // y=48：避开顶栏浏览器按钮（y3~33）与在线人数文本（y14~32）
    this.soundText = this.add.text(1260, 48, store.muted ? '🔇 静音' : '🔊 音效', {
      ...FONT, fontSize: '18px', color: store.muted ? '#aaaaaa' : '#ffd54f',
    }).setOrigin(1, 0.5).setInteractive({ useHandCursor: true });
    this.soundText.on('pointerdown', () => {
      store.toggleMuted();
      this.sound.setMute(store.muted);
      this.soundText.setText(store.muted ? '🔇 静音' : '🔊 音效');
      this.soundText.setColor(store.muted ? '#aaaaaa' : '#ffd54f');
    });
  }

  private showDifficultyDialog() {
    const g = currentGame();
    const dlg = this.add.container(640, 360).setDepth(200);
    // 全屏半透明遮罩
    const overlay = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.5).setInteractive();
    // 弹窗主体
    const bg = this.add.rectangle(0, 0, 520, 380, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    
    // 根据游戏类型定制标题和难度选项
    let titleText = '🤖 选择难度';
    let levels: Array<[string, string, number, string, string]>;
    
    if (g.id === 'ddz') {
      // 斗地主：基于记牌完整度
      titleText = '🤖 选择难度（影响记牌完整度）';
      levels = [
        ['easy', '😊 简单', 0x4caf50, '记关键牌（A/2/王），偶尔失误', '🟢'],
        ['normal', '🙂 普通', 0x1e88e5, '全量记牌，策略均衡', '🔵'],
        ['hard', '🤓 困难', 0xfb8c00, '完美记牌+MCTS残局搜索', '🟠'],
      ];
    } else if (g.id === 'mahjong') {
      // 麻将：基于攻守平衡和向听数精度
      titleText = '🤖 选择难度（影响攻守策略）';
      levels = [
        ['easy', '😊 简单', 0x4caf50, '进攻优先，10%失误率', '🟢'],
        ['normal', '🙂 普通', 0x1e88e5, '攻守平衡，5%失误率', '🔵'],
        ['hard', '🤓 困难', 0xfb8c00, '防守优先，精确计算', '🟠'],
      ];
    } else if (g.id === 'gomoku') {
      // 五子棋：基于搜索深度
      titleText = '🤖 选择难度（影响搜索深度）';
      levels = [
        ['easy', '😊 简单', 0x4caf50, '搜索2层，30%随机扰动', '🟢'],
        ['normal', '🙂 普通', 0x1e88e5, '搜索3层，15%随机扰动', '🔵'],
        ['hard', '🤓 困难', 0xfb8c00, '搜索4层，VCF/VCT完备', '🟠'],
      ];
    } else if (g.id === 'chess') {
      // 中国象棋：基于搜索深度
      titleText = '🤖 选择难度（影响棋力强度）';
      levels = [
        ['easy', '😊 简单', 0x4caf50, '搜索2层，适合新手陪练', '🟢'],
        ['normal', '🙂 普通', 0x1e88e5, '搜索4层，中级挑战', '🔵'],
        ['hard', '🤓 困难', 0xfb8c00, '搜索6层，高手对决', '🟠'],
      ];
    } else {
      // 默认三档难度
      levels = [
        ['easy', '😊 简单', 0x4caf50, '休闲模式，轻松上手', '🟢'],
        ['normal', '🙂 普通', 0x1e88e5, '标准模式，适度挑战', '🔵'],
        ['hard', '🤓 困难', 0xfb8c00, '专家模式，极限挑战', '🟠'],
      ];
    }
    
    const title = this.add.text(0, -155, titleText, { ...FONT, fontSize: '24px' }).setOrigin(0.5);
    // 分隔线
    const separator = this.add.rectangle(0, -125, 440, 1, 0xffd54f, 0.3);

    const buttons: Phaser.GameObjects.GameObject[] = [overlay, bg, title, separator];
    levels.forEach(([key, label, color, desc, indicator], i) => {
      const y = -40 + i * 80;
      const row = this.add.container(0, y);
      // 按钮背景
      const btn = this.add.rectangle(0, 0, 440, 65, color, 0.85)
        .setStrokeStyle(2, 0xffffff, 0.4)
        .setInteractive({ useHandCursor: true });
      btn.on('pointerover', () => { btn.setFillStyle(color, 1); btn.setScale(1.02); });
      btn.on('pointerout', () => { btn.setFillStyle(color, 0.85); btn.setScale(1); });
      // 难度名称
      const t = this.add.text(-195, -10, label, { ...FONT, fontSize: '20px', fontStyle: 'bold' }).setOrigin(0, 0.5);
      // 难度描述
      const d = this.add.text(-195, 16, desc, { ...FONT, fontSize: '14px', color: '#e0e0e0', strokeThickness: 0 }).setOrigin(0, 0.5);
      // 难度指示器
      const ind = this.add.text(195, 0, indicator, { fontSize: '26px' }).setOrigin(0.5);
      row.add([btn, t, d, ind]);
      btn.on('pointerdown', () => {
        dlg.destroy();
        const gameID = currentGame().id;
        this.pendingRecreate = { gameID, practice: true, difficulty: key };
        net.send(MsgTypes.MsgPracticeMatch, { game_id: gameID, difficulty: key } satisfies PracticeMatchPayload);
      });
      buttons.push(row);
    });

    // 关闭按钮
    const close = this.add.text(235, -165, '✕', { ...FONT, fontSize: '24px', color: '#ffffff' })
      .setInteractive({ useHandCursor: true });
    close.on('pointerover', () => close.setColor('#ff8a80'));
    close.on('pointerout', () => close.setColor('#ffffff'));
    close.on('pointerdown', () => dlg.destroy());
    buttons.push(close);

    dlg.add(buttons);
  }

  /** 已有旧房间弹窗：用户建房/人机练习时发现旧房间是自己创建的，询问是否关闭。
   *  旧房间可能已满员（人机房）或处于对局中，房间列表不可见，只能通过此弹窗关闭。 */
  private showCloseOldRoomDialog(data: RoomExistsData) {
    const dlg = this.add.container(640, 360).setDepth(200);
    const overlay = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.5).setInteractive();
    const bg = this.add.rectangle(0, 0, 480, 280, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    const title = this.add.text(0, -100, '🏠 已有旧房间', { ...FONT, fontSize: '24px' }).setOrigin(0.5);
    const separator = this.add.rectangle(0, -70, 400, 1, 0xffd54f, 0.3);

    const gameName = getGame(data.game_id)?.name ?? data.game_id;
    const msg1 = this.add.text(0, -30, `你已创建过房间 ${data.room_code}`, { ...FONT, fontSize: '18px' }).setOrigin(0.5);
    const msg2 = this.add.text(0, 0, `(${gameName})`, { ...FONT, fontSize: '18px', color: '#81d4fa' }).setOrigin(0.5);
    const msg3 = this.add.text(0, 30, '是否关闭旧房间并创建新房间？', { ...FONT, fontSize: '18px' }).setOrigin(0.5);

    // 取消按钮
    const cancelBtn = this.add.rectangle(-120, 80, 170, 50, 0x546e7a, 0.9)
      .setStrokeStyle(2, 0xffffff, 0.4)
      .setInteractive({ useHandCursor: true });
    cancelBtn.on('pointerover', () => cancelBtn.setFillStyle(0x546e7a, 1));
    cancelBtn.on('pointerout', () => cancelBtn.setFillStyle(0x546e7a, 0.9));
    cancelBtn.on('pointerdown', () => {
      dlg.destroy();
      this.pendingRecreate = undefined;
    });
    const cancelLabel = this.add.text(-120, 80, '❌ 取消', { ...FONT_BOLD, fontSize: '18px', color: '#fffde7' }).setOrigin(0.5);

    // 确认按钮
    const confirmBtn = this.add.rectangle(120, 80, 170, 50, 0xd84315, 0.9)
      .setStrokeStyle(2, 0xffffff, 0.4)
      .setInteractive({ useHandCursor: true });
    confirmBtn.on('pointerover', () => confirmBtn.setFillStyle(0xd84315, 1));
    confirmBtn.on('pointerout', () => confirmBtn.setFillStyle(0xd84315, 0.9));
    confirmBtn.on('pointerdown', () => {
      dlg.destroy();
      this.recreateOldRoom();
    });
    const confirmLabel = this.add.text(120, 80, '🗑️ 关闭并新建', { ...FONT_BOLD, fontSize: '16px', color: '#fffde7' }).setOrigin(0.5);

    dlg.add([overlay, bg, title, separator, msg1, msg2, msg3, cancelBtn, cancelLabel, confirmBtn, confirmLabel]);
  }

  /** 关闭旧房间并新建（单请求）：服务端 recreate_room 在一个接口内完成
   *  验证权限 → 彻底关闭旧房（停止对局/删快照/关记录/验证）→ 新建并返回最终结果。 */
  private recreateOldRoom() {
    // 无待重建参数时（非建房流程触发的冲突）按当前游戏新建普通房间
    const p = this.pendingRecreate ?? { gameID: currentGame().id, practice: false };
    this.toast('正在关闭旧房间并新建...');
    net.send(MsgTypes.MsgRecreateRoom, {
      game_id: p.gameID,
      practice: p.practice,
      difficulty: p.difficulty,
    } satisfies RecreateRoomPayload);
  }

  private buildRoomList() {
    // 中央区域：y 从 100 开始，在按钮区下方、排行榜左侧
    this.add.text(430, 100, '📋 已存在房间', FONT_TITLE).setOrigin(0.5, 0);
    this.roomListContainer = this.add.container(280, 140);
    const refresh = this.add.text(430 + 150, 100, '⟳ 刷新', { ...FONT, color: '#ffd54f', fontSize: '18px' }).setInteractive({ useHandCursor: true });
    refresh.on('pointerdown', () => net.send(MsgTypes.MsgGetRoomList, {} satisfies GetRoomListPayload));
  }

  private renderRoomList(rooms: RoomListResultPayload) {
    this.roomListContainer.removeAll(true);
    rooms.rooms.forEach((r, i) => {
      const col = i % 2;
      const row = Math.floor(i / 2);
      const card = this.add.rectangle(col * 170 + 80, row * 95 + 45, 160, 85, 0x000000, 0.4)
        .setStrokeStyle(2, 0xffd54f, 0.6)
        .setInteractive({ useHandCursor: true });
      const label = this.add.text(card.x, card.y - 26, r.room_code, { ...FONT_BOLD, fontSize: '20px', color: '#fffde7', strokeThickness: 3 }).setOrigin(0.5);
      const gameName = getGame(r.game_id)?.name ?? r.game_id;
      const stateText = r.state === 'playing' ? `${gameName}·对局中` : gameName;
      const meta = this.add.text(card.x, card.y - 2, stateText, {
        ...FONT, fontSize: '13px', color: r.state === 'playing' ? '#ffb74d' : '#81d4fa', strokeThickness: 2,
      }).setOrigin(0.5);
      const count = this.add.text(card.x, card.y + 22, `${r.player_count}/${r.max_players}${r.creator_name ? ' · ' + r.creator_name : ''}`, {
        ...FONT, fontSize: '14px', color: '#cccccc', strokeThickness: 2,
      }).setOrigin(0.5);
      if (r.state === 'playing') {
        // 对局进行中的房间不可加入（服务端也会拒绝），仅展示
        card.setStrokeStyle(2, 0x888888, 0.6);
      } else {
        card.on('pointerdown', () => {
          // 发送加入房间请求，服务器会检查用户是否已在房间中
          net.send(MsgTypes.MsgJoinRoom, { room_code: r.room_code });
        });
      }
      this.roomListContainer.add([card, label, meta, count]);
    });
    if (rooms.rooms.length === 0) {
      this.roomListContainer.add(
        this.add.text(150, 40, '暂无房间，创建一个吧', { ...FONT, color: '#aaaaaa' }),
      );
    }
  }

  // --- 排行榜 ---

  private buildLeaderboard() {
    const rightX = 1050;
    this.add.text(rightX, 48, '🏆 排行榜', FONT_TITLE).setOrigin(0.5, 0);
    this.boardContainer = this.add.container(860, 80);
    // 排行榜背景框：左侧 0 ~ 340, 顶部 0 ~ 440
    this.boardContainer.add(
      this.add.rectangle(170, 220, 340, 440, 0x000000, 0.5)
        .setStrokeStyle(2, 0xffd54f, 0.9),
    );
    const tabs: Array<[string, string, number]> = [['total', '总榜', 0], ['daily', '日榜', 75], ['weekly', '周榜', 150]];
    for (const [type, label, x] of tabs) {
      const active = this.boardType === type;
      const tab = this.add.text(x + 37, 0, label, {
        ...FONT, color: active ? '#ffe082' : '#cccccc', fontSize: '18px',
        stroke: '#000000', strokeThickness: active ? 3 : 2,
      }).setOrigin(0.5, 0).setInteractive({ useHandCursor: true });
      tab.on('pointerdown', () => {
        this.boardType = type;
        net.send(MsgTypes.MsgGetLeaderboard, { game_id: currentGame().id, type, offset: 0, limit: 10 } satisfies GetLeaderboardPayload);
      });
      this.boardContainer.add(tab);
    }
  }

  private renderLeaderboard(p: LeaderboardResultPayload) {
    this.boardContainer.each((obj: Phaser.GameObjects.GameObject) => {
      if (obj instanceof Phaser.GameObjects.Text) obj.destroy();
    });
    const add = (x: number, y: number, text: string, style?: Partial<Phaser.Types.GameObjects.Text.TextStyle>) => {
      const t = this.add.text(x, y, text, { ...FONT, fontSize: '15px', strokeThickness: 2, ...style });
      this.boardContainer.add(t);
      return t;
    };
    p.entries.forEach((e, i) => {
      const rankColor = e.rank <= 3 ? '#ffd54f' : '#ffffff';
      add(10, 40 + i * 32, `${e.rank}`, { color: rankColor, fontSize: '17px' });
      add(50, 40 + i * 32, e.player_name.length > 8 ? e.player_name.slice(0, 8) + '…' : e.player_name, { color: '#ffffff' });
      add(200, 40 + i * 32, `${e.score}分`, { color: '#ffe082' });
      add(295, 40 + i * 32, `${e.wins}胜`, { color: '#81d4fa', fontSize: '14px' });
    });
    if (p.entries.length === 0) {
      add(120, 50, '暂无数据', { color: '#aaaaaa' });
    }
  }

  private toast(msg: string) {
    const t = this.add.text(640, 500, msg, { ...FONT, fontSize: '24px', color: '#ff8a80', backgroundColor: '#000000cc', padding: { x: 12, y: 6 } })
      .setOrigin(0.5).setDepth(100);
    this.tweens.add({ targets: t, alpha: 0, delay: 2000, duration: 500, onComplete: () => t.destroy() });
  }

  // --- 消息订阅 ---

  private subscribe() {
    const on = (t: Parameters<typeof net.on>[0], h: Parameters<typeof net.on>[1]) => this.unsubscribers.push(net.on(t, h));

    on(MsgTypes.MsgConnected, (p: ConnectedPayload) => {
      store.playerID = p.player_id;
      store.playerName = p.player_name;
      store.score = p.score ?? 0;
      store.rank = p.rank ?? 0;
      this.refreshNameBar();
    });
    on(MsgTypes.MsgOnlineCount, (p: OnlineCountPayload) => {
      store.onlineCount = p.count;
      this.onlineText.setText(`在线 ${p.count}`);
    });
    on(MsgTypes.MsgRoomListResult, (p: RoomListResultPayload) => this.renderRoomList(p));
    on(MsgTypes.MsgLeaderboardResult, (p: LeaderboardResultPayload) => {
      if (p.type === this.boardType) this.renderLeaderboard(p);
    });
    on(MsgTypes.MsgStatsResult, (p: StatsResultPayload) => {
      // 同步权威积分/排名到名字栏（含结算后服务端主动推送的统计）
      if (p.player_id !== store.playerID) return;
      store.score = p.score;
      store.rank = p.rank;
      this.refreshNameBar();
      this.toast(`战绩：${p.total_games}局 ${p.wins}胜 胜率${(p.win_rate * 100).toFixed(0)}% 积分${p.score} 排名${p.rank}`);
    });
    on(MsgTypes.MsgError, (p: ErrorPayload) => {
      // ErrRoomExists + creator_is_self：旧房间是自己创建的 → 弹窗确认后单请求关闭并新建
      if (p.code === 2006 && p.data) {
        const data = p.data as RoomExistsData;
        if (data.creator_is_self) {
          this.showCloseOldRoomDialog(data);
          return;
        }
      }
      this.toast(p.message);
    });

    on(MsgTypes.MsgReconnected, (p: ReconnectedPayload) => {
      store.playerID = p.player_id;
      store.playerName = p.player_name;
      store.score = p.score ?? 0;
      store.rank = p.rank ?? 0;
      this.refreshNameBar();
      if (p.game_state) {
        store.roomCode = p.room_code ?? '';
        restoreCurrentGameState(p.game_state);
        // ── 流程状态机：重连恢复进入对局 ──
        setFlowState(FlowState.GAME);
        this.scene.start(currentGameSceneKey());
      } else if (p.room_code) {
        store.roomCode = p.room_code;
        setFlowState(FlowState.ROOM);
        this.toast(`已恢复房间 ${p.room_code} 座位，请重新输入房号进入`);
      }
    });

    // 昵称修改成功
    on(MsgTypes.MsgProfileUpdated, (p: ProfileUpdatedPayload) => {
      if (p.player_id === store.playerID) {
        store.playerName = p.player_name;
        this.refreshNameBar();
        this.toast(`昵称已更新为 ${p.player_name}`);
      }
    });

    const enterRoom = (p: RoomJoinedPayload | RoomCreatedPayload) => {
      // ── 游戏 ID 校验：忽略非当前游戏的房间消息 ──
      // 切换游戏后缓冲消息重放（或网络延迟到达）可能携带旧游戏的房间消息，
      // 在大厅中处理会导致误进入非当前游戏的房间
      if (p.game_id && p.game_id !== currentGame().id) {
        console.warn(`[LobbyScene] 忽略非当前游戏的房间消息 (msg=${p.game_id}, current=${currentGame().id})`);
        return;
      }
      const code = p.room_code;
      store.roomCode = code;
      // 记录房主：自建房间的创建人即自己；加入/匹配房间取服务端下发值

      store.roomCreatorID = ('creator_id' in p ? p.creator_id : store.playerID) ?? '';
      if ('players' in p) {
        store.players = p.players;
      } else {
        store.players = [p.player];
      }
      // 防御性修正：确保真人不被错误标记为机器人
      for (const pl of store.players) {
        if (pl.id === store.playerID && pl.is_bot) {
          console.warn('[enterRoom] 当前玩家被错误标记为机器人，本地修正');
          pl.is_bot = false;
        }
      }
      // 清除待重建的建房参数
      this.pendingRecreate = undefined;
      // ── 流程状态机：进入房间 ──
      setFlowState(FlowState.ROOM);
      this.scene.start('Room');
    };
    on(MsgTypes.MsgRoomCreated, (p: RoomCreatedPayload) => enterRoom(p));
    on(MsgTypes.MsgRoomJoined, (p: RoomJoinedPayload) => enterRoom(p));

    // ── 关键修复：MsgGameStart 回到 REPLAYABLE 后，LobbyScene 也需正确处理 ──
    // 覆盖场景：MsgGameStart 在 MsgRoomJoined 场景切换间隙到达，
    // 被 REPLAYABLE 缓冲后重放到 LobbyScene。此时直接进入 GameScene，
    // 跳过 RoomScene（人机练习无需等待房间 UI）。
    on(MsgTypes.MsgGameStart, (p: GameStartPayload) => {
      // ── 递增对局代次，过滤过期重放 ──
      // 仅在当前未进入对局流程时递增，避免 LobbyScene 与 RoomScene 同时活跃时双重递增
      if (!isInGameFlow()) store.gameStartEpoch++;
      const epoch = store.gameStartEpoch;

      for (const pl of p.players) {
        if (pl.id === store.playerID && pl.is_bot) {
          console.warn('[LobbyScene/game_start] 当前玩家被错误标记为机器人，本地修正');
          pl.is_bot = false;
        }
      }
      store.players = p.players;
      store.afkPlayers.clear();
      store.gameEnded = false;
      store.inGame = true; // 标记对局活跃，防止 GameScene shutdown 误发 MsgLeaveGame
      // 注意：不在此处设 stateReceived=true，MsgGameStart 仅含玩家列表，
      // 完整对局状态需等 MsgGameState（由 GameScene.create 主动拉取）到达后才算完整
      // ── 流程状态机：进入对局 ──
      setFlowState(FlowState.GAME);
      console.warn(`[LobbyScene] MsgGameStart 到达，直接进入对局场景 (epoch=${epoch})`);
      // 关键修复：scene.start('Game') 只停止调用方（Lobby），不会停止已被 MsgRoomJoined
      // 排队/启动的 Room，会造成 Room 与 Game 同时活跃、Room 覆盖 Game。
      // 因此启动对局场景前显式停止 Room（若已启动）。
      this.scene.stop('Room');
      this.scene.start(currentGameSceneKey());
    });

    on(MsgTypes.MsgMaintenancePush, (p: MaintenancePayload) => {
      store.maintenance = p.maintenance;
      if (p.maintenance) this.showMaintenanceOverlay();
      else this.hideMaintenanceOverlay();
    });
    on(MsgTypes.MsgMaintenancePull, (p: MaintenancePayload) => {
      store.maintenance = p.maintenance;
    });
  }

  private refreshNameBar() {
    this.nameText.setText(`👤 ${store.playerName}  🎯${store.score}  🏆${store.rank || '—'}  ✎`);
  }

  private showMaintenanceOverlay() {
    if (this.maintenanceOverlay) return;
    this.maintenanceOverlay = this.add.container(640, 360).setDepth(300);
    const bg = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.7);
    const title = this.add.text(0, -40, '🛠️ 服务器维护中', {
      ...FONT, fontSize: '40px', color: '#ffd54f',
    }).setOrigin(0.5);
    const msg = this.add.text(0, 20, '对局、匹配、建房暂时关闭\n请稍后再试', {
      ...FONT, fontSize: '22px', color: '#eeeeee', align: 'center',
    }).setOrigin(0.5);
    this.maintenanceOverlay.add([bg, title, msg]);
  }

  private hideMaintenanceOverlay() {
    this.maintenanceOverlay?.destroy();
    this.maintenanceOverlay = undefined;
  }
}
