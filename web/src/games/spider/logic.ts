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

// ── 提示：枚举合法移牌并打分 ────────────────────────────

/** 单步移牌的价值权重：收组 >> 翻暗牌 >> 同花色接续 >> 整理牌列 */
const VALUE_COLLECT = 10000;
const VALUE_OPEN = 500;
const VALUE_SUIT = 120;
const VALUE_CARD = 15;
const VALUE_HIDDEN = 3;
/** 把牌塞进空列会占用唯一的缓冲区，小罚；整列搬到空列原地踏步，直接排除 */
const PENALTY_EMPTY = 40;

export interface SpiderHint {
  /** 源列下标 */
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
}

/**
 * 提示：打分最高的移牌方案。
 *
 * 只推荐正收益的一步（收组 / 翻暗牌 / 同花色接续 / 腾出暗牌多的列），
 * 返回 null 表示无有价值的移动，此时若备牌未发完应转而建议发牌。
 */
export function findHint(layout: SpiderLayout): SpiderHint | null {
  const t = layout.tableau;
  let best: SpiderHint | null = null;
  let bestScore = 0;
  for (let from = 0; from < t.length; from++) {
    const col = t[from];
    const hidden = col.filter((c) => !c.open).length;
    for (let index = 0; index < col.length; index++) {
      if (!canPickUp(col, index)) continue;
      const head = col[index];
      const count = col.length - index;
      for (let to = 0; to < t.length; to++) {
        if (to === from) continue;
        const dst = t[to];
        if (!canDrop(dst, head)) continue;
        if (dst.length === 0 && index === 0) continue; // 整列搬到空列：原地踏步

        const opens = index > 0 && !col[index - 1].open;
        const collect = canCollect([...dst, ...col.slice(index)]);
        let score = count * VALUE_CARD + hidden * VALUE_HIDDEN;
        if (opens) score += VALUE_OPEN;
        if (collect) score += VALUE_COLLECT;
        if (dst.length === 0) score -= PENALTY_EMPTY;
        else if (dst[dst.length - 1].suit === head.suit) score += VALUE_SUIT;

        if (score > bestScore) {
          bestScore = score;
          best = { from, index, to, count, collect, opens };
        }
      }
    }
  }
  return best;
}
