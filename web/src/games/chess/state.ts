import { store } from '../../platform/state/store';
import type { CcGameStateDTO } from '../../platform/protocol';

export const ccState = {
  board: [] as number[],
  myCamp: 0,
  lastFromRow: -1,
  lastFromCol: -1,
  lastToRow: -1,
  lastToCol: -1,
  moveNumber: 1,
  phase: '',
  selectedRow: -1,
  selectedCol: -1,
  legalTargets: [] as Array<{ row: number; col: number }>,
  redCaptures: [] as number[],
  blackCaptures: [] as number[],

  reset() {
    this.board = [];
    this.myCamp = 0;
    this.lastFromRow = -1;
    this.lastFromCol = -1;
    this.lastToRow = -1;
    this.lastToCol = -1;
    this.moveNumber = 1;
    this.phase = '';
    this.selectedRow = -1;
    this.selectedCol = -1;
    this.legalTargets = [];
    this.redCaptures = [];
    this.blackCaptures = [];
  },

  restore(dto: CcGameStateDTO) {
    this.phase = dto.phase;
    this.board = dto.board ?? [];
    this.myCamp = dto.my_camp;
    this.lastFromRow = dto.last_from_row ?? -1;
    this.lastFromCol = dto.last_from_col ?? -1;
    this.lastToRow = dto.last_to_row ?? -1;
    this.lastToCol = dto.last_to_col ?? -1;
    this.moveNumber = dto.move_number ?? 1;
    this.redCaptures = dto.red_captures ?? [];
    this.blackCaptures = dto.black_captures ?? [];
    this.selectedRow = -1;
    this.selectedCol = -1;
    this.legalTargets = [];

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

  clearSelection() {
    this.selectedRow = -1;
    this.selectedCol = -1;
    this.legalTargets = [];
  },
};

export const CC_COLS = 9;
export const CC_ROWS = 10;
export const CC_EMPTY = 0;
