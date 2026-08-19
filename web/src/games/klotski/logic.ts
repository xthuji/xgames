/**
 * 华容道纯逻辑
 *
 * 规则参考 our-mini-games/mini-games 的 klotski（无 LICENSE，仅参考规则与布局，自行重写）：
 * 4×5 棋盘，曹操（2×2）在各棋子间移动到棋盘底部中央出口即胜；
 * 棋子编码：1 曹操 / 2 横将（2×1）/ 3 竖将（1×2）/ 4 卒（1×1）。
 */

export const COLS = 4;
export const ROWS = 5;

export type PieceKind = 'caocao' | 'horizontal' | 'vertical' | 'pawn';

export interface Piece {
  id: number;
  kind: PieceKind;
  /** 名称（曹操 / 关羽 / 张飞 / 赵云 / 黄忠 / 马超 / 卒一~四） */
  label: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Layout {
  name: string;
  grid: number[][];
}

export const LAYOUTS: Layout[] = [
  {
    name: '横刀立马',
    grid: [
      [3, 1, 1, 3],
      [3, 1, 1, 3],
      [3, 2, 2, 3],
      [3, 4, 4, 3],
      [4, 0, 0, 4],
    ],
  },
  {
    name: '指挥若定',
    grid: [
      [3, 1, 1, 3],
      [3, 1, 1, 3],
      [4, 2, 2, 4],
      [3, 4, 4, 3],
      [3, 0, 0, 3],
    ],
  },
  {
    name: '齐头并进',
    grid: [
      [3, 1, 1, 3],
      [3, 1, 1, 3],
      [4, 4, 4, 4],
      [3, 2, 2, 3],
      [3, 0, 0, 3],
    ],
  },
  {
    name: '兵分三路',
    grid: [
      [4, 1, 1, 4],
      [3, 1, 1, 3],
      [3, 2, 2, 3],
      [3, 4, 4, 3],
      [3, 0, 0, 3],
    ],
  },
  {
    name: '一路进军',
    grid: [
      [3, 1, 1, 4],
      [3, 1, 1, 4],
      [3, 3, 3, 4],
      [3, 3, 3, 4],
      [0, 2, 2, 0],
    ],
  },
  {
    name: '水泄不通',
    grid: [
      [3, 1, 1, 4],
      [3, 1, 1, 4],
      [2, 2, 2, 2],
      [2, 2, 2, 2],
      [4, 0, 0, 4],
    ],
  },
];

const GENERALS = ['关羽', '张飞', '赵云', '黄忠', '马超'];
const PAWN_NAMES = ['卒一', '卒二', '卒三', '卒四'];

/** 由布局编码生成棋子列表（横将第一位为关羽，其余按序命名） */
export function createPieces(layout: Layout): Piece[] {
  const grid = layout.grid.map((row) => [...row]);
  const pieces: Piece[] = [];
  let id = 0;
  let general = 0;
  let pawn = 0;
  for (let y = 0; y < ROWS; y++) {
    for (let x = 0; x < COLS; x++) {
      const v = grid[y][x];
      if (v === 0 || v === -1) continue;
      if (v === 1) {
        pieces.push({ id: id++, kind: 'caocao', label: '曹操', x, y, w: 2, h: 2 });
        grid[y][x + 1] = grid[y + 1][x] = grid[y + 1][x + 1] = -1;
      } else if (v === 2) {
        pieces.push({ id: id++, kind: 'horizontal', label: GENERALS[general++ % GENERALS.length], x, y, w: 2, h: 1 });
        grid[y][x + 1] = -1;
      } else if (v === 3) {
        pieces.push({ id: id++, kind: 'vertical', label: GENERALS[general++ % GENERALS.length], x, y, w: 1, h: 2 });
        grid[y + 1][x] = -1;
      } else {
        pieces.push({ id: id++, kind: 'pawn', label: PAWN_NAMES[pawn++ % PAWN_NAMES.length], x, y, w: 1, h: 1 });
      }
    }
  }
  return pieces;
}

function overlap(p: Piece, q: Piece): boolean {
  return p.x < q.x + q.w && q.x < p.x + p.w && p.y < q.y + q.h && q.y < p.y + p.h;
}

/** 棋子能否向 (dx,dy) 移动一格（不出界且不与其他棋子重叠） */
export function canMove(pieces: Piece[], piece: Piece, dx: number, dy: number): boolean {
  const moved: Piece = { ...piece, x: piece.x + dx, y: piece.y + dy };
  if (moved.x < 0 || moved.y < 0 || moved.x + moved.w > COLS || moved.y + moved.h > ROWS) return false;
  return !pieces.some((p) => p.id !== piece.id && overlap(moved, p));
}

export function movePiece(piece: Piece, dx: number, dy: number): void {
  piece.x += dx;
  piece.y += dy;
}

/** 曹操抵达底部中央出口即胜 */
export function isWin(pieces: Piece[]): boolean {
  const c = pieces.find((p) => p.kind === 'caocao');
  return !!c && c.x === 1 && c.y === ROWS - 2;
}

// ── 提示：广度优先搜索最短解 ───────────────────────────

/** 四方向位移（与场景的方向按钮、方向键一致，一次一格） */
const DIRS: Array<[number, number]> = [[0, -1], [-1, 0], [0, 1], [1, 0]];

/** 同类棋子可互换，按尺寸分四个桶做归一化键（'2x2' 必为曹操） */
const KIND_ORDER = ['2x2', '2x1', '1x2', '1x1'];

/** 出口坐标：底边中央两列 */
const EXIT_X = 1;
const EXIT_Y = ROWS - 2;

export interface KlotskiHint {
  /**
   * 推荐移动的棋子下标（与传入 pieces 对齐）；dead 为 true 时无意义。
   */
  piece: number;
  dx: number;
  dy: number;
  /** 棋子名称（曹操 / 关羽 / 卒一 …） */
  label: string;
  /** 按最优解走到出口还需几步（含本步）；null = 预算内未算到出口，给的是局部启发 */
  rest: number | null;
  /** true = 已穷尽全部可达局面，曹操无论如何到不了出口 */
  dead: boolean;
}

type Dims = Array<[number, number]>;

/** 位置序列 [x0,y0,x1,y1,…]（棋子下标固定，BFS 中只变坐标） */
type Pos = number[];

function toPos(ps: Piece[]): Pos {
  const pos: Pos = [];
  for (const p of ps) pos.push(p.x, p.y);
  return pos;
}

function dimsOf(ps: Piece[]): Dims {
  return ps.map((p) => [p.w, p.h] as [number, number]);
}

/** 每个棋子属于哪个尺寸桶 */
function bucketsOf(dims: Dims): number[] {
  return dims.map(([w, h]) => KIND_ORDER.indexOf(`${w}x${h}`));
}

/**
 * 归一化状态键：同类棋子位置集合排序后拼接。
 * 同类棋子只影响几何、不影响规则，合并同型可把状态空间缩小一个量级。
 */
function keyOf(pos: Pos, buckets: number[]): string {
  const groups: string[][] = [[], [], [], []];
  for (let i = 0; i < buckets.length; i++) {
    groups[buckets[i]].push(`${pos[2 * i]},${pos[2 * i + 1]}`);
  }
  return groups.map((g) => g.sort().join(';')).join('|');
}

/** 棋盘占用图：下标 y*COLS+x，1 = 被任意棋子占用 */
function occupancy(pos: Pos, dims: Dims): Uint8Array {
  const occ = new Uint8Array(ROWS * COLS);
  for (let i = 0; i < dims.length; i++) {
    const [w, h] = dims[i];
    for (let dy = 0; dy < h; dy++) {
      for (let dx = 0; dx < w; dx++) occ[(pos[2 * i + 1] + dy) * COLS + pos[2 * i] + dx] = 1;
    }
  }
  return occ;
}

/** 棋子能否向 (dx,dy) 滑一格：只看移动方向上新进入的一排格（自身原址无需重检） */
function slideable(occ: Uint8Array, x: number, y: number, w: number, h: number, dx: number, dy: number): boolean {
  const nx = x + dx;
  const ny = y + dy;
  if (nx < 0 || ny < 0 || nx + w > COLS || ny + h > ROWS) return false;
  if (dx !== 0) {
    const col = dx > 0 ? nx + w - 1 : nx;
    for (let j = 0; j < h; j++) if (occ[(ny + j) * COLS + col]) return false;
    return true;
  }
  const row = dy > 0 ? ny + h - 1 : ny;
  for (let i = 0; i < w; i++) if (occ[row * COLS + nx + i]) return false;
  return true;
}

function caocaoReached(pos: Pos, caocao: number): boolean {
  return pos[2 * caocao] === EXIT_X && pos[2 * caocao + 1] === EXIT_Y;
}

/** 曹操到出口的曼哈顿距离 */
function exitDistance(pos: Pos, caocao: number): number {
  return Math.abs(pos[2 * caocao] - EXIT_X) + Math.abs(pos[2 * caocao + 1] - EXIT_Y);
}

/** 卡在曹操正下方通道上、需要让路的棋子数 */
function blockingExit(pos: Pos, dims: Dims, caocao: number): number {
  const top = pos[2 * caocao + 1] + dims[caocao][1];
  let n = 0;
  for (let i = 0; i < dims.length; i++) {
    if (i === caocao) continue;
    const [w, h] = dims[i];
    const x = pos[2 * i];
    const y = pos[2 * i + 1];
    if (x < EXIT_X + 2 && x + w > EXIT_X && y + h > top) n++;
  }
  return n;
}

/** 超预算时的局部启发：曹操尽量贴近出口 + 尽量少堵通道 */
function greedyHint(pieces: Piece[], pos: Pos, dims: Dims, caocao: number): KlotskiHint {
  const occ = occupancy(pos, dims);
  const base = exitDistance(pos, caocao) * 10 + blockingExit(pos, dims, caocao) * 3;
  let best: KlotskiHint | null = null;
  let bestScore = -Infinity;
  for (let i = 0; i < dims.length; i++) {
    const [w, h] = dims[i];
    for (const [dx, dy] of DIRS) {
      if (!slideable(occ, pos[2 * i], pos[2 * i + 1], w, h, dx, dy)) continue;
      const next = [...pos];
      next[2 * i] += dx;
      next[2 * i + 1] += dy;
      const score = base - (exitDistance(next, caocao) * 10 + blockingExit(next, dims, caocao) * 3);
      if (score > bestScore) {
        bestScore = score;
        best = { piece: i, dx, dy, label: pieces[i].label, rest: null, dead: false };
      }
    }
  }
  return best ?? { piece: caocao, dx: 0, dy: 0, label: pieces[caocao].label, rest: null, dead: true };
}

/**
 * 提示：BFS 求从当前局面到出口的最短解，返回第一步。
 *
 * 最优解需遍历整个可达状态空间（如「横刀立马」约 6.5 万局面），因此设时间
 * 预算：超预算退化为局部启发建议（rest=null）。开局卡住时多按几次提示会因
 * 局面变浅而更快算出完整解。
 */
export function findHint(pieces: Piece[], budgetMs = 1200): KlotskiHint | null {
  const dims = dimsOf(pieces);
  const buckets = bucketsOf(dims);
  const caocao = pieces.findIndex((p) => p.kind === 'caocao');
  const start = toPos(pieces);
  if (caocao < 0 || caocaoReached(start, caocao)) return null;

  const deadline = Date.now() + budgetMs;
  const seen = new Set<string>([keyOf(start, buckets)]);
  interface Node { pos: Pos; first: KlotskiHint | null; depth: number }
  const queue: Node[] = [{ pos: start, first: null, depth: 0 }];

  for (let head = 0; head < queue.length; head++) {
    if ((head & 255) === 0 && Date.now() > deadline) return greedyHint(pieces, start, dims, caocao);
    const cur = queue[head];
    const occ = occupancy(cur.pos, dims);
    for (let i = 0; i < dims.length; i++) {
      const [w, h] = dims[i];
      for (const [dx, dy] of DIRS) {
        if (!slideable(occ, cur.pos[2 * i], cur.pos[2 * i + 1], w, h, dx, dy)) continue;
        const next = [...cur.pos];
        next[2 * i] += dx;
        next[2 * i + 1] += dy;
        const first: KlotskiHint = cur.first ?? { piece: i, dx, dy, label: pieces[i].label, rest: null, dead: false };
        if (caocaoReached(next, caocao)) {
          return { ...first, rest: cur.depth + 1 };
        }
        const key = keyOf(next, buckets);
        if (seen.has(key)) continue;
        seen.add(key);
        queue.push({ pos: next, first, depth: cur.depth + 1 });
      }
    }
  }
  // 队列穷尽：可达局面里不存在出口，确无解
  return { piece: -1, dx: 0, dy: 0, label: '', rest: null, dead: true };
}
