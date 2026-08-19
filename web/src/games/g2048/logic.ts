/**
 * 2048 纯逻辑
 *
 * 规则参考 our-mini-games/mini-games 的 2048（无 LICENSE，仅参考规则，自行重写）：
 * 4×4 棋盘，滑动后同值相邻块合并一次，合并得分等于新块数值；无路可走判负。
 */

export const SIZE = 4;

/** board[y][x]，0 表示空 */
export type Board = number[][];

export type Direction = 'left' | 'right' | 'up' | 'down';

export function createBoard(): Board {
  return Array.from({ length: SIZE }, () => Array<number>(SIZE).fill(0));
}

/** 随机放一个新块（90% 出 2，10% 出 4）；无空格返回 false */
export function spawn(board: Board): boolean {
  const empty: Array<[number, number]> = [];
  for (let y = 0; y < SIZE; y++) {
    for (let x = 0; x < SIZE; x++) {
      if (board[y][x] === 0) empty.push([x, y]);
    }
  }
  if (empty.length === 0) return false;
  const [x, y] = empty[Math.floor(Math.random() * empty.length)];
  board[y][x] = Math.random() < 0.9 ? 2 : 4;
  return true;
}

/**
 * 向某方向滑动合并。
 * 返回是否发生移动（决定是否生成新块）与本步得分。
 */
export function move(board: Board, dir: Direction): { moved: boolean; gained: number } {
  let moved = false;
  let gained = 0;

  const line = (i: number): number[] => {
    const arr: number[] = [];
    for (let j = 0; j < SIZE; j++) {
      const [x, y] = cellAt(dir, i, j);
      arr.push(board[y][x]);
    }
    return arr;
  };

  for (let i = 0; i < SIZE; i++) {
    const src = line(i);
    const { merged, score } = mergeLine(src);
    gained += score;
    for (let j = 0; j < SIZE; j++) {
      const [x, y] = cellAt(dir, i, j);
      if (board[y][x] !== merged[j]) moved = true;
      board[y][x] = merged[j];
    }
  }
  return { moved, gained };
}

/**
 * 单行向左合并：先压缩去零，再相邻同值合并（每块一步内只合并一次），右侧补零。
 * cellAt 把四个方向都归一成「行首 = 滑动目标侧」，因此只需实现向左合并。
 */
function mergeLine(row: number[]): { merged: number[]; score: number } {
  const vals = row.filter((v) => v !== 0);
  const merged: number[] = [];
  let score = 0;
  for (let i = 0; i < vals.length; i++) {
    if (i + 1 < vals.length && vals[i] === vals[i + 1]) {
      merged.push(vals[i] * 2);
      score += vals[i] * 2;
      i++;
    } else {
      merged.push(vals[i]);
    }
  }
  while (merged.length < SIZE) merged.push(0);
  return { merged, score };
}

/** 第 i 条线（行/列）第 j 格在棋盘上的坐标，行首始终贴着滑动目标边 */
function cellAt(dir: Direction, i: number, j: number): [number, number] {
  switch (dir) {
    case 'left':
      return [j, i];
    case 'right':
      return [SIZE - 1 - j, i];
    case 'up':
      return [i, j];
    case 'down':
      return [i, SIZE - 1 - j];
  }
}

/** 是否还有合法移动（有空格或相邻同值） */
export function canMove(board: Board): boolean {
  for (let y = 0; y < SIZE; y++) {
    for (let x = 0; x < SIZE; x++) {
      if (board[y][x] === 0) return true;
      if (x + 1 < SIZE && board[y][x] === board[y][x + 1]) return true;
      if (y + 1 < SIZE && board[y][x] === board[y + 1][x]) return true;
    }
  }
  return false;
}

/** 是否出现过指定数值（达成 2048 判胜） */
export function reached(board: Board, value: number): boolean {
  return board.some((row) => row.some((v) => v >= value));
}

// ── 提示：期望搜索 ───────────────────────────────────────────

/** 全部滑动方向 */
export const DIRECTIONS: Direction[] = ['left', 'right', 'up', 'down'];

/** 新块生成概率（与 spawn 一致），机会节点按此加权 */
const SPAWN_WEIGHTS: Array<[number, number]> = [[2, 0.9], [4, 0.1]];

/**
 * 搜索深度（玩家步数）。3 步在 4×4 上最坏约万次评估、点一次提示几十毫秒，
 * 足以看出「这一步会不会把棋盘锁死」，又不会让界面卡一下。
 */
const SEARCH_DEPTH = 3;

export interface BoardHint {
  dir: Direction;
  /** 本步会发生合并的方块坐标（原棋盘坐标系，供高亮） */
  merging: Array<[number, number]>;
  /** 本步直接得分（0 表示只是整理棋盘） */
  gained: number;
}

/** 蛇形权重：一角最大、沿蛇形次序递减，鼓励大数字蹲角不乱跑 */
const SNAKE = [
  [15, 14, 13, 12],
  [8, 9, 10, 11],
  [7, 6, 5, 4],
  [0, 1, 2, 3],
];

/** 蛇形矩阵的四个朝向（角落可以位于任意一角） */
const WEIGHT_SETS = (() => {
  const flipX = (m: number[][]): number[][] => m.map((row) => [...row].reverse());
  const flipY = (m: number[][]): number[][] => [...m].reverse().map((row) => [...row]);
  return [SNAKE, flipX(SNAKE), flipY(SNAKE), flipY(flipX(SNAKE))].map((m) => m.map((row) => [...row]));
})();

function clone(board: Board): Board {
  return board.map((row) => [...row]);
}

/** 局面评估：蛇形加权（大数字成序蹲角）+ 空格奖励（保留可动性） */
function evaluate(board: Board): number {
  const logged = board.map((row) => row.map((v) => (v <= 1 ? 0 : Math.round(Math.log2(v)))));
  let order = -Infinity;
  let empties = 0;
  for (const w of WEIGHT_SETS) {
    let acc = 0;
    for (let y = 0; y < SIZE; y++) {
      for (let x = 0; x < SIZE; x++) acc += logged[y][x] * w[y][x];
    }
    if (acc > order) order = acc;
  }
  for (const row of board) {
    for (const v of row) if (v === 0) empties++;
  }
  return order + empties * 26;
}

/** 我方（max）节点：四个方向取最优后继；无路可走按死局重罚 */
function bestValue(board: Board, depth: number): number {
  if (depth <= 0) return evaluate(board);
  let best = -Infinity;
  for (const dir of DIRECTIONS) {
    const next = clone(board);
    if (!move(next, dir).moved) continue;
    const v = expectValue(next, depth - 1);
    if (v > best) best = v;
  }
  return best === -Infinity ? evaluate(board) - 1e6 : best;
}

/** 机会节点：每个空格按 90%/10% 生成 2/4，取概率加权平均（原地试放后还原） */
function expectValue(board: Board, depth: number): number {
  const empty: Array<[number, number]> = [];
  for (let y = 0; y < SIZE; y++) {
    for (let x = 0; x < SIZE; x++) if (board[y][x] === 0) empty.push([x, y]);
  }
  if (empty.length === 0) return bestValue(board, depth);
  let sum = 0;
  for (const [x, y] of empty) {
    for (const [v, p] of SPAWN_WEIGHTS) {
      board[y][x] = v;
      sum += p * bestValue(board, depth);
      board[y][x] = 0;
    }
  }
  return sum / empty.length;
}

/** 沿该方向滑会后发生合并的方块坐标（在原棋盘上取，供高亮） */
function mergingCells(board: Board, dir: Direction): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  for (let i = 0; i < SIZE; i++) {
    const line: Array<{ pos: [number, number]; v: number }> = [];
    for (let j = 0; j < SIZE; j++) {
      const [x, y] = cellAt(dir, i, j);
      if (board[y][x] !== 0) line.push({ pos: [x, y], v: board[y][x] });
    }
    for (let k = 0; k + 1 < line.length; ) {
      if (line[k].v === line[k + 1].v) {
        out.push(line[k].pos, line[k + 1].pos);
        k += 2;
      } else {
        k += 1;
      }
    }
  }
  return out;
}

/** 提示：期望搜索选出的最优滑动方向（已无路可走返回 null） */
export function findHint(board: Board): BoardHint | null {
  let best: BoardHint | null = null;
  let bestVal = -Infinity;
  for (const dir of DIRECTIONS) {
    const next = clone(board);
    const { moved, gained } = move(next, dir);
    if (!moved) continue;
    const v = expectValue(next, SEARCH_DEPTH - 1);
    if (v > bestVal) {
      bestVal = v;
      best = { dir, gained, merging: mergingCells(board, dir) };
    }
  }
  return best;
}
