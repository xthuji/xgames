/**
 * 贪吃蛇纯逻辑
 *
 * 规则参考 our-mini-games/mini-games 的 greedy-snake（无 LICENSE，仅参考规则与常量，自行重写）：
 * 40×30 网格、撞墙从对面穿出、撞自己结束、吃食物增长。
 */

export const COLS = 40;
export const ROWS = 30;

export interface Point {
  x: number;
  y: number;
}

export type Direction = 'up' | 'right' | 'down' | 'left';

const OPPOSITE: Record<Direction, Direction> = {
  up: 'down',
  down: 'up',
  left: 'right',
  right: 'left',
};

export interface SnakeState {
  /** 下标 0 为蛇头 */
  snake: Point[];
  dir: Direction;
  food: Point;
  score: number;
  over: boolean;
}

export function createGame(): SnakeState {
  const cx = Math.floor(COLS / 2);
  const cy = Math.floor(ROWS / 2);
  const snake: Point[] = [
    { x: cx, y: cy },
    { x: cx - 1, y: cy },
    { x: cx - 2, y: cy },
  ];
  return { snake, dir: 'right', food: spawnFood(snake), score: 0, over: false };
}

export function spawnFood(snake: Point[]): Point {
  const free: Point[] = [];
  for (let y = 0; y < ROWS; y++) {
    for (let x = 0; x < COLS; x++) {
      if (!snake.some((p) => p.x === x && p.y === y)) free.push({ x, y });
    }
  }
  if (free.length === 0) return { x: -1, y: -1 };
  return free[Math.floor(Math.random() * free.length)];
}

/** 转向（禁止 180° 掉头） */
export function turn(s: SnakeState, dir: Direction): void {
  if (s.over || dir === OPPOSITE[s.dir]) return;
  s.dir = dir;
}

/**
 * 推进一帧：撞墙从对面穿出；撞到自己（蛇尾除外，尾巴本帧会让位）结束；
 * 吃到食物增长并计分。
 */
export function step(s: SnakeState): void {
  if (s.over) return;
  const head = s.snake[0];
  const next: Point = { x: head.x, y: head.y };
  switch (s.dir) {
    case 'up':
      next.y -= 1;
      break;
    case 'down':
      next.y += 1;
      break;
    case 'left':
      next.x -= 1;
      break;
    case 'right':
      next.x += 1;
      break;
  }
  if (next.x < 0) next.x = COLS - 1;
  else if (next.x >= COLS) next.x = 0;
  if (next.y < 0) next.y = ROWS - 1;
  else if (next.y >= ROWS) next.y = 0;

  for (let i = 0; i < s.snake.length - 1; i++) {
    const p = s.snake[i];
    if (p.x === next.x && p.y === next.y) {
      s.over = true;
      return;
    }
  }

  s.snake.unshift(next);
  if (next.x === s.food.x && next.y === s.food.y) {
    s.score += 10;
    s.food = spawnFood(s.snake);
    if (s.food.x < 0) s.over = true; // 填满整个棋盘
  } else {
    s.snake.pop();
  }
}

/** 下落间隔随分数加快：150ms 起步，每 50 分快 10ms，最快 60ms */
export function speed(score: number): number {
  return Math.max(60, 150 - Math.floor(score / 50) * 10);
}

// ── 提示：BFS 寻路 ───────────────────────────────────────────

const ALL_DIRS: Direction[] = ['up', 'down', 'left', 'right'];

/** 四方向位移 */
const VECTORS: Record<Direction, Point> = {
  up: { x: 0, y: -1 },
  down: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
};

export interface SnakeHint {
  /** 推荐的下一步转向 */
  dir: Direction;
  /** 从蛇头下一格起、到食物的路径格子（不可达时仅含推荐的首格） */
  path: Point[];
  /** 食物是否可达；false 表示本次给的是「先保住命」兼容建议 */
  reachable: boolean;
}

/** 穿墙：越界的格从对面穿出（与 step 一致） */
function wrap(p: Point): Point {
  return { x: (p.x + COLS) % COLS, y: (p.y + ROWS) % ROWS };
}

/**
 * 障碍集合：蛇身占用的格（一维索引）。
 * 蛇尾除外——它在本帧会让位，与 step 的碰撞判定保持一致。
 */
function blockedCells(s: SnakeState): Set<number> {
  const set = new Set<number>();
  for (let i = 0; i < s.snake.length - 1; i++) set.add(s.snake[i].y * COLS + s.snake[i].x);
  return set;
}

/** BFS 求蛇头到食物的最短路径，返回不含起点的格子序列；不可达返回 null */
function pathToFood(s: SnakeState): Point[] | null {
  const blocked = blockedCells(s);
  const start = s.snake[0];
  const startKey = start.y * COLS + start.x;
  const seen = new Set<number>([startKey]);
  const prev = new Map<number, number>();
  const queue: Point[] = [start];
  for (let head = 0; head < queue.length; head++) {
    const cur = queue[head];
    for (const dir of ALL_DIRS) {
      const next = wrap({ x: cur.x + VECTORS[dir].x, y: cur.y + VECTORS[dir].y });
      const key = next.y * COLS + next.x;
      if (seen.has(key)) continue;
      const isFood = next.x === s.food.x && next.y === s.food.y;
      if (!isFood && blocked.has(key)) continue;
      seen.add(key);
      prev.set(key, cur.y * COLS + cur.x);
      if (!isFood) {
        queue.push(next);
        continue;
      }
      const path: Point[] = [];
      let k = key;
      for (;;) {
        path.unshift({ x: k % COLS, y: Math.floor(k / COLS) });
        const p = prev.get(k);
        if (p === undefined) break;
        k = p;
      }
      path.shift(); // 去掉起点（蛇头自己）
      return path;
    }
  }
  return null;
}

/** 由相邻两格反推方向（含穿墙环绕的相邻） */
function dirBetween(from: Point, to: Point): Direction | null {
  for (const dir of ALL_DIRS) {
    const n = wrap({ x: from.x + VECTORS[dir].x, y: from.y + VECTORS[dir].y });
    if (n.x === to.x && n.y === to.y) return dir;
  }
  return null;
}

/** 食物不可达时的兼容建议：选一个不会立即撞身、且前方空间最开阔的方向 */
function safestDir(s: SnakeState): Direction | null {
  const blocked = blockedCells(s);
  const head = s.snake[0];
  let best: Direction | null = null;
  let bestRoom = -1;
  for (const dir of ALL_DIRS) {
    if (dir === OPPOSITE[s.dir]) continue; // turn 禁止 180° 掉头
    const next = wrap({ x: head.x + VECTORS[dir].x, y: head.y + VECTORS[dir].y });
    if (blocked.has(next.y * COLS + next.x)) continue;
    let room = 0;
    for (const probe of ALL_DIRS) {
      const n = wrap({ x: next.x + VECTORS[probe].x, y: next.y + VECTORS[probe].y });
      if (!blocked.has(n.y * COLS + n.x)) room++;
    }
    if (room > bestRoom) {
      bestRoom = room;
      best = dir;
    }
  }
  return best;
}

/** 提示：通往食物最短路径的首步方向；食物不可达时退化为最开阔方向（已无路可退返回 null） */
export function findHint(s: SnakeState): SnakeHint | null {
  if (s.over || s.food.x < 0) return null;
  const head = s.snake[0];
  const path = pathToFood(s);
  if (path && path.length > 0) {
    const dir = dirBetween(head, path[0]);
    if (dir && dir !== OPPOSITE[s.dir]) return { dir, path, reachable: true };
  }
  const dir = safestDir(s);
  if (!dir) return null;
  return {
    dir,
    path: [wrap({ x: head.x + VECTORS[dir].x, y: head.y + VECTORS[dir].y })],
    reachable: false,
  };
}
