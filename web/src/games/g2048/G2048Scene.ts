import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { addHintButton, arrowOf, cellBox, clearHint, dirNameOf, highlightBoxes, labelBox, showHintText } from '../../platform/ui/hint';
import { canMove, createBoard, findHint, move, reached, spawn, type Board, type Direction } from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

const SIZE = 4;
const CELL = 120;
const GAP = 12;
const BOARD_PX = SIZE * CELL + (SIZE + 1) * GAP;
const BOARD_X = (1280 - BOARD_PX) / 2 - 130;
const BOARD_Y = (720 - BOARD_PX) / 2 + 20;
const PANEL_X = BOARD_X + BOARD_PX + 70;
const BEST_KEY = 'xgames_2048_best';

const TILE_COLORS: Record<number, { bg: number; fg: string }> = {
  2: { bg: 0xeee4da, fg: '#776e65' },
  4: { bg: 0xede0c8, fg: '#776e65' },
  8: { bg: 0xf2b179, fg: '#ffffff' },
  16: { bg: 0xf59563, fg: '#ffffff' },
  32: { bg: 0xf67c5f, fg: '#ffffff' },
  64: { bg: 0xf65e3b, fg: '#ffffff' },
  128: { bg: 0xedcf72, fg: '#ffffff' },
  256: { bg: 0xedcc61, fg: '#ffffff' },
  512: { bg: 0xedc850, fg: '#ffffff' },
  1024: { bg: 0xedc53f, fg: '#ffffff' },
  2048: { bg: 0xedc22e, fg: '#ffffff' },
};

/** 2048（单机）：方向键滑动合并，达成 2048 获胜可继续挑战更高数字 */
export class G2048Scene extends Phaser.Scene {
  private board!: Board;
  private score = 0;
  private best = 0;
  private over = false;
  private wonShown = false;
  private started = false;

  private tileLayer!: Phaser.GameObjects.Container;
  private scoreText!: Phaser.GameObjects.Text;
  private bestText!: Phaser.GameObjects.Text;
  private overlay?: Phaser.GameObjects.Container;

  // === 渲染优化：持久化精灵 + 增量更新 ===
  /** 方块精灵网格 [row][col] -> {bg, text} */
  private tileGrid: Array<Array<{ bg: Phaser.GameObjects.Rectangle; text: Phaser.GameObjects.Text } | null>> = [];
  /** 背景Graphics（只创建一次） */
  private boardBg?: Phaser.GameObjects.Graphics;
  /** 上一帧的游戏状态哈希 */
  private lastStateHash = '';

  constructor() {
    super('G2048Game');
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/g2048') location.hash = '/game/g2048';
    this.best = Number(localStorage.getItem(BEST_KEY) ?? 0) || 0;

    this.buildTopBar();
    this.buildPanel();
    this.tileLayer = this.add.container(0, 0);

    this.bindKeys();
    this.newGame();
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      this.input.keyboard?.removeAllListeners();
      this.input.keyboard?.removeCapture('LEFT,RIGHT,UP,DOWN');
    });
  }

  private buildTopBar() {
    this.add.text(20, 18, '🔢 2048', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });

    addHintButton(this, 900, () => this.showHint());

    const restartBtn = this.add.text(1040, 24, '🔄 重新开始', {
      ...FONT, fontSize: '16px', color: '#ffd54f', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
    }).setOrigin(0.5).setInteractive({ useHandCursor: true });
    restartBtn.on('pointerdown', () => this.newGame());

    const exitBtn = this.add.text(1260, 24, '🚪 退出', {
      ...FONT, fontSize: '16px', color: '#ff8a80', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
    }).setOrigin(0.5).setInteractive({ useHandCursor: true });
    exitBtn.on('pointerdown', () => this.exitToLobby());
  }

  private buildPanel() {
    const mk = (y: number, label: string) => {
      this.add.text(PANEL_X, y, label, { ...FONT, fontSize: '16px', color: '#aac7ff' });
      return this.add.text(PANEL_X, y + 24, '', { ...FONT, fontSize: '24px', color: '#ffe082' });
    };
    this.scoreText = mk(160, '得分');
    this.bestText = mk(240, '最高分');
    this.add.text(PANEL_X, 340, '方向键滑动\n相同数字相撞合并', {
      ...FONT, fontSize: '14px', color: '#8899aa', lineSpacing: 6,
    });
  }

  private bindKeys() {
    const kb = this.input.keyboard;
    if (!kb) return;
    kb.addCapture('LEFT,RIGHT,UP,DOWN');
    kb.on('keydown-LEFT', () => { if (this.started) this.doMove('left'); });
    kb.on('keydown-RIGHT', () => { if (this.started) this.doMove('right'); });
    kb.on('keydown-UP', () => { if (this.started) this.doMove('up'); });
    kb.on('keydown-DOWN', () => { if (this.started) this.doMove('down'); });
  }

  private newGame() {
    this.board = createBoard();
    spawn(this.board);
    spawn(this.board);
    this.score = 0;
    this.over = false;
    this.wonShown = false;
    this.started = false;
    clearHint(this);
    this.overlay?.destroy();
    this.overlay = undefined;
    
    // 清理旧的tile网格
    this.clearTileGrid();
    
    this.updatePanel();
    this.redraw();
    this.showStartOverlay();
  }

  /** 清理所有tile精灵 */
  private clearTileGrid() {
    for (const row of this.tileGrid) {
      if (!row) continue;
      for (const cell of row) {
        if (cell) {
          cell.bg.destroy();
          cell.text.destroy();
        }
      }
    }
    this.tileGrid = [];
    this.boardBg?.destroy();
    this.boardBg = undefined;
    this.lastStateHash = '';
  }

  private showStartOverlay() {
    const container = this.add.container(0, 0).setDepth(200);
    const bg = this.add.rectangle(640, 360, 1280, 720, 0x000000, 0.55);
    const title = this.add.text(640, 240, '🔢 2048', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '方向键滑动 · 相同数字相撞合并', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

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
      const rules = getGameRule('g2048');
      if (rules) showGameRules(this, getGameName('g2048'), rules);
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

  private updatePanel() {
    this.scoreText.setText(String(this.score));
    this.bestText.setText(String(this.best));
  }

  private doMove(dir: Direction) {
    if (this.over || this.overlay) return;
    clearHint(this); // 局面已变，上一次的推荐作废
    const { moved, gained } = move(this.board, dir);
    if (!moved) return;
    spawn(this.board);
    this.score += gained;
    if (this.score > this.best) {
      this.best = this.score;
      localStorage.setItem(BEST_KEY, String(this.best));
    }
    this.updatePanel();
    this.redraw();
    if (reached(this.board, 2048) && !this.wonShown) {
      this.wonShown = true;
      this.showOverlay('🎉 达成 2048', true);
      return;
    }
    if (!canMove(this.board)) {
      this.over = true;
      this.showOverlay('💥 无路可走', false);
    }
  }

  /** 计算状态哈希 */
  private computeStateHash(): string {
    let hash = `${this.score}|`;
    for (let y = 0; y < SIZE; y++) {
      for (let x = 0; x < SIZE; x++) {
        hash += `${this.board[y][x]},`;
      }
    }
    return hash;
  }

  private redraw() {
    // 状态哈希检测：无变化则跳过渲染
    const stateHash = this.computeStateHash();
    if (stateHash === this.lastStateHash) return;
    this.lastStateHash = stateHash;

    // 首次渲染：创建背景
    if (!this.boardBg) {
      this.boardBg = this.add.graphics();
      this.boardBg.fillStyle(0xbbada0, 1);
      this.boardBg.fillRoundedRect(BOARD_X, BOARD_Y, BOARD_PX, BOARD_PX, 10);
      this.tileLayer.add(this.boardBg);
      this.boardBg.setDepth(-1);
    }

    // 首次渲染：创建所有tile精灵
    if (this.tileGrid.length === 0) {
      for (let y = 0; y < SIZE; y++) {
        const row: Array<{ bg: Phaser.GameObjects.Rectangle; text: Phaser.GameObjects.Text } | null> = [];
        for (let x = 0; x < SIZE; x++) {
          const px = BOARD_X + GAP + x * (CELL + GAP);
          const py = BOARD_Y + GAP + y * (CELL + GAP);
          
          const bg = this.add.rectangle(px + CELL / 2, py + CELL / 2, CELL, CELL, 0xcdc1b4, 0.6);
          bg.setStrokeStyle(0, 0);
          const text = this.add.text(px + CELL / 2, py + CELL / 2, '', {
            fontFamily: 'Arial', fontSize: '48px', fontStyle: 'bold', color: '#776e65',
          }).setOrigin(0.5);
          
          this.tileLayer.add([bg, text]);
          row.push({ bg, text });
        }
        this.tileGrid.push(row);
      }
    }

    // 增量更新：只更新变化的方块
    for (let y = 0; y < SIZE; y++) {
      for (let x = 0; x < SIZE; x++) {
        const v = this.board[y][x];
        const cell = this.tileGrid[y][x];
        if (!cell) continue;

        const c = TILE_COLORS[v] ?? { bg: 0x3c3a32, fg: '#ffffff' };
        
        if (v === 0) {
          // 空位：显示默认背景
          cell.bg.setFillStyle(0xcdc1b4, 0.6);
          cell.bg.setStrokeStyle(0, 0);
          cell.text.setText('');
        } else {
          // 有数字：显示对应颜色
          cell.bg.setFillStyle(c.bg, 1);
          cell.bg.setStrokeStyle(0, 0);
          cell.text.setText(String(v));
          cell.text.setColor(c.fg);
          
          const fontSize = v >= 1024 ? '32px' : v >= 128 ? '40px' : '48px';
          cell.text.setFontSize(fontSize);
        }
      }
    }
  }

  /**
   * 提示：高亮本步会合并的方块，并在滑动方向一侧标出箭头。
   *
   * 搜索只看棋盘、不含随机新块，所以建议的是“当前局面下最优的一步”，不是必胜法。
   */
  private showHint() {
    if (!this.started || this.over) return;
    const hint = findHint(this.board);
    if (!hint) {
      clearHint(this);
      showHintText(this, '已无路可走');
      return;
    }
    const cx = (x: number) => BOARD_X + GAP + x * (CELL + GAP) + CELL / 2;
    const cy = (y: number) => BOARD_Y + GAP + y * (CELL + GAP) + CELL / 2;
    // 箭头落在滑动方向一侧的棋盘内沿，不会被方块淹没、也不与其他顶栏元素相撞
    const at: Record<Direction, { x: number; y: number }> = {
      up: { x: cx(1.5), y: BOARD_Y + 30 },
      down: { x: cx(1.5), y: BOARD_Y + BOARD_PX - 30 },
      left: { x: BOARD_X + 30, y: cy(1.5) },
      right: { x: BOARD_X + BOARD_PX - 30, y: cy(1.5) },
    };
    const boxes = hint.merging.map(([x, y]) => cellBox(cx(x), cy(y), CELL, 6, 0x69f0ae));
    boxes.push(labelBox(at[hint.dir].x, at[hint.dir].y, arrowOf(hint.dir), 52));
    highlightBoxes(this, boxes);
    const pairs = hint.merging.length / 2;
    showHintText(this, `向${dirNameOf(hint.dir)}滑动 ${arrowOf(hint.dir)} · ` +
      (hint.gained > 0 ? `可合并 ${pairs} 对，本步 +${hint.gained} 分` : '这一步不为得分，只为把棋盘理顺'));
  }

  private showOverlay(title: string, canContinue: boolean) {
    clearHint(this);
    const c = this.add.container(640, 360).setDepth(200);
    const mask = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6).setInteractive();
    const panel = this.add.rectangle(0, 0, 560, 320, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    c.add([mask, panel]);
    c.add(this.add.text(0, -110, title, { ...FONT, fontSize: '32px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5));
    c.add(this.add.text(0, -40, `得分 ${this.score}    最高分 ${this.best}`, {
      ...FONT, fontSize: '20px',
    }).setOrigin(0.5));

    const mkBtn = (x: number, y: number, w: number, label: string, color: number, cb: () => void) => {
      const bg = this.add.rectangle(x, y, w, 54, color).setStrokeStyle(2, 0xffffff, 0.6).setInteractive({ useHandCursor: true });
      bg.on('pointerover', () => bg.setFillStyle(color + 0x222222));
      bg.on('pointerout', () => bg.setFillStyle(color));
      bg.on('pointerdown', cb);
      c.add([bg, this.add.text(x, y, label, { ...FONT, fontSize: '20px', color: '#fffde7' }).setOrigin(0.5)]);
    };
    if (canContinue) {
      mkBtn(0, 30, 240, '▶ 继续挑战', 0x1565c0, () => {
        c.destroy();
        this.overlay = undefined;
      });
      mkBtn(0, 100, 240, '🔄 重新开始', 0x2e7d32, () => this.newGame());
    } else {
      mkBtn(-115, 70, 190, '🔄 再来一局', 0x2e7d32, () => this.newGame());
      mkBtn(115, 70, 190, '🚪 返回大厅', 0xbf360c, () => this.exitToLobby());
    }
    this.overlay = c;
  }

  private exitToLobby() {
    store.setCurrentGame('ddz'); // 回到主游戏，避免大厅守卫再次进入本单机场景
    setFlowState(FlowState.LOBBY);
    this.scene.start('Lobby');
  }

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 2048是静态场景，不需要每帧更新
    // 所有变化都通过事件驱动（按键、点击等）
  }
}
