/**
 * 扫雷核心逻辑（纯函数，无框架依赖）
 *
 * 规则与常量参考开源项目 our-mini-games/mini-games
 * （packages/mine-sweeper 的 lib/utils.ts 与 config/index.ts），
 * 重写为适配 Phaser 场景的纯函数版本。
 *
 * 规则：随机布雷；首次点击安全（首点是雷则与非雷格互换，随后才计算邻雷数）；
 * 0 邻雷格翻开时泛洪展开；踩雷判负；全部非雷格翻开（或全部雷被标旗）判胜。
 */

export type CellStatus = 'covered' | 'open' | 'flagged';

export interface Cell {
  mine: boolean;
  /** 邻雷数（首次点击后才计算，0-8） */
  adjacent: number;
  status: CellStatus;
}

export type Board = Cell[][];

export interface Level {
  rows: number;
  cols: number;
  mines: number;
}

/** 难度预设（基础版默认中级，不提供切换 UI） */
export const LEVELS: Level[] = [
  { rows: 9, cols: 9, mines: 10 }, // 简单
  { rows: 16, cols: 16, mines: 40 }, // 中等
  { rows: 16, cols: 30, mines: 99 }, // 困难
];

/** Fisher–Yates 洗牌（原地） */
function shuffle<T>(arr: T[]): T[] {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

/** 生成棋盘：随机布雷（邻雷数延迟到首次点击后计算） */
export function createBoard(level: Level): Board {
  const total = level.rows * level.cols;
  const marks = shuffle(
    Array.from({ length: total }, (_, i) => i < level.mines),
  );
  const board: Board = [];
  for (let r = 0; r < level.rows; r++) {
    board.push(
      Array.from({ length: level.cols }, (_, c) => ({
        mine: marks[r * level.cols + c],
        adjacent: 0,
        status: 'covered' as CellStatus,
      })),
    );
  }
  return board;
}

export function neighbors(board: Board, r: number, c: number): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  for (let dr = -1; dr <= 1; dr++) {
    for (let dc = -1; dc <= 1; dc++) {
      if (dr === 0 && dc === 0) continue;
      const nr = r + dr;
      const nc = c + dc;
      if (nr >= 0 && nr < board.length && nc >= 0 && nc < board[0].length) {
        out.push([nr, nc]);
      }
    }
  }
  return out;
}

/** 首次点击：保证不踩雷（是雷则与非雷格互换），随后计算全盘邻雷数 */
export function firstClick(board: Board, r: number, c: number): void {
  if (board[r][c].mine) {
    outer: for (let i = 0; i < board.length; i++) {
      for (let j = 0; j < board[i].length; j++) {
        if (!board[i][j].mine) {
          board[i][j].mine = true;
          board[r][c].mine = false;
          break outer;
        }
      }
    }
  }
  for (let i = 0; i < board.length; i++) {
    for (let j = 0; j < board[i].length; j++) {
      if (!board[i][j].mine) {
        board[i][j].adjacent = neighbors(board, i, j).filter(([nr, nc]) => board[nr][nc].mine).length;
      }
    }
  }
}

/** 翻开格子（含 0 格泛洪）；返回是否踩雷 */
export function openCell(board: Board, r: number, c: number): boolean {
  const cell = board[r][c];
  if (cell.status !== 'covered') return false;
  cell.status = 'open';
  if (cell.mine) return true;
  if (cell.adjacent === 0) {
    const queue: Array<[number, number]> = [[r, c]];
    while (queue.length > 0) {
      const [cr, cc] = queue.pop()!;
      for (const [nr, nc] of neighbors(board, cr, cc)) {
        const n = board[nr][nc];
        if (n.status !== 'covered' || n.mine) continue;
        n.status = 'open';
        if (n.adjacent === 0) queue.push([nr, nc]);
      }
    }
  }
  return false;
}

/** 标旗切换（仅覆盖态可标）；返回旗数变化（+1 / -1 / 0） */
export function toggleFlag(board: Board, r: number, c: number): number {
  const cell = board[r][c];
  if (cell.status === 'open') return 0;
  if (cell.status === 'flagged') {
    cell.status = 'covered';
    return -1;
  }
  cell.status = 'flagged';
  return 1;
}

export type GameResult = 'playing' | 'won' | 'lost';

/** 胜负判定：踩雷判负；全部非雷格翻开或全部雷被标旗判胜 */
export function checkResult(board: Board, mineCount: number): GameResult {
  let flaggedMines = 0;
  let closedSafe = 0;
  for (const row of board) {
    for (const cell of row) {
      if (cell.mine && cell.status === 'open') return 'lost';
      if (cell.mine && cell.status === 'flagged') flaggedMines++;
      if (!cell.mine && cell.status !== 'open') closedSafe++;
    }
  }
  if (closedSafe === 0 || flaggedMines >= mineCount) return 'won';
  return 'playing';
}

// ── 提示：仅用已公开信息做约束推理 ───────────────────────

/**
 * 提示结果：与玩家可用信息严格对齐，**绝不读 cell.mine，也不读未翻开格的 adjacent**，
 * 否则提示会直接泄底。推理只用已翻开数字、已标旗位置与未翻开格集合。
 */
export interface MineHint {
  /** 推荐翻开的格（确定安全；uncertain 为 true 时仅是风险最低的猜点） */
  safe: Array<[number, number]>;
  /** 推荐标旗的格（由数字必能断定是雷） */
  mines: Array<[number, number]>;
  /** true = 现有数字推不出确定的一格，safe 只是概率最低的猜测 */
  uncertain: boolean;
}

/** 约束：这组未翻开格里恰好有 mines 颗雷 */
interface Constraint {
  cells: number[];
  mines: number;
}

/**
 * 提示：约束传播（含子集归约）推导必安全/必为雷的格；推不动时给风险最低的猜点。
 *
 * 结论的可靠性以“玩家插的旗是对的”为前提（提示与玩家共用同一套认知），因此丢弃
 * 自相矛盾的约束（一个数字周围的旗插得比雷数还多/还少），避免一处标错就把整片格子判成“必安全”。
 */
export function findHint(board: Board): MineHint | null {
  const rows = board.length;
  const cols = board[0].length;
  const idx = (r: number, c: number) => r * cols + c;
  const pos = (i: number): [number, number] => [Math.floor(i / cols), i % cols];

  const safe = new Set<number>();
  const mines = new Set<number>();
  let revealed = 0;
  const pending: Constraint[] = [];

  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const cell = board[r][c];
      if (cell.status !== 'open') continue;
      revealed++;
      if (cell.adjacent === 0) continue;
      let flagged = 0;
      const unknown: number[] = [];
      for (const [nr, nc] of neighbors(board, r, c)) {
        const n = board[nr][nc];
        if (n.status === 'flagged') flagged++;
        else if (n.status === 'covered') unknown.push(idx(nr, nc));
      }
      const need = cell.adjacent - flagged;
      if (unknown.length === 0) continue;
      if (need < 0 || need > unknown.length) continue; // 旗子与数字对不上：这条推理不可信
      if (need === 0) unknown.forEach((i) => safe.add(i));
      else if (need === unknown.length) unknown.forEach((i) => mines.add(i));
      else pending.push({ cells: unknown, mines: need });
    }
  }
  if (revealed === 0) return null; // 一格都没翻开，无任何可推理信息

  // 不动点传播：已知结论代回约束，再用子集归约发掘新结论
  for (let round = 0; round < 10; round++) {
    let moved = false;
    const alive: Constraint[] = [];
    for (const con of pending) {
      const cells: number[] = [];
      let need = con.mines;
      for (const i of con.cells) {
        if (mines.has(i)) need -= 1;
        else if (!safe.has(i)) cells.push(i);
      }
      con.cells = cells;
      con.mines = need;
      if (need < 0 || need > cells.length) continue; // 与已定结论矛盾（多半是旗子标错），丢弃
      if (cells.length === 0) continue;
      if (need === 0) {
        cells.forEach((i) => safe.add(i));
        moved = true;
      } else if (need === cells.length) {
        cells.forEach((i) => mines.add(i));
        moved = true;
      } else {
        alive.push(con);
      }
    }
    // 子集归约：B 的未知格全包含于 A 时，A\B 的雷数 = A.mines - B.mines
    for (const a of alive) {
      const aSet = new Set(a.cells);
      for (const b of alive) {
        if (a === b || b.cells.length >= a.cells.length || b.mines > a.mines) continue;
        if (!b.cells.every((i) => aSet.has(i))) continue;
        const diff = a.cells.filter((i) => !b.cells.includes(i));
        const need = a.mines - b.mines;
        if (need === 0) {
          diff.forEach((i) => safe.add(i));
          moved = true;
        } else if (need === diff.length) {
          diff.forEach((i) => mines.add(i));
          moved = true;
        }
      }
    }
    pending.length = 0;
    pending.push(...alive);
    if (!moved) break;
  }

  mines.forEach((i) => safe.delete(i)); // 理论上不可达：旗子与数字矛盾时以雷为准
  if (safe.size > 0 || mines.size > 0) {
    return {
      safe: [...safe].map(pos),
      mines: [...mines].map(pos),
      uncertain: false,
    };
  }

  // 推不动了：拿出现存信息最少的未翻开格（距已翻开格最远，踩雷概率最低）
  let best: [number, number] | null = null;
  let bestGap = -1;
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const cell = board[r][c];
      if (cell.status !== 'covered') continue;
      let gap = 99;
      for (let i = 0; i < rows; i++) {
        for (let j = 0; j < cols; j++) {
          if (board[i][j].status !== 'open') continue;
          gap = Math.min(gap, Math.max(Math.abs(i - r), Math.abs(j - c)));
        }
      }
      if (gap > bestGap) {
        bestGap = gap;
        best = [r, c];
      }
    }
  }
  if (!best) return null;
  return { safe: [best], mines: [], uncertain: true };
}
