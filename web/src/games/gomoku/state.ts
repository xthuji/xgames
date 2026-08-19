import { store } from '../../platform/state/store';
import type { GkGameStateDTO } from '../../platform/protocol';

/**
 * 五子棋客户端对局状态（与斗地主 store 的对局字段同位，
 * 但棋盘/执子颜色等属五子棋私有，故独立模块维护）。
 * 重连/同步拉取时用服务端 DTO 整体覆盖还原。
 */
export const gkState = {
  /** 15×15 行优先：0=空 1=黑 2=白 */
  board: [] as number[],
  /** 我执子颜色（1=黑 2=白；0=未知） */
  myColor: 0,
  /** 最近一手（-1 = 尚未落子） */
  lastRow: -1,
  lastCol: -1,
  /** 下一手序号 */
  moveNumber: 1,
  /** playing / ended */
  phase: '',

  reset() {
    this.board = [];
    this.myColor = 0;
    this.lastRow = -1;
    this.lastCol = -1;
    this.moveNumber = 1;
    this.phase = '';
  },

  /** 重连恢复：整桌还原（注册表 restoreState 入口） */
  restore(dto: GkGameStateDTO) {
    this.phase = dto.phase;
    this.board = dto.board ?? [];
    this.myColor = dto.my_color;
    this.lastRow = dto.last_row ?? -1;
    this.lastCol = dto.last_col ?? -1;
    this.moveNumber = dto.move_number ?? 1;

    store.inGame = true;
    store.gameEnded = false;
    store.stateReceived = true;
    store.players = dto.players;
    store.currentTurn = dto.current_turn;
    store.afkPlayers.clear();
    for (const p of dto.players) {
      if (p.afk) store.afkPlayers.add(p.id);
    }
  },
};

/** 棋盘尺寸（与服务端 rule.Size 一致） */
export const GK_SIZE = 15;
export const GK_EMPTY = 0;
export const GK_BLACK = 1;
export const GK_WHITE = 2;
