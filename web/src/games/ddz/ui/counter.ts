import { CardInfo } from '../../../platform/protocol';

/**
 * CardCounter 记牌器（移植 client/card_counter.go）：
 * 初始化 54 张计数 → 扣除自己手牌 → 每收到 card_played 扣减。
 */
export class CardCounter {
  private remaining = new Map<number, number>();

  reset() {
    this.remaining.clear();
    for (let rank = 3; rank <= 15; rank++) this.remaining.set(rank, 4);
    this.remaining.set(16, 1); // 小王
    this.remaining.set(17, 1); // 大王
  }

  deduct(cards: CardInfo[]) {
    for (const c of cards) {
      const left = this.remaining.get(c.rank) ?? 0;
      if (left > 0) this.remaining.set(c.rank, left - 1);
    }
  }

  get(rank: number): number {
    return this.remaining.get(rank) ?? 0;
  }

  /** 展示顺序：大王 → 3 */
  displayRanks(): number[] {
    return [17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3];
  }
}
