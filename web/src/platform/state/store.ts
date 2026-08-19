import { CardInfo, GameStateDTO, PlayerInfo } from '../protocol';

/**
 * Store 客户端状态镜像：大厅/房间/对局 DTO（技术设计 9.1）。
 * 场景直接读写，重连时用 game_state 整体覆盖还原。
 */
export const store = {
  playerID: '',
  playerName: '',
  score: 0,
  rank: 0,
  onlineCount: 0,
  maintenance: false,

  /** 当前选择的游戏（外壳侧边栏驱动，与 platform/registry 共用同一 localStorage key） */
  currentGameID: localStorage.getItem('xgames_current_game') ?? 'ddz',
  setCurrentGame(id: string) {
    this.currentGameID = id;
    localStorage.setItem('xgames_current_game', id);
  },

  /** 音效开关（localStorage 持久化） */
  muted: localStorage.getItem('ddz_muted') === '1',
  toggleMuted() {
    this.muted = !this.muted;
    localStorage.setItem('ddz_muted', this.muted ? '1' : '0');
  },

  // 房间
  roomCode: '',

  roomCreatorID: '', // 房间创建人（仅创建人可添加机器人）
  players: [] as PlayerInfo[],

  // 对局
  inGame: false,
  gameEnded: false, // 对局已结束但尚未返回房间（阻止残留 MsgAfkChanged 重新标记用户）
  stateReceived: false, // 是否已接收过游戏状态消息（用于状态同步检测）
  gameStartEpoch: 0, // 对局代次计数器：每次新对局开始时递增，用于过滤过期 MsgGameStart
  phase: '', // bidding / doubling / playing
  hand: [] as CardInfo[],
  bottomCards: [] as CardInfo[],
  landlordID: '',
  currentTurn: '',
  lastPlayed: [] as CardInfo[],
  lastPlayerID: '',
  mustPlay: false,
  canBeat: true,
  multiplier: 1,
  // 本轮对局各玩家积分（sessionScores）
  sessionScores: new Map<string, number>(),
  // 挂机中的玩家（仅挂机/离线时机器人才代为出牌）
  afkPlayers: new Set<string>(),

  reset() {
    this.roomCode = '';
    this.roomCreatorID = '';
    this.players = [];
    this.resetGame();
  },

  resetGame() {
    this.inGame = false;
    this.gameEnded = false;
    this.stateReceived = false;
    // 注意：gameStartEpoch 不在此处重置，它仅在 MsgGameStart 到达时递增
    this.phase = '';
    this.hand = [];
    this.bottomCards = [];
    this.landlordID = '';
    this.currentTurn = '';
    this.lastPlayed = [];
    this.lastPlayerID = '';
    this.mustPlay = false;
    this.canBeat = true;
    this.multiplier = 1;
    this.sessionScores.clear();
    this.afkPlayers.clear();
  },

  /** 重连恢复：整桌还原 */
  restoreGameState(gs: GameStateDTO) {
    this.inGame = true;
    this.gameEnded = false;
    this.stateReceived = true; // 已收到服务端权威对局状态
    this.phase = gs.phase;
    // 防御性修正：确保真人不被错误标记为机器人
    for (const p of gs.players) {
      if (p.id === this.playerID && p.is_bot) {
        console.warn('[restoreGameState] 当前玩家被错误标记为机器人，本地修正');
        p.is_bot = false;
      }
    }
    this.players = gs.players;
    this.hand = gs.hand;
    this.bottomCards = gs.bottom_cards;
    this.currentTurn = gs.current_turn;
    this.lastPlayed = gs.last_played ?? [];
    this.lastPlayerID = gs.last_player_id ?? '';
    this.mustPlay = gs.must_play;
    this.canBeat = gs.can_beat;
    this.afkPlayers.clear();
    for (const p of gs.players) {
      if (p.is_landlord) this.landlordID = p.id;
      if (p.afk) this.afkPlayers.add(p.id);
    }
  },

  seatOf(id: string): number {
    return this.players.find((p) => p.id === id)?.seat ?? -1;
  },

  player(id: string): PlayerInfo | undefined {
    return this.players.find((p) => p.id === id);
  },
};
