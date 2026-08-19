/**
 * 俄罗斯方块核心逻辑（纯函数，无框架依赖）
 *
 * 规则与常量参考开源项目 our-mini-games/mini-games
 * （packages/tetris 的 lib/utils.ts 与 config/{game.config,tetrisCoordinates}.ts），
 * 重写为适配 Phaser 场景的纯函数版本：旋转用坐标表换型实现（替代其 640 行
 * nextType 状态机），棋盘格记录方块类型以便彩色渲染（其实现为 boolean 单色）。
 */

export const COLS = 10;
export const ROWS = 20;

export type Piece = 'I' | 'O' | 'T' | 'S' | 'Z' | 'L' | 'J';

/** 棋盘：[y][x]，y=0 为顶；null 为空，否则为占据该格的方块类型 */
export type Board = Array<Array<Piece | null>>;

/** 7 种方块 × 4 旋转态的局部坐标（4×4 / 3×3 网格，数据同参考项目坐标表） */
export const SHAPES: Record<Piece, Array<Array<[number, number]>>> = {
  I: [
    [[0, 1], [1, 1], [2, 1], [3, 1]],
    [[2, 0], [2, 1], [2, 2], [2, 3]],
    [[0, 2], [1, 2], [2, 2], [3, 2]],
    [[1, 0], [1, 1], [1, 2], [1, 3]],
  ],
  J: [
    [[1, 0], [1, 1], [1, 2], [2, 2]],
    [[0, 1], [1, 1], [2, 1], [0, 2]],
    [[0, 0], [1, 0], [1, 1], [1, 2]],
    [[2, 0], [0, 1], [1, 1], [2, 1]],
  ],
  L: [
    [[1, 0], [1, 1], [0, 2], [1, 2]],
    [[0, 0], [0, 1], [1, 1], [2, 1]],
    [[1, 0], [2, 0], [1, 1], [1, 2]],
    [[0, 1], [1, 1], [2, 1], [2, 2]],
  ],
  S: [
    [[1, 0], [2, 0], [0, 1], [1, 1]],
    [[1, 0], [1, 1], [2, 1], [2, 2]],
    [[1, 1], [2, 1], [0, 2], [1, 2]],
    [[0, 0], [0, 1], [1, 1], [1, 2]],
  ],
  Z: [
    [[0, 0], [1, 0], [1, 1], [2, 1]],
    [[2, 0], [1, 1], [2, 1], [1, 2]],
    [[0, 1], [1, 1], [1, 2], [2, 2]],
    [[1, 0], [0, 1], [1, 1], [0, 2]],
  ],
  T: [
    [[0, 1], [1, 1], [2, 1], [1, 2]],
    [[1, 0], [0, 1], [1, 1], [1, 2]],
    [[1, 0], [0, 1], [1, 1], [2, 1]],
    [[1, 0], [1, 1], [2, 1], [1, 2]],
  ],
  O: [
    [[0, 0], [1, 0], [0, 1], [1, 1]],
    [[0, 0], [1, 0], [0, 1], [1, 1]],
    [[0, 0], [1, 0], [0, 1], [1, 1]],
    [[0, 0], [1, 0], [0, 1], [1, 1]],
  ],
};

/** 各方块渲染颜色（标准配色） */
export const PIECE_COLORS: Record<Piece, number> = {
  I: 0x00bcd4,
  O: 0xffd54f,
  T: 0xba68c8,
  S: 0x81c784,
  Z: 0xe57373,
  L: 0xffb74d,
  J: 0x64b5f6,
};

export interface ActivePiece {
  piece: Piece;
  /** 旋转态 0-3 */
  rot: number;
  /** 左上角在棋盘上的偏移（y 可为负，表示尚未完全入场） */
  x: number;
  y: number;
}

/** 消行得分（1-4 行） */
export const LINE_SCORES = [0, 10, 30, 60, 100];
export const MAX_LEVEL = 20;
/** 每级的下落间隔（ms）：1 级 800，每级 -35 */
export function levelSpeed(level: number): number {
  return 800 - (level - 1) * 35;
}

export function createBoard(): Board {
  return Array.from({ length: ROWS }, () => Array<Piece | null>(COLS).fill(null));
}

const PIECES: Piece[] = ['I', 'O', 'T', 'S', 'Z', 'L', 'J'];

export function randomPiece(): Piece {
  return PIECES[Math.floor(Math.random() * PIECES.length)];
}

/** 绝对坐标（棋盘格） */
export function cells(p: ActivePiece): Array<[number, number]> {
  return SHAPES[p.piece][p.rot].map(([lx, ly]) => [p.x + lx, p.y + ly]);
}

/** 坐标合法性：横向/底部边界 + 与已落方块不重叠（允许 y<0 的入场区） */
export function isLegal(coords: Array<[number, number]>, board: Board): boolean {
  return coords.every(([x, y]) => {
    if (x < 0 || x >= COLS || y >= ROWS) return false;
    if (y >= 0 && board[y][x]) return false;
    return true;
  });
}

/** 幽灵投影：当前方块落到底部时的 y */
export function ghostY(board: Board, p: ActivePiece): number {
  let y = p.y;
  while (isLegal(cells({ ...p, y: y + 1 }), board)) y++;
  return y;
}

/** 把方块固定进棋盘 */
export function lock(board: Board, p: ActivePiece): void {
  for (const [x, y] of cells(p)) {
    if (y >= 0) board[y][x] = p.piece;
  }
}

/** 顶出判定：固定后仍有格子在棋盘顶部之上 → 游戏结束 */
export function isTopOut(p: ActivePiece): boolean {
  return cells(p).some(([, y]) => y < 0);
}

/** 已满行的索引 */
export function fullRows(board: Board): number[] {
  const rows: number[] = [];
  board.forEach((row, y) => {
    if (row.every(Boolean)) rows.push(y);
  });
  return rows;
}

/** 移除满行，上方整体下移 */
export function clearRows(board: Board, rows: number[]): void {
  for (const y of rows.sort((a, b) => a - b)) {
    board.splice(y, 1);
    board.unshift(Array<Piece | null>(COLS).fill(null));
  }
}

// ── 提示：落点启发评估 ───────────────────────────────

/** 启发权重（业界常用经验值：消行为正收益，堆高/不平/挖洞为负收益） */
const W_LINES = 0.76;
const W_HEIGHT = 0.51;
const W_ROW_TRANS = 0.356;
const W_HOLES = 0.37;
const W_WELL = 0.03;

export interface TetrisHint {
  /** 目标旋转态 */
  rot: number;
  /** 需要按几次↑（只能正向旋转） */
  rotates: number;
  /** 落定后方块占据的格子（供高亮） */
  landing: Array<[number, number]>;
  /** 落点所在列（方块左边缘，0 起算） */
  col: number;
  /** 该落点可消除的行数 */
  cleared: number;
}

/** 行过渡数：每行在空/满之间切换的次数（含两侧墙壁），越多越碎 */
function rowTransitions(board: Board): number {
  let n = 0;
  for (let y = 0; y < ROWS; y++) {
    let prev = 0;
    for (let x = 0; x <= COLS; x++) {
      const v = x === COLS ? 0 : board[y][x] ? 1 : 0;
      if (v !== prev) n++;
      prev = v;
    }
  }
  return n;
}

/** 评估一个硬降落点：分越高越好 */
function scoreDrop(board: Board, p: ActivePiece): { score: number; cleared: number } {
  const copy: Board = board.map((row) => [...row]);
  lock(copy, p);
  const rows = fullRows(copy);
  clearRows(copy, rows);

  const heights: number[] = [];
  let holes = 0;
  for (let x = 0; x < COLS; x++) {
    let h = 0;
    for (let y = 0; y < ROWS; y++) {
      if (copy[y][x]) {
        h = ROWS - y;
        break;
      }
    }
    heights.push(h);
    let seen = false;
    for (let y = 0; y < ROWS; y++) {
      if (copy[y][x]) seen = true;
      else if (seen) holes++;
    }
  }
  let wells = 0;
  for (let x = 0; x < COLS; x++) {
    const left = x === 0 ? ROWS : heights[x - 1];
    const right = x === COLS - 1 ? ROWS : heights[x + 1];
    const depth = Math.min(left, right) - heights[x];
    if (depth > 0) wells += depth;
  }
  const aggHeight = heights.reduce((a, b) => a + b, 0);
  const cleared = rows.length;
  const score = W_LINES * cleared
    - W_HEIGHT * aggHeight
    - W_ROW_TRANS * rowTransitions(copy)
    - W_HOLES * holes
    + W_WELL * wells;
  return { score, cleared };
}

/**
 * 提示：枚举 4 个旋转态 × 全部可顶入的列，取启发分最高的硬降落点。
 *
 * 只看“落下去会怎样”，不考虑横向移动路径（无隔空移块，同一行空位下都能滑到）。
 */
export function findHint(board: Board, piece: Piece, curRot: number): TetrisHint | null {
  let best: TetrisHint | null = null;
  let bestScore = -Infinity;
  for (let rot = 0; rot < 4; rot++) {
    for (let x = -3; x < COLS; x++) {
      const start: ActivePiece = { piece, rot, x, y: -3 };
      if (!isLegal(cells(start), board)) continue;
      const y = ghostY(board, start);
      if (y < 0) continue; // 落点仍在入场区以上：堆到顶了，不推荐
      const placed: ActivePiece = { ...start, y };
      const { score, cleared } = scoreDrop(board, placed);
      if (score > bestScore) {
        bestScore = score;
        const landing = cells(placed);
        best = {
          rot,
          rotates: (rot - curRot + 4) % 4,
          landing,
          col: Math.min(...landing.map(([cx]) => cx)),
          cleared,
        };
      }
    }
  }
  return best;
}
