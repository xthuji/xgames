/**
 * 连连看纯逻辑
 *
 * 规则参考 our-mini-games/mini-games 的 link-game（无 LICENSE，仅参考规则，自行重写）：
 * 相同图案的两个方块，若能用不超过两个拐角、不穿过其他方块的折线相连，即可消除。
 * 本实现把棋盘外圈视为空（可从边界外绕行），用「直线 / 一拐 / 两拐」三级判定。
 */

export const ROWS = 8;
export const COLS = 14;
/** 图案种类数（棋盘总格数必须为偶数对） */
export const KINDS = 12;

export interface Point {
  x: number;
  y: number;
}

/** board[y][x]：0 = 空，1..KINDS = 图案序号 */
export type Board = number[][];

export function createBoard(): Board {
  const total = ROWS * COLS;
  const seqs: number[] = [];
  for (let i = 0; i < total / 2; i++) {
    const seq = (i % KINDS) + 1;
    seqs.push(seq, seq);
  }
  shuffle(seqs);
  const board: Board = [];
  for (let y = 0; y < ROWS; y++) {
    board.push(seqs.slice(y * COLS, (y + 1) * COLS));
  }
  // 保证开局有解
  if (!findAnyPair(board)) reshuffle(board);
  return board;
}

export function shuffle<T>(arr: T[]): void {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
}

export function remaining(board: Board): number {
  let n = 0;
  for (const row of board) for (const v of row) if (v !== 0) n++;
  return n;
}

/** 空判定：棋盘外一圈恒为空（允许沿边界外侧连线） */
function isEmpty(board: Board, x: number, y: number): boolean {
  if (x < 0 || x >= COLS || y < 0 || y >= ROWS) return x >= -1 && x <= COLS && y >= -1 && y <= ROWS;
  return board[y][x] === 0;
}

/** p、q 同行/同列且中间全空（不含端点） */
function lineClear(board: Board, p: Point, q: Point): boolean {
  if (p.x === q.x) {
    const [a, b] = p.y < q.y ? [p.y, q.y] : [q.y, p.y];
    for (let y = a + 1; y < b; y++) {
      if (!isEmpty(board, p.x, y)) return false;
    }
    return true;
  }
  if (p.y === q.y) {
    const [a, b] = p.x < q.x ? [p.x, q.x] : [q.x, p.x];
    for (let x = a + 1; x < b; x++) {
      if (!isEmpty(board, x, p.y)) return false;
    }
    return true;
  }
  return false;
}

/**
 * 寻找连接路径（含两端点），不存在返回 null。
 * 依次尝试：相邻/同线 → 一个拐角 → 两个拐角。
 */
export function findPath(board: Board, a: Point, b: Point): Point[] | null {
  if (a.x === b.x && a.y === b.y) return null;
  const va = board[a.y]?.[a.x];
  const vb = board[b.y]?.[b.x];
  if (!va || va !== vb) return null;

  // 1. 直线（含相邻）
  if ((a.x === b.x || a.y === b.y) && lineClear(board, a, b)) return [a, b];

  // 2. 一个拐角：两个候选交点
  for (const c of [{ x: a.x, y: b.y }, { x: b.x, y: a.y }]) {
    if (isEmpty(board, c.x, c.y) && lineClear(board, a, c) && lineClear(board, c, b)) {
      return [a, c, b];
    }
  }

  // 3. 两个拐角：沿 a 的四条射线逐格推进，每个空位尝试一次「一拐连 b」
  const dirs = [[1, 0], [-1, 0], [0, 1], [0, -1]];
  for (const [dx, dy] of dirs) {
    let x = a.x + dx;
    let y = a.y + dy;
    while (isEmpty(board, x, y)) {
      const c: Point = { x, y };
      for (const e of [{ x: c.x, y: b.y }, { x: b.x, y: c.y }]) {
        if ((e.x === c.x || e.y === c.y) && isEmpty(board, e.x, e.y)
          && lineClear(board, c, e) && lineClear(board, e, b) && lineClear(board, a, c)) {
          return [a, c, e, b];
        }
      }
      x += dx;
      y += dy;
    }
  }
  return null;
}

/** 找到任意一对可消除的方块（提示/死局检测用） */
export function findAnyPair(board: Board): [Point, Point] | null {
  const bySeq = new Map<number, Point[]>();
  for (let y = 0; y < ROWS; y++) {
    for (let x = 0; x < COLS; x++) {
      const v = board[y][x];
      if (v === 0) continue;
      const list = bySeq.get(v) ?? [];
      list.push({ x, y });
      bySeq.set(v, list);
    }
  }
  for (const list of bySeq.values()) {
    for (let i = 0; i < list.length; i++) {
      for (let j = i + 1; j < list.length; j++) {
        if (findPath(board, list[i], list[j])) return [list[i], list[j]];
      }
    }
  }
  return null;
}

/** 剩余方块原地重排（保证重排后有解） */
export function reshuffle(board: Board): void {
  for (let attempt = 0; attempt < 50; attempt++) {
    const seqs: number[] = [];
    const cells: Point[] = [];
    for (let y = 0; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        if (board[y][x] !== 0) {
          seqs.push(board[y][x]);
          cells.push({ x, y });
        }
      }
    }
    if (seqs.length === 0) return;
    shuffle(seqs);
    cells.forEach((p, i) => {
      board[p.y][p.x] = seqs[i];
    });
    if (findAnyPair(board)) return;
  }
}
