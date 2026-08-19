import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { addHintButton, clearHint, highlightBoxes, showHintText, type HintBox } from '../../platform/ui/hint';
import {
  SpiderCard,
  SpiderLayout,
  canCollect,
  canDeal,
  canDrop,
  canPickUp,
  collectRun,
  deal,
  dealRound,
  findHint,
  openTop,
} from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

const CARD_W = 90;
const CARD_H = 120;
const BACK_FRAME = 54;
const COLS = 10;
const TABLEAU_X = 25;
const COL_STEP = 122;
const TABLEAU_Y = 100;
const COL_MAX_H = 470; // 牌列可用高度（100 → 570）
const DOWN_GAP = 16; // 暗牌叠放间距
const UP_GAP = 26; // 明牌叠放间距
const STOCK_X = 25;
const STOCK_Y = 590;
const FOUND_X = 479;
const FOUND_STEP = 98;
const FOUND_Y = 590;
/** 提示条画在顶栏与牌列之间的空隔：默认画布底部会被完成区的牌压住 */
const HINT_Y = 66;

/** poker 图集帧号：花色 (0♠1♥2♣3♦) → 行 (♥0 ♦1 ♠2 ♣3)，rank 1-13 → 列 0-12 */
function cardFrame(c: SpiderCard): number {
  const row = [2, 0, 3, 1][c.suit];
  return row * 13 + (c.rank - 1);
}

function fmtTime(s: number): string {
  const m = Math.floor(s / 60);
  return `${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
}

/** 花色下标 → 字形（与 cardFrame 的行映射一致） */
const SUIT_GLYPHS = ['♠', '♥', '♣', '♦'];
const RANK_NAMES = ['', 'A', '2', '3', '4', '5', '6', '7', '8', '9', '10', 'J', 'Q', 'K'];

/** 蜘蛛纸牌（单机）：点选移动 + 备牌发牌 + K→A 自动收集，计分 500 起、每步 −1、每组 +100 */
export class SpiderScene extends Phaser.Scene {
  private layout!: SpiderLayout;
  private done: SpiderCard[][] = [];
  private sel: { col: number; index: number } | null = null;
  private score = 500;
  private moves = 0;
  private seconds = 0;
  private over = false;
  private started = false;
  private layer!: Phaser.GameObjects.Container;
  private hudText!: Phaser.GameObjects.Text;
  private timer?: Phaser.Time.TimerEvent;
  private overlay?: Phaser.GameObjects.Container;
  
  // === 渲染优化：持久化精灵 + 增量更新 ===
  /** 列占位区 */
  private activeZones: Phaser.GameObjects.Rectangle[] = [];
  /** 牌精灵网格 [col][index] -> sprite */
  private cardGrid: Map<string, Phaser.GameObjects.Sprite> = new Map();
  /** 备牌精灵列表 */
  private stockSprites: Phaser.GameObjects.Sprite[] = [];
  /** 完成区精灵列表 */
  private doneSprites: Phaser.GameObjects.Sprite[] = [];
  /** 空槽矩形列表 */
  private emptySlots: Phaser.GameObjects.Rectangle[] = [];
  /** 上一帧的游戏状态哈希 */
  private lastStateHash = '';
  /** 是否已初始化渲染 */
  private isInitialized = false;

  constructor() {
    super('SpiderGame');
  }

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 蜘蛛纸牌是静态场景，不需要每帧更新
    // 所有变化都通过事件驱动（点击、计时器等）
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/spider') location.hash = '/game/spider';

    this.buildTopBar();
    this.layer = this.add.container(0, 0);
    this.newGame();

    this.timer = this.time.addEvent({
      delay: 5000, // 5秒更新一次计时器，降低CPU占用
      loop: true,
      callback: () => {
        if (this.started && !this.over) {
          this.seconds += 5;
          this.updateHud();
        }
      },
    });
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.timer?.remove());
  }

  private buildTopBar() {
    this.add.text(20, 18, '🕷️ 蜘蛛纸牌', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });
    this.hudText = this.add.text(640, 24, '', { ...FONT, fontSize: '20px' }).setOrigin(0.5);

    addHintButton(this, 905, () => this.showHint());

    const restartBtn = this.add.text(1040, 24, '🔄 重新开始', {
      ...FONT, fontSize: '16px', color: '#ffd54f', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
    }).setOrigin(0.5).setInteractive({ useHandCursor: true });
    restartBtn.on('pointerdown', () => this.newGame());

    const exitBtn = this.add.text(1260, 24, '🚪 退出', {
      ...FONT, fontSize: '16px', color: '#ff8a80', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
    }).setOrigin(0.5).setInteractive({ useHandCursor: true });
    exitBtn.on('pointerdown', () => this.exitToLobby());
  }

  private newGame() {
    // 清空所有旧状态
    this.clearAllGraphics();
    
    this.layout = deal(1); // 基础版默认单花色（简单）
    this.done = [];
    this.sel = null;
    this.score = 500;
    this.moves = 0;
    this.seconds = 0;
    this.over = false;
    this.started = false;
    clearHint(this);
    this.lastStateHash = ''; // 重置状态哈希
    this.isInitialized = false;
    this.overlay?.destroy();
    this.overlay = undefined;
    this.updateHud();
    this.render();
    this.showStartOverlay();
  }

  /** 清空所有图形对象 */
  private clearAllGraphics() {
    // 销毁牌精灵
    for (const [, sprite] of this.cardGrid) {
      sprite.destroy();
    }
    this.cardGrid.clear();
    
    // 销毁备牌精灵
    for (const s of this.stockSprites) {
      s.destroy();
    }
    this.stockSprites = [];
    
    // 销毁完成区精灵
    for (const s of this.doneSprites) {
      s.destroy();
    }
    this.doneSprites = [];
    
    // 销毁空槽矩形
    for (const r of this.emptySlots) {
      r.destroy();
    }
    this.emptySlots = [];
    
    // 销毁占位区
    for (const zone of this.activeZones) {
      zone.destroy();
    }
    this.activeZones = [];
  }

  private showStartOverlay() {
    const container = this.add.container(0, 0).setDepth(200);
    const bg = this.add.rectangle(640, 360, 1280, 720, 0x000000, 0.55);
    const title = this.add.text(640, 240, '🕷️ 蜘蛛纸牌', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '点选移动 · K→A 自动收集 · 发牌前至少每列 1 张', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

    // 按钮容器
    const btnContainer = this.add.container(640, 400);

    // 规则按钮
    const ruleBtnBg = this.add.rectangle(-140, 0, 160, 50, 0x0288d1, 0.9)
      .setStrokeStyle(2, 0xffffff, 0.6)
      .setInteractive({ useHandCursor: true });
    const ruleBtnText = this.add.text(-140, 0, '📖 规则', { ...FONT, fontSize: '20px', color: '#ffffff' }).setOrigin(0.5);
    ruleBtnBg.on('pointerover', () => ruleBtnBg.setFillStyle(0x0398e1, 1));
    ruleBtnBg.on('pointerout', () => ruleBtnBg.setFillStyle(0x0288d1, 0.9));
    ruleBtnBg.on('pointerdown', () => {
      const rules = getGameRule('spider');
      if (rules) showGameRules(this, getGameName('spider'), rules);
    });

    // 开始按钮
    const startBtn = this.add.text(140, 0, '🚀 开始游戏', { ...FONT, fontSize: '28px', color: '#ffe082', backgroundColor: '#000000aa', padding: { x: 28, y: 14 } })
      .setOrigin(0.5).setInteractive({ useHandCursor: true });
    startBtn.on('pointerover', () => startBtn.setStyle({ ...startBtn.style, color: '#fff176' }));
    startBtn.on('pointerout', () => startBtn.setStyle({ ...startBtn.style, color: '#ffe082' }));
    startBtn.on('pointerdown', () => { this.started = true; container.destroy(); this.overlay = undefined; });

    btnContainer.add([ruleBtnBg, ruleBtnText, startBtn]);
    container.add([bg, title, hint, btnContainer]);
    this.overlay = container;
  }

  private updateHud() {
    this.hudText.setText(`⏱ ${fmtTime(this.seconds)}    💰 ${this.score} 分    🃏 ${this.moves} 步    📦 备牌 ${this.layout.stock.length}`);
  }

  // ── 渲染（持久化精灵 + 增量更新）──

  private render() {
    // 计算当前状态哈希，如果状态未变化则跳过重绘
    const stateHash = this.computeStateHash();
    if (stateHash === this.lastStateHash) return;
    this.lastStateHash = stateHash;

    const t = this.layout.tableau;

    // 首次渲染：创建所有基础结构
    if (!this.isInitialized) {
      this.isInitialized = true;
      this.createBaseStructure(t);
    }

    // 增量更新：只更新变化的部分
    this.updateTableau(t);
    this.updateStock();
    this.updateDone();
  }

  /** 创建基础结构（占位区等） */
  private createBaseStructure(t: SpiderCard[][]) {
    // 列占位区（最底层）
    for (let i = 0; i < COLS; i++) {
      const x = TABLEAU_X + i * COL_STEP;
      if (t[i].length === 0) {
        const rect = this.add.rectangle(x + CARD_W / 2, TABLEAU_Y + CARD_H / 2, CARD_W, CARD_H, 0xffffff, 0.06)
          .setStrokeStyle(1, 0xffffff, 0.3);
        this.layer.add(rect);
      }
      const zone = this.add.rectangle(x + CARD_W / 2, TABLEAU_Y + COL_MAX_H / 2, CARD_W, COL_MAX_H, 0xffffff, 0)
        .setInteractive();
      zone.on('pointerdown', () => this.onZoneTap(i));
      this.layer.add(zone);
      this.activeZones.push(zone);
    }
  }

  /** 增量更新牌列 */
  private updateTableau(t: SpiderCard[][]) {
    // 关键修复：由于牌的索引会变化，无法安全复用精灵
    // 所以采用完全重建策略，但只在实际需要时执行
    
    // 先销毁所有旧的牌列精灵
    for (const [, sprite] of this.cardGrid) {
      sprite.destroy();
    }
    this.cardGrid.clear();
    
    // 重新创建所有精灵
    for (let i = 0; i < COLS; i++) {
      const col = t[i];
      const x = TABLEAU_X + i * COL_STEP;
      const fd = col.filter((c) => !c.open).length;
      const fu = col.length - fd;
      let upGap = UP_GAP;
      if (fu > 1 && fd * DOWN_GAP + (fu - 1) * upGap + CARD_H > COL_MAX_H) {
        upGap = Math.max(12, Math.floor((COL_MAX_H - CARD_H - fd * DOWN_GAP) / (fu - 1)));
      }
      let y = TABLEAU_Y;
      
      col.forEach((card, j) => {
        const key = `${i},${j}`;
        const frame = card.open ? cardFrame(card) : BACK_FRAME;
        
        const sprite = this.add.sprite(x, y, 'poker', frame).setOrigin(0, 0);
        this.cardGrid.set(key, sprite);
        this.layer.add(sprite);
        
        // 设置交互（仅明牌）
        if (card.open) {
          sprite.setInteractive({ useHandCursor: true });
          sprite.on('pointerdown', () => this.onTableauTap(i, j));
          if (this.sel && this.sel.col === i && j >= this.sel.index) {
            sprite.setTint(0xffd54f);
          }
        }
        
        y += card.open ? upGap : DOWN_GAP;
      });
    }
  }

  /** 增量更新备牌 */
  private updateStock() {
    // 销毁旧备牌
    for (const s of this.stockSprites) {
      s.destroy();
    }
    this.stockSprites = [];
    
    const stockN = this.layout.stock.length;
    if (stockN > 0) {
      for (let k = 0; k < stockN; k++) {
        const s = this.add.sprite(STOCK_X + k * 4, STOCK_Y, 'poker', BACK_FRAME).setOrigin(0, 0);
        if (k === stockN - 1) {
          s.setInteractive({ useHandCursor: true });
          s.on('pointerdown', () => this.onStockTap());
        }
        this.stockSprites.push(s);
        this.layer.add(s);
      }
    } else {
      // 检查是否已有"备牌已发完"文本
      const existingText = this.layer.getAll().find(obj => 
        obj instanceof Phaser.GameObjects.Text && 
        (obj as Phaser.GameObjects.Text).text === '备牌已发完'
      );
      if (!existingText) {
        this.layer.add(this.add.text(STOCK_X, STOCK_Y + CARD_H / 2, '备牌已发完', { ...FONT, color: '#888888', fontSize: '14px' }).setOrigin(0, 0.5));
      }
    }
  }

  /** 增量更新完成区 */
  private updateDone() {
    // 销毁旧完成区精灵
    for (const s of this.doneSprites) {
      s.destroy();
    }
    this.doneSprites = [];
    
    // 销毁旧空槽
    for (const r of this.emptySlots) {
      r.destroy();
    }
    this.emptySlots = [];
    
    for (let k = 0; k < 8; k++) {
      const x = FOUND_X + k * FOUND_STEP;
      if (k < this.done.length) {
        const top = this.done[k][this.done[k].length - 1];
        const s = this.add.sprite(x, FOUND_Y, 'poker', cardFrame(top)).setOrigin(0, 0);
        this.doneSprites.push(s);
        this.layer.add(s);
      } else {
        const rect = this.add.rectangle(x + CARD_W / 2, FOUND_Y + CARD_H / 2, CARD_W, CARD_H, 0xffffff, 0.04)
          .setStrokeStyle(1, 0xffffff, 0.2);
        this.emptySlots.push(rect);
        this.layer.add(rect);
      }
    }
  }

  /** 计算游戏状态的简单哈希，用于检测是否需要重绘 */
  private computeStateHash(): string {
    // 使用更高效的哈希算法：只检查关键变化点
    let hash = `${this.sel ? `${this.sel.col},${this.sel.index}` : 'n'}|${this.done.length}|${this.layout.stock.length}`;
    
    // 只检查每列的牌数和顶牌状态（最可能变化的部分）
    for (let i = 0; i < COLS; i++) {
      const col = this.layout.tableau[i];
      hash += `|${col.length}`;
      if (col.length > 0) {
        const top = col[col.length - 1];
        hash += `,${top.suit}${top.rank}${top.open ? 1 : 0}`;
      }
    }
    
    return hash;
  }

  // ── 交互 ──

  private onTableauTap(col: number, index: number) {
    if (this.over || !this.started) return;
    // 已有选中且点击其他列 → 尝试移动
    if (this.sel && this.sel.col !== col) {
      const moving = this.layout.tableau[this.sel.col].slice(this.sel.index);
      if (canDrop(this.layout.tableau[col], moving[0])) {
        this.doMove(this.sel.col, this.sel.index, col);
        return;
      }
    }
    if (!canPickUp(this.layout.tableau[col], index)) return;
    if (this.sel && this.sel.col === col && this.sel.index === index) {
      this.sel = null; // 再点一次取消选择
    } else {
      this.sel = { col, index };
    }
    this.render();
  }

  private onZoneTap(col: number) {
    if (this.over || !this.started || !this.sel || this.sel.col === col) return;
    const moving = this.layout.tableau[this.sel.col].slice(this.sel.index);
    if (canDrop(this.layout.tableau[col], moving[0])) {
      this.doMove(this.sel.col, this.sel.index, col);
    }
  }

  private doMove(from: number, index: number, to: number) {
    clearHint(this);
    const src = this.layout.tableau[from];
    const moving = src.splice(index);
    this.layout.tableau[to].push(...moving);
    openTop(src);
    this.moves++;
    this.score -= 1;
    this.sel = null;
    this.collectFrom(to);
    this.updateHud();
    this.render();
  }

  private onStockTap() {
    if (this.over || !this.started) return;
    if (this.layout.stock.length === 0) return;
    clearHint(this);
    if (!canDeal(this.layout)) {
      this.toast('所有牌列都必须有牌才能发牌');
      return;
    }
    dealRound(this.layout);
    this.sel = null;
    for (let i = 0; i < COLS; i++) this.collectFrom(i);
    this.updateHud();
    this.render();
  }

  /** 检查并收走指定列尾部的完整 K→A（可能连带翻开新顶牌后再次成组） */
  private collectFrom(col: number) {
    const c = this.layout.tableau[col];
    while (canCollect(c)) {
      this.done.push(collectRun(c));
      this.score += 100;
      openTop(c);
    }
    if (this.done.length === 8 && !this.over) {
      this.over = true;
      this.updateHud();
      this.showOverlay('🎉 恭喜通关！');
    }
  }

  // ── 提示与结算 ──

  /**
   * 提示：高亮推荐搬动的整段牌（绿）与目标列顶牌（黄）。
   *
   * 只打分不代劳：列号文案配得上面上的第几列，玩家自己点选过去。
   */
  private showHint() {
    if (this.over || !this.started) return;
    const h = findHint(this.layout);
    if (!h) {
      clearHint(this);
      if (canDeal(this.layout)) showHintText(this, '没有划算的移牌了，点左下角备牌发一轮再看', { y: HINT_Y });
      else if (this.layout.stock.length > 0) showHintText(this, '有空列时不能发牌，先把牌移到空列再发牌', { y: HINT_Y });
      else showHintText(this, '已没有有价值的移动，只能从现有局面里选择', { y: HINT_Y });
      return;
    }
    const t = this.layout.tableau;
    const srcTop = this.cardGrid.get(`${h.from},${h.index}`);
    const srcBot = this.cardGrid.get(`${h.from},${t[h.from].length - 1}`);
    if (!srcTop || !srcBot) return;
    const dstTop = this.cardGrid.get(`${h.to},${t[h.to].length - 1}`);
    const boxes: HintBox[] = [{
      x: TABLEAU_X + h.from * COL_STEP + CARD_W / 2,
      y: (srcTop.y + srcBot.y + CARD_H) / 2,
      w: CARD_W - 4,
      h: srcBot.y + CARD_H - srcTop.y + 4,
      color: 0x69f0ae,
    }, dstTop
      ? { x: dstTop.x + CARD_W / 2, y: dstTop.y + CARD_H / 2, w: CARD_W - 4, h: CARD_H - 4, color: 0xffd54f }
      : { x: TABLEAU_X + h.to * COL_STEP + CARD_W / 2, y: TABLEAU_Y + CARD_H / 2, w: CARD_W, h: CARD_H, color: 0xffd54f }];
    highlightBoxes(this, boxes);
    const head = t[h.from][h.index];
    const why = h.collect ? '搬完能集齐一组 K→A' : h.opens ? '还能翻开一张暗牌' : '把同花色接上序列';
    showHintText(this, `把第 ${h.from + 1} 列的 ${h.count} 张（${SUIT_GLYPHS[head.suit]}${RANK_NAMES[head.rank]} 起）移到第 ${h.to + 1} 列 · ${why}`, { y: HINT_Y });
  }

  private toast(msg: string) {
    const t = this.add.text(640, 500, msg, { ...FONT, fontSize: '22px', color: '#ff8a80', backgroundColor: '#000000cc', padding: { x: 12, y: 6 } })
      .setOrigin(0.5).setDepth(300);
    this.tweens.add({ targets: t, alpha: 0, delay: 1800, duration: 500, onComplete: () => t.destroy() });
  }

  private showOverlay(title: string) {
    clearHint(this);
    const c = this.add.container(640, 360).setDepth(200);
    const mask = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6).setInteractive();
    const panel = this.add.rectangle(0, 0, 520, 300, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    c.add([mask, panel]);
    c.add(this.add.text(0, -100, title, { ...FONT, fontSize: '32px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5));
    c.add(this.add.text(0, -30, `用时 ${fmtTime(this.seconds)}    步数 ${this.moves}    得分 ${this.score}`, {
      ...FONT, fontSize: '20px',
    }).setOrigin(0.5));

    const mkBtn = (x: number, label: string, color: number, cb: () => void) => {
      const bg = this.add.rectangle(x, 60, 190, 54, color).setStrokeStyle(2, 0xffffff, 0.6).setInteractive({ useHandCursor: true });
      bg.on('pointerover', () => bg.setFillStyle(color + 0x222222));
      bg.on('pointerout', () => bg.setFillStyle(color));
      bg.on('pointerdown', cb);
      c.add([bg, this.add.text(x, 60, label, { ...FONT, fontSize: '20px', color: '#fffde7' }).setOrigin(0.5)]);
    };
    mkBtn(-105, '🔄 再来一局', 0x2e7d32, () => this.newGame());
    mkBtn(105, '🚪 返回大厅', 0xbf360c, () => this.exitToLobby());
    this.overlay = c;
  }

  private exitToLobby() {
    store.setCurrentGame('ddz'); // 回到主游戏，避免大厅守卫再次进入本单机场景
    setFlowState(FlowState.LOBBY);
    this.scene.start('Lobby');
  }
}
