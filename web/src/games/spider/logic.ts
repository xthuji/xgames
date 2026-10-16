/**
 * 蜘蛛纸牌核心逻辑（纯函数，无框架依赖）
 *
 * 规则与常量参考开源项目 our-mini-games/mini-games
 * （packages/spider-solitaire 的 lib/helper.ts 与 lib/validator.ts），
 * 重写为适配 Phaser 场景的纯函数版本。
 *
 * 规则：2 副牌共 104 张（无王）；10 列，前 4 列 6 张、后 6 列 5 张，各列顶牌翻开；
 * 备牌 5 沓每沓 10 张。同花色递减连续段可整体移动；放置只看目标列顶牌大 1 点（花色不限）；
 * 列尾凑齐同花色 K→A 自动收走；发牌要求所有列非空；收齐 8 组获胜。
 */

/** 花色：0=♠ 1=♥ 2=♣ 3=♦（与斗地主 CardInfo.suit 一致） */
export type Suit = 0 | 1 | 2 | 3;

export interface SpiderCard {
  suit: Suit;
  /** 1=A ... 13=K */
  rank: number;
  open: boolean;
}

export interface SpiderLayout {
  /** 10 个牌列（索引小 = 底牌） */
  tableau: SpiderCard[][];
  /** 备牌：最多 5 沓，每沓 10 张 */
  stock: SpiderCard[][];
}

/** 难度模式：使用的花色种数（1/2/4） */
export type SpiderMode = 1 | 2 | 4;

/** Fisher–Yates 洗牌（原地） */
export function shuffle<T>(arr: T[]): T[] {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

/** 发一副新牌：104 张洗匀后按规则布局 */
export function deal(mode: SpiderMode = 1): SpiderLayout {
  const base: Suit[] =
    mode === 4 ? [0, 1, 2, 3] : mode === 2 ? [0, 1] : [0];
  // 凑齐 8 组花色（共 104 张）
  const suits: Suit[] = [];
  while (suits.length < 8) suits.push(...base);

  const deck = shuffle(
    suits.flatMap((suit) =>
      Array.from({ length: 13 }, (_, i) => ({ suit, rank: i + 1, open: false })),
    ),
  );

  const tableau: SpiderCard[][] = [];
  for (let i = 0; i < 10; i++) {
    tableau.push(deck.splice(0, i < 4 ? 6 : 5));
  }
  const stock: SpiderCard[][] = [];
  for (let i = 0; i < 5; i++) {
    stock.push(deck.splice(0, 10));
  }
  tableau.forEach((col) => {
    col[col.length - 1].open = true;
  });
  return { tableau, stock };
}

/**
 * 从 index 开始的尾段能否整体移动：
 * 全部翻开 + 同花色 + 严格递减 1
 */
export function canPickUp(col: SpiderCard[], index: number): boolean {
  if (index < 0 || index >= col.length) return false;
  const head = col[index];
  if (!head.open) return false;
  for (let i = index + 1; i < col.length; i++) {
    const c = col[i];
    if (!c.open || c.suit !== head.suit || c.rank !== head.rank - (i - index)) {
      return false;
    }
  }
  return true;
}

/** 能否把一张牌放到目标列：空列任意；非空列要求顶牌点数恰好 +1（花色不限） */
export function canDrop(col: SpiderCard[], card: SpiderCard): boolean {
  if (col.length === 0) return true;
  return col[col.length - 1].rank === card.rank + 1;
}

/** 列尾是否存在同花色 K→A 完整 13 张 */
export function canCollect(col: SpiderCard[]): boolean {
  if (col.length < 13) return false;
  const tail = col.slice(-13);
  const suit = tail[0].suit;
  return tail.every((c, i) => c.open && c.suit === suit && c.rank === 13 - i);
}

/** 收走列尾 13 张（调用方需先确认 canCollect） */
export function collectRun(col: SpiderCard[]): SpiderCard[] {
  return col.splice(col.length - 13, 13);
}

/** 翻开列顶牌（若存在且未翻开） */
export function openTop(col: SpiderCard[]): void {
  const top = col[col.length - 1];
  if (top && !top.open) top.open = true;
}

/** 能否发牌：备牌非空 且 所有列非空（标准规则） */
export function canDeal(layout: SpiderLayout): boolean {
  return layout.stock.length > 0 && layout.tableau.every((col) => col.length > 0);
}

/** 发一轮：备牌弹出一沓，每列追加一张明牌；不满足发牌条件返回 false */
export function dealRound(layout: SpiderLayout): boolean {
  if (!canDeal(layout)) return false;
  const pile = layout.stock.pop()!;
  for (const col of layout.tableau) {
    const card = pile.pop()!;
    card.open = true;
    col.push(card);
  }
  return true;
}

// ── 提示：只推荐真正推进局面的移牌 + 浅层前瞻 ────────────────
/**
 * 蜘蛛纸牌的唯一实质进展只有四类：凑成一组 K→A、翻开一张暗牌、**新建立**一条同花色
 * 接续、整列搬空腾出缓冲列。旧版给每个合法移牌都算一个恒正的基础分，
 * 于是把「同点数异花色换个位置」这类原地踏步的横移也当成提示返回——对解局毫无意义。
 * 更隐蔽的一类是：一段牌已经接在同花色点数+1 的牌上，再把它搬到另一张同花色+1 上，
 * 看似「同花色接续」实则是把已有的链接换个位置，同样是毫无意义的平移，必须排除。
 *
 * 新版：
 * 1. pass 1 只从「有实质进展」的移牌里选最优（同花色接续必须是新建立的才算）；
 * 2. 无有意义移牌时按序退化：能发牌就建议发牌 → 空列挡住发牌就建议填列 →
 *    否则做 1 步前瞻，找一步「看似横移但套用后能解锁下一步实质进展」的铺垫着；
 * 3. 都找不到就诚实告知「已无有意义移动」，绝不再硬凑无用横移。
 */

/** 单步移牌的价值权重：收组 >> 翻暗牌 >> 腾空列 >> 同花色接续 >> 搬动张数 */
const VALUE_COLLECT = 100000;
const VALUE_OPEN = 5000;
const VALUE_FREED = 1200;
const VALUE_SUIT = 600;
/** 平局偏向：优先搬更长的同花色完整段（张数本身不是进展，故权重很小） */
const VALUE_CARD = 10;
/** 把牌塞进空列会占用唯一的缓冲区，小罚（腾空整列本身有价值时不罚） */
const PENALTY_EMPTY = 400;
/** 前瞻展开的步数上限，兜住最坏情况的响应时延 */
const MAX_LOOKAHEAD = 800;

/** 提示类型：直接移牌 / 建议发牌 / 填列解锁发牌 / 前瞻铺垫 / 无路可走 */
export type SpiderHintKind = 'move' | 'deal' | 'fill' | 'setup' | 'none';

export interface SpiderHint {
  kind: SpiderHintKind;
  /** 源列下标（deal/none 为 -1） */
  from: number;
  /** 起搬位置（该下标起的整段一起搬） */
  index: number;
  /** 目标列下标 */
  to: number;
  /** 搬动张数 */
  count: number;
  /** 搬完能在目标列尾凑出同花色 K→A（下一帧自动收走） */
  collect: boolean;
  /** 搬完能翻开源列新露出的暗牌 */
  opens: boolean;
  /** setup：这一步为后续解锁哪种进展（文案用） */
  enables?: 'collect' | 'open' | 'suit';
}

/** 单个候选移牌及其进展分类与打分 */
interface MoveCandidate {
  from: number;
  index: number;
  to: number;
  count: number;
  collect: boolean;
  opens: boolean;
  /** 落到同花色顶牌上，接续/合并成更长的同花色段 */
  consolidate: boolean;
  /** 整列搬走使源列清空，腾出一个缓冲区 */
  freesColumn: boolean;
  score: number;
}

/** 有实质进展才算「有意义」：收组 / 翻暗牌 / 同花色接续 / 腾空列，四选一 */
function isMeaningful(m: MoveCandidate): boolean {
  return m.collect || m.opens || m.consolidate || m.freesColumn;
}

/** 在给定 tableau 上枚举所有合法移牌并打分（不改动入参） */
function enumerateMoves(t: SpiderCard[][]): MoveCandidate[] {
  const moves: MoveCandidate[] = [];
  for (let from = 0; from < t.length; from++) {
    const col = t[from];
    for (let index = 0; index < col.length; index++) {
      if (!canPickUp(col, index)) continue;
      const head = col[index];
      const count = col.length - index;
      for (let to = 0; to < t.length; to++) {
        if (to === from) continue;
        const dst = t[to];
        if (!canDrop(dst, head)) continue;
        if (dst.length === 0 && index === 0) continue; // 整列搬进空列：原地踏步

        const opens = index > 0 && !col[index - 1].open;
        const collect = canCollect([...dst, ...col.slice(index)]);
        const freesColumn = index === 0;
        // 段首在源列是否已经坐在「同花色点数+1」的牌上：若是，把它搬到另一张同花色+1
        // 只是给已有的同花色链接换个位置（原地横移），不算进展。
        const below = index > 0 ? col[index - 1] : null;
        const alreadyLinked = !!below && below.open && below.suit === head.suit && below.rank === head.rank + 1;
        // 只有「新建立」的同花色接续才算 consolidate；已接好的再平移被排除。
        const consolidate = dst.length > 0 && dst[dst.length - 1].suit === head.suit && !alreadyLinked;

        let score = count * VALUE_CARD;
        if (collect) score += VALUE_COLLECT;
        if (opens) score += VALUE_OPEN;
        if (freesColumn) score += VALUE_FREED;
        if (consolidate) score += VALUE_SUIT;
        if (dst.length === 0 && !freesColumn) score -= PENALTY_EMPTY;

        moves.push({ from, index, to, count, collect, opens, consolidate, freesColumn, score });
      }
    }
  }
  return moves;
}

/** 在深拷贝上套用一次移牌（翻开源列新顶牌），供前瞻用，绝不污染真实局面 */
function applyMoveClone(t: SpiderCard[][], m: MoveCandidate): SpiderCard[][] {
  const next = t.map((col) => col.map((c) => ({ ...c })));
  const moving = next[m.from].splice(m.index);
  next[m.to].push(...moving);
  const src = next[m.from];
  if (src.length > 0 && !src[src.length - 1].open) src[src.length - 1].open = true;
  return next;
}

function moveHint(kind: SpiderHintKind, m: MoveCandidate, enables?: 'collect' | 'open' | 'suit'): SpiderHint {
  return {
    kind, from: m.from, index: m.index, to: m.to, count: m.count,
    collect: m.collect, opens: m.opens, enables,
  };
}

function idleHint(kind: 'deal' | 'none'): SpiderHint {
  return { kind, from: -1, index: -1, to: -1, count: 0, collect: false, opens: false };
}

/**
 * 提示：返回下一步建议。始终给出结构化结果，由场景按 kind 渲染。
 *
 * 只推荐能翻开暗牌 / 收组 / 接续同花色 / 腾空列的动作，或为其中一种铺路的
 * 前瞻着法；对毫无意义的横移一律不推荐。
 */
export function findHint(layout: SpiderLayout): SpiderHint {
  const t = layout.tableau;
  const all = enumerateMoves(t);

  // pass 1：直接有实质进展的最优一步
  let best: MoveCandidate | null = null;
  for (const m of all) {
    if (!isMeaningful(m)) continue;
    if (!best || m.score > best.score) best = m;
  }
  if (best) return moveHint('move', best);

  // pass 2：无有意义移牌——按序退化
  if (canDeal(layout)) return idleHint('deal');

  // 有备牌但被空列挡住发牌：把某段移进空列以解锁发牌，优先顺带翻开暗牌、其次搬最长段
  const emptyCol = t.findIndex((col) => col.length === 0);
  if (layout.stock.length > 0 && emptyCol >= 0) {
    let fill: MoveCandidate | null = null;
    for (const m of all) {
      if (m.to !== emptyCol) continue;
      if (!fill || (m.opens && !fill.opens) || (m.opens === fill.opens && m.count > fill.count)) {
        fill = m;
      }
    }
    if (fill) return moveHint('fill', fill);
  }

  // 1 步前瞻：找一步横移 M1，使套用后解锁当前拿不到的实质进展 M2（排除原样搬回的逆着法）
  let setup: { cand: MoveCandidate; enables: 'collect' | 'open' | 'suit'; gain: number } | null = null;
  for (let i = 0; i < all.length && i < MAX_LOOKAHEAD; i++) {
    const m1 = all[i];
    const follow = enumerateMoves(applyMoveClone(t, m1));
    for (const m2 of follow) {
      if (!isMeaningful(m2)) continue;
      if (m2.from === m1.to && m2.to === m1.from && m2.count === m1.count) continue; // 逆操作
      if (!setup || m2.score > setup.gain) {
        setup = {
          cand: m1,
          enables: m2.collect ? 'collect' : m2.opens ? 'open' : 'suit',
          gain: m2.score,
        };
      }
    }
  }
  if (setup) return moveHint('setup', setup.cand, setup.enables);

  return idleHint('none');
}
