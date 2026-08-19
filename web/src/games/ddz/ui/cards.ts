import { CardInfo } from '../../../platform/protocol';

export const CARD_W = 90;
export const BACK_FRAME = 54;

/** CardInfo → poker spritesheet 帧号
 * 图集布局：4 行花色 (行0=♥, 行1=♦, 行2=♠, 行3=♣) × 13 列 (列0=A, 列1=2, 列2=3..列12=K)
 * 行4：52=大王, 53=小王, 54=牌背
 * suit: 0=♠ 1=♥ 2=♣ 3=♦ 4=王
 * rank: 3-13=3-K, 14=A, 15=2, 16=小王, 17=大王
 */
export function cardFrame(c: CardInfo): number {
  if (c.suit === 4) {
    return c.rank === 17 ? 52 : 53;
  }
  const suitRow = [2, 0, 3, 1][c.suit] ?? 0;
  const col = c.rank === 14 ? 0 : c.rank === 15 ? 1 : c.rank - 1; // A=0, 2=1, 3=2..K=12
  return suitRow * 13 + col;
}

/** rank → 中文语音名（3~2 / 小王 / 大王），用于牌型语音播报 */
export function rankChinese(rank: number): string {
  if (rank >= 3 && rank <= 10) return String(rank);
  switch (rank) {
    case 11: return '钩';  // J
    case 12: return '圈';  // Q
    case 13: return '凯';  // K
    case 14: return '尖';  // A
    case 15: return '二';
    case 16: return '小王';
    case 17: return '大王';
    default: return String(rank);
  }
}

/** 手牌排序：点数从大到小（大王 → 3） */
export function sortHandDesc(cards: CardInfo[]): CardInfo[] {
  return [...cards].sort((a, b) => b.rank - a.rank || a.suit - b.suit);
}

/** 手牌扇形平铺间距（landlord gap 算法简化：牌多时压缩间距），返回每张牌的 x */
export function handLayoutXs(count: number, maxWidth: number): number[] {
  if (count <= 0) return [];
  const gap = Math.min(46, Math.max(24, Math.floor((maxWidth - CARD_W) / Math.max(1, count - 1))));
  const total = CARD_W + gap * (count - 1);
  const xs: number[] = [];
  for (let i = 0; i < count; i++) {
    xs.push(-total / 2 + i * gap + CARD_W / 2);
  }
  return xs;
}

/** 牌相等（花色 + 点数） */
export function sameCard(a: CardInfo, b: CardInfo): boolean {
  return a.suit === b.suit && a.rank === b.rank;
}

/** 从手牌中移除已打出的牌 */
export function removeCards(hand: CardInfo[], played: CardInfo[]): CardInfo[] {
  const out = [...hand];
  for (const p of played) {
    const i = out.findIndex((c) => sameCard(c, p));
    if (i >= 0) out.splice(i, 1);
  }
  return out;
}
