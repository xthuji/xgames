import type { MjGameStateDTO, MjPlayerHandDTO } from '../../platform/protocol';

export const MJ_NUM_TYPES = 34; // 万(9) + 筒(9) + 条(9) + 字牌(7)
export const MJ_HAND_SIZE = 13;
export const MJ_HONOR_START = 27; // 字牌起始索引

export interface MjMeld {
  type: number; // 0=碰 1=杠 3=暗杠
  tile: number;
  from: number;
}

/** 面子有多少张牌 */
export function meldTileCount(type: number): number {
  return (type === 1 || type === 3) ? 4 : 3;
}

export interface MjPlayerState {
  playerId: string;
  playerName: string;
  seat: number;
  isBot: boolean;
  online: boolean;
  afk: boolean;
  handCount: number;
  hand: number[];
  melds: MjMeld[];
  isDealer: boolean;
  score: number;
}

export const mjState = {
  phase: '',
  players: [] as MjPlayerState[],
  currentTurn: '',
  myIndex: -1,
  dealer: 0,
  wallRemain: 0,
  meldNumber: 0,
  discardPools: [] as number[][],
  selectedTile: -1,
  actionAvail: false,
  canPong: false,
  canKong: false,
  canWin: false,
  actionTile: -1,
  selfDrew: false,

  reset() {
    this.phase = '';
    this.players = [];
    this.currentTurn = '';
    this.myIndex = -1;
    this.dealer = 0;
    this.wallRemain = 0;
    this.meldNumber = 0;
    this.discardPools = [];
    this.selectedTile = -1;
    this.actionAvail = false;
    this.canPong = false;
    this.canKong = false;
    this.canWin = false;
    this.actionTile = -1;
    this.selfDrew = false;
  },

  restore(dto: MjGameStateDTO) {
    this.phase = dto.phase;
    this.currentTurn = dto.current_turn;
    this.myIndex = dto.my_index;
    this.dealer = dto.dealer;
    this.wallRemain = dto.wall_remain;
    this.meldNumber = dto.meld_number;
    this.discardPools = dto.discard_pools || [];
    this.players = dto.players.map((p: MjPlayerHandDTO) => ({
      playerId: p.player_id,
      playerName: p.player_name,
      seat: p.seat,
      isBot: p.is_bot,
      online: p.online,
      afk: p.afk,
      handCount: p.hand_count,
      hand: p.hand || [],
      melds: (p.melds || []).map(m => ({ type: m.type, tile: m.tile, from: m.from })),
      isDealer: p.is_dealer,
      score: p.score ?? 0,
    }));
    this.selectedTile = -1;
    this.actionAvail = false;
    const me = this.myIndex >= 0 ? this.players[this.myIndex] : undefined;
    this.selfDrew = !!me && me.hand.length === MJ_HAND_SIZE + 1;
  },
};

/** 花色：0=万 1=筒 2=条 3=字牌 */
export function tileSuit(t: number): number {
  if (t >= MJ_HONOR_START) return 3;
  return Math.floor(t / 9);
}

/** 点数：数牌 1-9（返回 0-8），字牌 0-6 */
export function tileValue(t: number): number {
  if (t >= MJ_HONOR_START) return t - MJ_HONOR_START;
  return t % 9;
}

const HONOR_NAMES = ['东', '南', '西', '北', '中', '发', '白'];
const HONOR_SPEECH = ['东风', '南风', '西风', '北风', '红中', '发财', '白板'];
const SUIT_NAMES = ['万', '筒', '条'];

export function tileName(t: number): string {
  if (t >= MJ_HONOR_START) {
    return HONOR_NAMES[t - MJ_HONOR_START];
  }
  return `${tileValue(t) + 1}${SUIT_NAMES[tileSuit(t)]}`;
}

/** 语音播报用牌名：字牌用全称（东风/南风/西风/北风/红中/发财/白板） */
export function tileSpeech(t: number): string {
  if (t >= MJ_HONOR_START) {
    return HONOR_SPEECH[t - MJ_HONOR_START];
  }
  return tileName(t);
}

/** 牌面颜色：万红、筒蓝、条绿、字牌黑（中红、发绿、白灰） */
export function tileColor(t: number): number {
  const s = tileSuit(t);
  if (s === 0) return 0xe74c3c; // 万
  if (s === 1) return 0x3498db; // 筒
  if (s === 2) return 0x2ecc71; // 条
  // 字牌：东/南/西/北=0x2c3e50(深蓝), 中=0xe74c3c(红), 发=0x27ae60(绿), 白=0x7f8c8d(灰)
  const hv = t - MJ_HONOR_START;
  if (hv <= 3) return 0x2c3e50; // 东南西北
  if (hv === 4) return 0xe74c3c; // 中
  if (hv === 5) return 0x27ae60; // 发
  return 0x7f8c8d; // 白
}

/** 是否字牌 */
export function isHonor(t: number): boolean {
  return t >= MJ_HONOR_START;
}
