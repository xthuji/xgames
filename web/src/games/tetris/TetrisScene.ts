import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { addHintButton, cellBox, clearHint, highlightBoxes, showHintText } from '../../platform/ui/hint';
import {
  ActivePiece,
  Board,
  COLS,
  LINE_SCORES,
  MAX_LEVEL,
  clearRows,
  cells,
  createBoard,
  findHint,
  fullRows,
  ghostY,
  isLegal,
  isTopOut,
  levelSpeed,
  lock,
  randomPiece,
} from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

// 掌机风格：10×20 格，较小的格子尺寸
const CELL = 24;
const BOARD_PX_W = COLS * CELL;
const ROWS = 20;
const BOARD_PX_H = ROWS * CELL;
const BOARD_X = 430;
const BOARD_Y = 80;
const PANEL_X = 800;
const HI_KEY = 'xgames_tetris_hiscore';

// 掌机黑白配色
const MONO_BG = 0x1a1a1a;
const MONO_GRID = 0x333333;
const MONO_CELL_FILL = 0xe8e8e8;
const MONO_CELL_EDGE = 0x000000;
const MONO_GHOST = 0x555555;

/** 俄罗斯方块（单机）：经典 10×20，坐标表旋转（无踢墙），幽灵投影，软硬降，消行计分升级 */
export class TetrisScene extends Phaser.Scene {
  private board!: Board;
  private cur!: ActivePiece;
  private nextPiece = randomPiece();
  private score = 0;
  private hiScore = 0;
  private lines = 0;
  private level = 1;
  private over = false;
  private paused = false;
  private started = false;
  private soft = false;
  private acc = 0;
  private lastTime = 0;

  // DAS 自动重复系统
  private keyLeft = false;
  private keyRight = false;
  private keyTimer = 0;
  private keyDelay = 0;   // 下一次自动重复剩余时间（ms）
  private keyRepeatInterval = 50; // ARR 自动重复间隔 ms

  private g!: Phaser.GameObjects.Graphics;
  private scoreText!: Phaser.GameObjects.Text;
  private hiText!: Phaser.GameObjects.Text;
  private levelText!: Phaser.GameObjects.Text;
  private linesText!: Phaser.GameObjects.Text;
  private pauseText!: Phaser.GameObjects.Text;
  private overlay?: Phaser.GameObjects.Container;

  constructor() {
    super('TetrisGame');
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/tetris') location.hash = '/game/tetris';
    this.hiScore = Number(localStorage.getItem(HI_KEY) ?? 0) || 0;

    this.buildTopBar();
    this.buildPanel();
    this.g = this.add.graphics();
    this.pauseText = this.add.text(BOARD_X + BOARD_PX_W / 2, BOARD_Y + BOARD_PX_H / 2, '⏸ 已暂停（Enter 继续）', {
      ...FONT, fontSize: '24px', color: '#ffe082', backgroundColor: '#000000aa', padding: { x: 12, y: 8 },
    }).setOrigin(0.5).setDepth(100).setVisible(false);

    this.bindKeys();
    this.newGame();
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      this.input.keyboard?.removeAllListeners();
      this.input.keyboard?.removeCapture('LEFT,RIGHT,UP,DOWN,SPACE,ENTER');
    });
  }

  private buildTopBar() {
    this.add.text(20, 18, '🧱 俄罗斯方块', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });

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
    this.add.text(PANEL_X, 100, '下一个', { ...FONT, fontSize: '18px', color: '#aac7ff' });
    this.add.rectangle(PANEL_X + 60, 175, 130, 110, 0x000000, 0.35).setStrokeStyle(1, 0xffffff, 0.3);
    const mk = (y: number, label: string) => {
      this.add.text(PANEL_X, y, label, { ...FONT, fontSize: '16px', color: '#aac7ff' });
      return this.add.text(PANEL_X, y + 24, '', { ...FONT, fontSize: '22px', color: '#ffe082' });
    };
    this.scoreText = mk(320, '得分');
    this.hiText = mk(390, '最高分');
    this.levelText = mk(460, '等级');
    this.linesText = mk(530, '消行');
    this.add.text(PANEL_X, 620, '← → 移动   ↑ 变形\n↓ 加速下落   空格 直落到底\nEnter 暂停', {
      ...FONT, fontSize: '14px', color: '#8899aa', lineSpacing: 6,
    });
  }

  private bindKeys() {
    const kb = this.input.keyboard;
    if (!kb) return;
    kb.addCapture('LEFT,RIGHT,UP,DOWN,SPACE,ENTER');

    // 方向键：使用状态追踪 + DAS 自动重复系统
    kb.on('keydown-LEFT', () => {
      if (this.started && !this.keyLeft) {
        this.keyLeft = true;
        this.keyDelay = 167; // DAS 初始延迟 167ms
        this.move(-1);
      }
    });
    kb.on('keydown-RIGHT', () => {
      if (this.started && !this.keyRight) {
        this.keyRight = true;
        this.keyDelay = 167;
        this.move(1);
      }
    });
    kb.on('keyup-LEFT', () => { this.keyLeft = false; if (!this.keyRight) this.keyDelay = 0; });
    kb.on('keyup-RIGHT', () => { this.keyRight = false; if (!this.keyLeft) this.keyDelay = 0; });

    kb.on('keydown-UP', () => { if (this.started) this.rotate(); });
    kb.on('keydown-DOWN', () => { if (this.started) this.soft = true; });
    kb.on('keyup-DOWN', () => { this.soft = false; });
    kb.on('keydown-SPACE', () => { if (this.started) this.hardDrop(); });
    kb.on('keydown-ENTER', () => { if (this.started) this.togglePause(); });
  }

  private newGame() {
    this.board = createBoard();
    this.score = 0;
    this.lines = 0;
    this.level = 1;
    this.over = false;
    this.paused = false;
    this.started = false;
    this.soft = false;
    this.acc = 0;
    this.lastTime = 0;
    this.nextPiece = randomPiece();
    this.spawn();
    clearHint(this);
    this.overlay?.destroy();
    this.overlay = undefined;
    this.pauseText.setVisible(false);
    this.updatePanel();
    this.redraw();
    this.showStartOverlay();
  }

  private showStartOverlay() {
    const container = this.add.container(0, 0).setDepth(200);
    const bg = this.add.rectangle(640, 360, 1280, 720, 0x000000, 0.55);
    const title = this.add.text(640, 240, '🧱 俄罗斯方块', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '← → 移动 · ↑ 变形 · ↓ 加速 · 空格 直落', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

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
      const rules = getGameRule('tetris');
      if (rules) showGameRules(this, getGameName('tetris'), rules);
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

  private spawn() {
    this.cur = { piece: this.nextPiece, rot: 0, x: 3, y: 0 };
    this.nextPiece = randomPiece();
    if (!isLegal(cells(this.cur), this.board)) {
      this.gameOver();
    }
  }

  private updatePanel() {
    this.scoreText.setText(String(this.score));
    this.hiText.setText(String(this.hiScore));
    this.levelText.setText(String(this.level));
    this.linesText.setText(String(this.lines));
  }

  // ── 操作 ──

  private move(dx: number) {
    if (this.over || this.paused) return;
    clearHint(this); // 块已挪位，高亮的落点不再对应当前形状
    const moved = { ...this.cur, x: this.cur.x + dx };
    if (isLegal(cells(moved), this.board)) {
      this.cur = moved;
      this.redraw();
    }
  }

  private rotate() {
    if (this.over || this.paused) return;
    clearHint(this);
    const rotated = { ...this.cur, rot: (this.cur.rot + 1) % 4 };
    if (isLegal(cells(rotated), this.board)) {
      this.cur = rotated;
      this.redraw();
    }
  }

  private hardDrop() {
    if (this.over || this.paused) return;
    clearHint(this);
    this.cur.y = ghostY(this.board, this.cur);
    this.acc = 0;
    this.lockAndNext();
  }

  private togglePause() {
    if (this.over) return;
    this.paused = !this.paused;
    this.pauseText.setVisible(this.paused);
    if (!this.paused) clearHint(this); // 继续下落，旧落点立即作废
  }

  /**
   * 提示：按启发分挑出最优硬降落点，高亮落定后的形状。
   *
   * 方块一直在下落，不暂停的话高亮两秒就变成谎言，所以点提示顺带暂停（Enter 继续）。
   */
  private showHint() {
    if (!this.started || this.over) return;
    if (!this.paused) this.togglePause();
    const hint = findHint(this.board, this.cur.piece, this.cur.rot);
    if (!hint) {
      clearHint(this);
      showHintText(this, '已没有可堆入的位置了');
      return;
    }
    highlightBoxes(this, hint.landing
      .filter(([, y]) => y >= 0)
      .map(([x, y]) => cellBox(BOARD_X + x * CELL + CELL / 2, BOARD_Y + y * CELL + CELL / 2, CELL, 1, 0x69f0ae)));
    const go = hint.rotates > 0 ? `按 ↑×${hint.rotates} 旋转再移到第 ${hint.col + 1} 列` : `直接移到第 ${hint.col + 1} 列`;
    showHintText(this, `已暂停·${go}，空格直落 · ${hint.cleared > 0 ? `可消 ${hint.cleared} 行` : '先把堆面垫平'}`);
  }

  // ── 主循环 ──

  update(time: number) {
    if (!this.started) { this.lastTime = time; return; }
    const delta = this.lastTime === 0 ? 0 : time - this.lastTime;
    this.lastTime = time;
    if (this.over || this.paused) return;

    // DAS 自动重复：处理方向键长按
    const dir = (this.keyRight ? 1 : 0) - (this.keyLeft ? 1 : 0);
    if (dir !== 0) {
      this.keyTimer += delta;
      if (this.keyDelay > 0) {
        // DAS 延迟阶段
        if (this.keyTimer >= this.keyDelay) {
          this.keyTimer -= this.keyDelay;
          this.keyDelay = 0;
          this.move(dir);
          this.keyTimer = 0;
        }
      } else {
        // ARR 自动重复阶段
        while (this.keyTimer >= this.keyRepeatInterval) {
          this.keyTimer -= this.keyRepeatInterval;
          this.move(dir);
        }
      }
    } else {
      this.keyTimer = 0;
    }

    // 重力下落
    this.acc += delta;
    const interval = this.soft ? 60 : levelSpeed(this.level);
    if (this.acc >= interval) {
      this.acc = 0;
      const down = { ...this.cur, y: this.cur.y + 1 };
      if (isLegal(cells(down), this.board)) {
        this.cur = down;
        this.redraw();
      } else {
        this.lockAndNext();
      }
    }
  }

  private lockAndNext() {
    lock(this.board, this.cur);
    if (isTopOut(this.cur)) {
      this.redraw();
      this.gameOver();
      return;
    }
    const rows = fullRows(this.board);
    if (rows.length > 0) {
      clearRows(this.board, rows);
      this.score += LINE_SCORES[rows.length];
      this.lines += rows.length;
      this.level = Math.min(MAX_LEVEL, 1 + Math.floor(this.lines / 10));
      if (this.score > this.hiScore) {
        this.hiScore = this.score;
        localStorage.setItem(HI_KEY, String(this.hiScore));
      }
    }
    this.spawn();
    this.updatePanel();
    this.redraw();
  }

  private gameOver() {
    this.over = true;
    clearHint(this);
    this.updatePanel();
    this.showOverlay('💥 游戏结束');
  }

  // ── 渲染 ──

  private redraw() {
    const g = this.g;
    g.clear();
    // 掌机风格：深色背景 + 网格
    g.fillStyle(MONO_BG, 1);
    g.fillRect(BOARD_X, BOARD_Y, BOARD_PX_W, BOARD_PX_H);
    g.lineStyle(1, MONO_GRID, 1);
    for (let x = 0; x <= COLS; x++) {
      g.lineBetween(BOARD_X + x * CELL, BOARD_Y, BOARD_X + x * CELL, BOARD_Y + BOARD_PX_H);
    }
    for (let y = 0; y <= ROWS; y++) {
      g.lineBetween(BOARD_X, BOARD_Y + y * CELL, BOARD_X + BOARD_PX_W, BOARD_Y + y * CELL);
    }
    // 外边框
    g.lineStyle(2, 0x666666, 1);
    g.strokeRect(BOARD_X, BOARD_Y, BOARD_PX_W, BOARD_PX_H);

    // 已落方块（掌机黑白）
    for (let y = 0; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        const p = this.board[y][x];
        if (p) this.drawCell(x, y, false);
      }
    }

    if (!this.over) {
      // 幽灵投影（暗色）
      const gy = ghostY(this.board, this.cur);
      for (const [x, y] of cells({ ...this.cur, y: gy })) {
        if (y >= 0) this.drawGhostCell(x, y);
      }
      // 当前方块
      for (const [x, y] of cells(this.cur)) {
        if (y >= 0) this.drawCell(x, y, true);
      }
    }

    // 下一块预览（黑白）
    const preview: ActivePiece = { piece: this.nextPiece, rot: 0, x: 0, y: 0 };
    const list = cells(preview);
    const minX = Math.min(...list.map(([x]) => x));
    const maxX = Math.max(...list.map(([x]) => x));
    const minY = Math.min(...list.map(([, y]) => y));
    const maxY = Math.max(...list.map(([, y]) => y));
    const s = 18;
    const ox = PANEL_X + 60 - ((maxX - minX + 1) * s) / 2;
    const oy = 175 - ((maxY - minY + 1) * s) / 2;
    g.fillStyle(MONO_CELL_FILL, 1);
    for (const [x, y] of list) {
      g.fillRect(ox + (x - minX) * s + 1, oy + (y - minY) * s + 1, s - 2, s - 2);
    }
  }

  private drawCell(x: number, y: number, isActive: boolean) {
    const px = BOARD_X + x * CELL + 1;
    const py = BOARD_Y + y * CELL + 1;
    const size = CELL - 2;
    // 填充
    this.g.fillStyle(isActive ? MONO_CELL_FILL : 0xcccccc, 1);
    this.g.fillRect(px, py, size, size);
    // 黑色边框（掌机像素风格）
    this.g.lineStyle(1, MONO_CELL_EDGE, 1);
    this.g.strokeRect(px, py, size, size);
  }

  private drawGhostCell(x: number, y: number) {
    const px = BOARD_X + x * CELL + 2;
    const py = BOARD_Y + y * CELL + 2;
    const size = CELL - 4;
    this.g.lineStyle(1, MONO_GHOST, 1);
    this.g.strokeRect(px, py, size, size);
  }

  // ── 结算 ──

  private showOverlay(title: string) {
    const c = this.add.container(640, 360).setDepth(200);
    const mask = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6).setInteractive();
    const panel = this.add.rectangle(0, 0, 520, 300, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    c.add([mask, panel]);
    c.add(this.add.text(0, -100, title, { ...FONT, fontSize: '32px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5));
    c.add(this.add.text(0, -30, `得分 ${this.score}    最高分 ${this.hiScore}    消行 ${this.lines}    等级 ${this.level}`, {
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
