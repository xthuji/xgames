import { CardInfo } from '../../../platform/protocol';

/**
 * 客户端本地牌型判定与出牌提示系统。
 * 用于"提示"选牌建议（AI 智能排序）和挂机自动出牌；
 * 服务端对实际出牌做权威校验。
 */

export interface ParsedHand {
  type: string;
  rank: number; // 主点数
  length: number; // 顺子/连对/飞机长度
}

/** Hint 出牌建议（含评分，score 越低越优先） */
export interface Hint {
  cards: CardInfo[];
  score: number;
}

/** 解析牌型；非法返回 null */
export function parseHand(cards: CardInfo[]): ParsedHand | null {
  const n = cards.length;
  if (n === 0) return null;
  const ranks = cards.map((c) => c.rank).sort((a, b) => a - b);
  const count = new Map<number, number>();
  for (const r of ranks) count.set(r, (count.get(r) ?? 0) + 1);

  if (n === 2 && ranks[0] === 16 && ranks[1] === 17) return { type: 'rocket', rank: 99, length: 1 };
  if (n === 1) return { type: 'single', rank: ranks[0], length: 1 };
  if (n === 2 && ranks[0] === ranks[1]) return { type: 'pair', rank: ranks[0], length: 1 };
  if (n === 3 && ranks[0] === ranks[1] && ranks[1] === ranks[2]) return { type: 'triple', rank: ranks[0], length: 1 };
  if (n === 4 && ranks[0] === ranks[3]) return { type: 'bomb', rank: ranks[0], length: 1 };

  const byCount = (k: number) => [...count.entries()].filter(([, c]) => c === k).map(([r]) => r).sort((a, b) => a - b);

  if (n === 4 && byCount(3).length === 1) return { type: 'triple_single', rank: byCount(3)[0], length: 1 };
  if (n === 5 && byCount(3).length === 1 && byCount(2).length === 1) return { type: 'triple_pair', rank: byCount(3)[0], length: 1 };

  // 顺子：≥5 张连续单张（不含 2 和王）
  if (n >= 5 && count.size === n && ranks[n - 1] <= 14 && isConsecutive(ranks)) {
    return { type: 'straight', rank: ranks[n - 1], length: n };
  }
  // 连对：≥3 对连续
  if (n >= 6 && n % 2 === 0 && byCount(2).length === n / 2 && byCount(2)[byCount(2).length - 1] <= 14 && isConsecutive(byCount(2))) {
    return { type: 'pair_straight', rank: byCount(2)[byCount(2).length - 1], length: n / 2 };
  }
  // 飞机不带：≥2 个连续三张
  const triples = byCount(3);
  if (n >= 6 && n % 3 === 0 && triples.length === n / 3 && triples[triples.length - 1] <= 14 && isConsecutive(triples)) {
    return { type: 'plane', rank: triples[triples.length - 1], length: triples.length };
  }
  // 飞机带单/带对：机身连续三张 + 同数量翅膀
  if (triples.length >= 2 && isConsecutive(triples) && triples[triples.length - 1] <= 14) {
    const bodyLen = triples.length;
    // 翅膀不能拆炸弹（四张同点）当带牌，也不能把王炸拆作带牌
    const hasFourWing = [...count.values()].some((c) => c === 4);
    const bothJokers = (count.get(16) ?? 0) > 0 && (count.get(17) ?? 0) > 0;
    // 飞机带单：带 bodyLen 张单牌，对子可拆作两张单张（如 2 个机身可带一对）
    if (n === bodyLen * 4 && !hasFourWing && !bothJokers) {
      return { type: 'plane_with_singles', rank: triples[bodyLen - 1], length: bodyLen };
    }
    // 飞机带对：带 bodyLen 对
    if (n === bodyLen * 5 && byCount(2).length === bodyLen) {
      return { type: 'plane_with_pairs', rank: triples[bodyLen - 1], length: bodyLen };
    }
  }
  // 四带二（单）
  if (n === 6 && byCount(4).length === 1) return { type: 'four_two', rank: byCount(4)[0], length: 1 };
  return null;
}

function isConsecutive(sorted: number[]): boolean {
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i] !== sorted[i - 1] + 1) return false;
  }
  return true;
}

/** 能否压过（同类型比主点数，炸弹/火箭通吃） */
export function canBeat(next: ParsedHand, last: ParsedHand): boolean {
  if (next.type === 'rocket') return true;
  if (last.type === 'rocket') return false;
  if (next.type === 'bomb' && last.type !== 'bomb') return true;
  if (next.type !== last.type || next.length !== last.length) return false;
  return next.rank > last.rank;
}

// --- 评分 ---

function scorePlay(cards: CardInfo[]): number {
  let sum = 0;
  for (const c of cards) sum += c.rank;
  const ph = parseHand(cards);
  if (ph) {
    if (ph.type === 'bomb') sum += 30;
    if (ph.type === 'rocket') sum += 40;
  }
  if (cards.length >= 2) sum -= cards.length * 2;
  return sum;
}

// --- 主入口 ---

/** 生成所有出牌建议并按推荐度排序（score 升序，越低越优先） */
export function hintAll(hand: CardInfo[], last: CardInfo[] | null): CardInfo[][] {
  const hints = generateHints(hand, last);
  return hints.map((h) => h.cards);
}

/** 生成带评分的出牌建议列表（供高级 UI 使用） */
export function generateHints(hand: CardInfo[], last: CardInfo[] | null): Hint[] {
  const target = last && last.length > 0 ? parseHand(last) : null;
  let hints: Hint[];
  if (!target) {
    hints = generateLeadHints(hand);
  } else {
    hints = generateFollowHints(hand, target);
  }
  hints.sort((a, b) => a.score - b.score);
  return hints;
}

/** 取得分最优的出牌建议；返回 null 表示应 pass */
export function bestHint(hand: CardInfo[], last: CardInfo[] | null): CardInfo[] | null {
  const hints = generateHints(hand, last);
  if (hints.length === 0 || hints[0].score <= 0) return null;
  return hints[0].cards;
}

/** 出牌提示（兼容旧接口）：返回最优建议 */
export function hint(hand: CardInfo[], last: CardInfo[] | null): CardInfo[] | null {
  return bestHint(hand, last);
}

// --- 领出 ---

function generateLeadHints(hand: CardInfo[]): Hint[] {
  const cbr = groupByRank(hand);
  const hints: Hint[] = [];

  // 单张
  for (const cs of cbr.values()) hints.push({ cards: [cs[0]], score: scorePlay([cs[0]]) });
  // 对子
  for (const cs of cbr.values()) {
    if (cs.length >= 2) { const c = cs.slice(0, 2); hints.push({ cards: c, score: scorePlay(c) }); }
  }
  // 三张 / 三带一 / 三带二
  for (const cs of cbr.values()) {
    if (cs.length >= 3) {
      const trio = cs.slice(0, 3);
      hints.push({ cards: trio, score: scorePlay(trio) });
      const k1 = pickKicker(hand, trio, 1);
      if (k1) { const c = [...trio, ...k1]; hints.push({ cards: c, score: scorePlay(c) }); }
      const k2 = pickKicker(hand, trio, 2);
      if (k2) { const c = [...trio, ...k2]; hints.push({ cards: c, score: scorePlay(c) }); }
    }
  }
  // 炸弹（含四带二、四带两对）
  for (const cs of cbr.values()) {
    if (cs.length >= 4) {
      const four = cs.slice(0, 4);
      hints.push({ cards: four, score: scorePlay(four) });
      const remaining = excludeCards(hand, four);
      const k2 = pickN(remaining, 2);
      if (k2) { const c = [...four, ...k2]; hints.push({ cards: c, score: scorePlay(c) }); }
      const kp = pickPairs(remaining, 2);
      if (kp) { const c = [...four, ...kp]; hints.push({ cards: c, score: scorePlay(c) }); }
    }
  }
  // 火箭
  const jokers = findJokers(hand);
  if (jokers) hints.push({ cards: jokers, score: scorePlay(jokers) });
  // 顺子
  hints.push(...genStraights(cbr));
  // 连对
  hints.push(...genPairStraights(cbr));
  // 飞机
  hints.push(...genPlanes(hand, cbr));
  return hints;
}

// --- 跟牌 ---

function generateFollowHints(hand: CardInfo[], target: ParsedHand): Hint[] {
  const cbr = groupByRank(hand);
  const hints: Hint[] = [];

  switch (target.type) {
    case 'single': hints.push(...followSingle(hand, cbr, target)); break;
    case 'pair': hints.push(...followPair(cbr, target)); break;
    case 'triple': hints.push(...followTrio(hand, cbr, target, 0)); break;
    case 'triple_single': hints.push(...followTrio(hand, cbr, target, 1)); break;
    case 'triple_pair': hints.push(...followTrio(hand, cbr, target, 2)); break;
    case 'straight': hints.push(...followStraight(cbr, target)); break;
    case 'pair_straight': hints.push(...followPairStraight(cbr, target)); break;
    case 'plane': hints.push(...followPlane(hand, cbr, target, 0)); break;
    case 'plane_with_singles': hints.push(...followPlane(hand, cbr, target, 1)); break;
    case 'plane_with_pairs': hints.push(...followPlane(hand, cbr, target, 2)); break;
    // four_two / four_two_pairs: 只能被炸弹/火箭压
  }
  // 炸弹
  if (target.type !== 'bomb') {
    for (const cs of cbr.values()) {
      if (cs.length >= 4) { const c = cs.slice(0, 4); hints.push({ cards: c, score: scorePlay(c) }); }
    }
  } else {
    for (const [r, cs] of cbr) {
      if (cs.length >= 4 && r > target.rank) { const c = cs.slice(0, 4); hints.push({ cards: c, score: scorePlay(c) }); }
    }
  }
  // 火箭
  if (target.type !== 'rocket') {
    const jokers = findJokers(hand);
    if (jokers) hints.push({ cards: jokers, score: scorePlay(jokers) });
  }
  return hints;
}

function followSingle(hand: CardInfo[], cbr: Map<number, CardInfo[]>, t: ParsedHand): Hint[] {
  const h: Hint[] = [];
  for (const [r, cs] of cbr) {
    if (r > t.rank) {
      h.push({ cards: [cs[0]], score: scorePlay([cs[0]]) });
      if (cs.length >= 2) { const c = cs.slice(0, 2); h.push({ cards: c, score: scorePlay(c) }); }
      if (cs.length >= 3) {
        const trio = cs.slice(0, 3);
        h.push({ cards: trio, score: scorePlay(trio) });
        const k = pickKicker(hand, trio, 1);
        if (k) { const c = [...trio, ...k]; h.push({ cards: c, score: scorePlay(c) }); }
      }
    }
  }
  return h;
}

function followPair(cbr: Map<number, CardInfo[]>, t: ParsedHand): Hint[] {
  const h: Hint[] = [];
  for (const [r, cs] of cbr) {
    if (r > t.rank && cs.length >= 2) { const c = cs.slice(0, 2); h.push({ cards: c, score: scorePlay(c) }); }
  }
  return h;
}

function followTrio(hand: CardInfo[], cbr: Map<number, CardInfo[]>, t: ParsedHand, kickerType: number): Hint[] {
  const h: Hint[] = [];
  for (const [r, cs] of cbr) {
    if (r > t.rank && cs.length >= 3) {
      const trio = cs.slice(0, 3);
      if (kickerType === 0) {
        h.push({ cards: trio, score: scorePlay(trio) });
      } else {
        const k = pickKicker(hand, trio, kickerType);
        if (k) { const c = [...trio, ...k]; h.push({ cards: c, score: scorePlay(c) }); }
      }
    }
  }
  return h;
}

function followStraight(cbr: Map<number, CardInfo[]>, t: ParsedHand): Hint[] {
  const h: Hint[] = [];
  const consec = consecutiveRanks(cbr, 3, 14);
  for (const seq of subsequences(consec, t.length)) {
    if (seq[seq.length - 1] > t.rank) {
      const c = takeOnePerRank(cbr, seq);
      h.push({ cards: c, score: scorePlay(c) });
    }
  }
  return h;
}

function followPairStraight(cbr: Map<number, CardInfo[]>, t: ParsedHand): Hint[] {
  const h: Hint[] = [];
  const consec = consecutiveRanksWithMin(cbr, 3, 14, 2);
  for (const seq of subsequences(consec, t.length)) {
    if (seq[seq.length - 1] > t.rank) {
      const c = takeNPerRank(cbr, seq, 2);
      h.push({ cards: c, score: scorePlay(c) });
    }
  }
  return h;
}

function followPlane(hand: CardInfo[], cbr: Map<number, CardInfo[]>, t: ParsedHand, kickerType: number): Hint[] {
  const h: Hint[] = [];
  const trioRanks = consecutiveRanksWithMin(cbr, 3, 14, 3);
  for (const seq of subsequences(trioRanks, t.length)) {
    if (seq[seq.length - 1] > t.rank) {
      const base = takeNPerRank(cbr, seq, 3);
      if (kickerType === 0) {
        h.push({ cards: base, score: scorePlay(base) });
      } else {
        const used = new Set(seq);
        const k = pickKickersExcluding(hand, used, kickerType * t.length);
        if (k) { const c = [...base, ...k]; h.push({ cards: c, score: scorePlay(c) }); }
      }
    }
  }
  return h;
}

// --- 领出复合牌型 ---

function genStraights(cbr: Map<number, CardInfo[]>): Hint[] {
  const hints: Hint[] = [];
  const consec = consecutiveRanks(cbr, 3, 14);
  for (let len = consec.length; len >= 5; len--) {
    for (const seq of subsequences(consec, len)) {
      const c = takeOnePerRank(cbr, seq);
      hints.push({ cards: c, score: scorePlay(c) });
    }
  }
  return hints;
}

function genPairStraights(cbr: Map<number, CardInfo[]>): Hint[] {
  const hints: Hint[] = [];
  const consec = consecutiveRanksWithMin(cbr, 3, 14, 2);
  for (let len = consec.length; len >= 3; len--) {
    for (const seq of subsequences(consec, len)) {
      const c = takeNPerRank(cbr, seq, 2);
      hints.push({ cards: c, score: scorePlay(c) });
    }
  }
  return hints;
}

function genPlanes(hand: CardInfo[], cbr: Map<number, CardInfo[]>): Hint[] {
  const hints: Hint[] = [];
  const trioRanks = consecutiveRanksWithMin(cbr, 3, 14, 3);
  for (let len = trioRanks.length; len >= 2; len--) {
    for (const seq of subsequences(trioRanks, len)) {
      const base = takeNPerRank(cbr, seq, 3);
      hints.push({ cards: base, score: scorePlay(base) });
      const used = new Set(seq);
      const ks = pickKickersExcluding(hand, used, len);
      if (ks) { const c = [...base, ...ks]; hints.push({ cards: c, score: scorePlay(c) }); }
      const kp = pickKickersExcludingPairs(hand, used, len);
      if (kp) { const c = [...base, ...kp]; hints.push({ cards: c, score: scorePlay(c) }); }
    }
  }
  return hints;
}

// --- 辅助函数 ---

function groupByRank(hand: CardInfo[]): Map<number, CardInfo[]> {
  const m = new Map<number, CardInfo[]>();
  for (const c of hand) {
    const arr = m.get(c.rank) ?? [];
    arr.push(c);
    m.set(c.rank, arr);
  }
  return m;
}

function findJokers(hand: CardInfo[]): CardInfo[] | null {
  const jokers = hand.filter((c) => c.rank === 16 || c.rank === 17);
  return jokers.length === 2 ? jokers : null;
}

function consecutiveRanks(cbr: Map<number, CardInfo[]>, lo: number, hi: number): number[] {
  const seq: number[] = [];
  for (let r = lo; r <= hi; r++) {
    if ((cbr.get(r) ?? []).length > 0) seq.push(r);
    else if (seq.length > 0) break;
  }
  return seq;
}

function consecutiveRanksWithMin(cbr: Map<number, CardInfo[]>, lo: number, hi: number, minCount: number): number[] {
  const seq: number[] = [];
  for (let r = lo; r <= hi; r++) {
    if ((cbr.get(r) ?? []).length >= minCount) seq.push(r);
    else if (seq.length > 0) break;
  }
  return seq;
}

function subsequences(sorted: number[], length: number): number[][] {
  if (sorted.length < length) return [];
  const result: number[][] = [];
  for (let i = 0; i <= sorted.length - length; i++) {
    result.push(sorted.slice(i, i + length));
  }
  return result;
}

function takeOnePerRank(cbr: Map<number, CardInfo[]>, ranks: number[]): CardInfo[] {
  return ranks.map((r) => cbr.get(r)![0]);
}

function takeNPerRank(cbr: Map<number, CardInfo[]>, ranks: number[], n: number): CardInfo[] {
  const cs: CardInfo[] = [];
  for (const r of ranks) cs.push(...cbr.get(r)!.slice(0, n));
  return cs;
}

function pickKicker(hand: CardInfo[], exclude: CardInfo[], count: number): CardInfo[] | null {
  const excludeSet = new Set(exclude.map((c) => `${c.suit}-${c.rank}`));
  const usedRanks = new Set(exclude.map((c) => c.rank));

  if (count === 2) {
    for (const [r, cs] of groupByRank(hand)) {
      const avail = cs.filter((c) => !excludeSet.has(`${c.suit}-${c.rank}`));
      if (avail.length >= 2 && !usedRanks.has(r)) return avail.slice(0, 2);
    }
    return null;
  }

  for (const c of hand) {
    if (!excludeSet.has(`${c.suit}-${c.rank}`) && !usedRanks.has(c.rank)) return [c];
  }
  return null;
}

function pickKickersExcluding(hand: CardInfo[], usedRanks: Set<number>, n: number): CardInfo[] | null {
  const picked: CardInfo[] = [];
  const seen = new Set<number>();
  for (const c of hand) {
    if (picked.length >= n) break;
    if (usedRanks.has(c.rank) || seen.has(c.rank)) continue;
    picked.push(c);
    seen.add(c.rank);
  }
  return picked.length >= n ? picked : null;
}

function pickKickersExcludingPairs(hand: CardInfo[], usedRanks: Set<number>, pairCount: number): CardInfo[] | null {
  const cbr = groupByRank(hand);
  const picked: CardInfo[] = [];
  const seen = new Set<number>();
  for (let r = 3; r <= 15; r++) {
    if (picked.length >= pairCount * 2) break;
    if (usedRanks.has(r) || seen.has(r)) continue;
    const cs = cbr.get(r) ?? [];
    if (cs.length >= 2) { picked.push(...cs.slice(0, 2)); seen.add(r); }
  }
  return picked.length >= pairCount * 2 ? picked : null;
}

function pickN(hand: CardInfo[], n: number): CardInfo[] | null {
  return hand.length >= n ? hand.slice(0, n) : null;
}

function pickPairs(hand: CardInfo[], pairCount: number): CardInfo[] | null {
  const cbr = groupByRank(hand);
  const picked: CardInfo[] = [];
  for (let r = 3; r <= 17; r++) {
    if (picked.length >= pairCount * 2) break;
    const cs = cbr.get(r) ?? [];
    if (cs.length >= 2) picked.push(...cs.slice(0, 2));
  }
  return picked.length >= pairCount * 2 ? picked : null;
}

function excludeCards(hand: CardInfo[], cards: CardInfo[]): CardInfo[] {
  const used = new Set<string>();
  for (const tc of cards) used.add(`${tc.suit}-${tc.rank}`);
  const result: CardInfo[] = [];
  for (const c of hand) {
    const key = `${c.suit}-${c.rank}`;
    if (used.has(key)) { used.delete(key); }
    else { result.push(c); }
  }
  return result;
}
