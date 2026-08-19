import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { addHintButton, cellBox, clearHint, highlightBoxes, showHintText } from '../../platform/ui/hint';
import {
  COLS,
  ROWS,
  createBoard,
  findAnyPair,
  findPath,
  remaining,
  reshuffle,
  type Board,
  type Point,
} from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

const ICONS = ['🍎', '🍌', '🍇', '🍒', '🍑', '🍍', '🥝', '🍓', '🍋', '🍉', '🌽', '🍆'];
const CELL = 46;
/** 棋盘外留一圈空格供连线绕行 */
const BOARD_W = (COLS + 2) * CELL;
const BOARD_H = (ROWS + 2) * CELL;
const BOARD_X = (1280 - BOARD_W) / 2;
const BOARD_Y = 100;

/** 连连看（单机）：两拐角以内折线相连的同图案方块可消除，清空即胜 */
export class LinkLinkScene extends Phaser.Scene {
  private board!: Board;
  private score = 0;
  private seconds = 0;
  private selected: Point | null = null;
  private finished = false;

  private tiles!: Phaser.GameObjects.Container;
  private lineG!: Phaser.GameObjects.Graphics;
  private scoreText!: Phaser.GameObjects.Text;
  private timeText!: Phaser.GameObjects.Text;
  private restText!: Phaser.GameObjects.Text;
  private timerEvent?: Phaser.Time.TimerEvent;
  private overlay?: Phaser.GameObjects.Container;

  // === 渲染优化：持久化精灵 + 增量更新 ===
  /** 方块精灵网格 [row][col] -> {bg, icon} */
  private tileGrid: Array<Array<{ bg: Phaser.GameObjects.Rectangle; icon: Phaser.GameObjects.Text } | null>> = [];
  /** 上一帧的游戏状态哈希 */
  private lastStateHash = '';

  constructor() {
    super('LinkGame');
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/link') location.hash = '/game/link';

    this.buildTopBar();
    this.tiles = this.add.container(0, 0);
    this.lineG = this.add.graphics().setDepth(50);
    this.buildStatus();
    this.newGame();

    this.input.on('pointerdown', (p: Phaser.Input.Pointer) => this.onClick(p));
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      this.input.removeAllListeners();
    });
  }

  private buildTopBar() {
    this.add.text(20, 18, '🔗 连连看', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });

    const mkBtn = (x: number, label: string, cb: () => void) => {
      const t = this.add.text(x, 24, label, {
        ...FONT, fontSize: '16px', color: '#ffd54f', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
      }).setOrigin(0.5).setInteractive({ useHandCursor: true });
      t.on('pointerdown', cb);
    };
    addHintButton(this, 880, () => this.showHint());
    mkBtn(990, '🔀 重排', () => this.doShuffle());
    mkBtn(1110, '🔄 重新开始', () => this.newGame());
    const exitBtn = this.add.text(1260, 24, '🚪 退出', {
      ...FONT, fontSize: '16px', color: '#ff8a80', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
    }).setOrigin(0.5).setInteractive({ useHandCursor: true });
    exitBtn.on('pointerdown', () => this.exitToLobby());
  }

  private buildStatus() {
    this.scoreText = this.add.text(BOARD_X, BOARD_Y + BOARD_H + 14, '得分 0', { ...FONT, fontSize: '20px', color: '#ffe082' });
    this.restText = this.add.text(BOARD_X + 200, BOARD_Y + BOARD_H + 14, '', { ...FONT, fontSize: '20px', color: '#aac7ff' });
    this.timeText = this.add.text(BOARD_X + BOARD_W - 120, BOARD_Y + BOARD_H + 14, '⏱ 00:00', { ...FONT, fontSize: '20px', color: '#ffffff' });
  }

  private newGame() {
    this.board = createBoard();
    this.score = 0;
    this.seconds = 0;
    this.selected = null;
    this.finished = false;
    clearHint(this);
    this.overlay?.destroy();
    this.overlay = undefined;
    this.lineG.clear();
    this.timeText.setText('⏱ 00:00');
    this.timerEvent?.remove(false);
    this.timerEvent = undefined;  // 延迟到开始按钮后再创建
    
    // 清理旧的tile网格
    this.clearTileGrid();
    
    this.updateStatus();
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
          cell.icon.destroy();
        }
      }
    }
    this.tileGrid = [];
    this.lastStateHash = '';
  }

  private showStartOverlay() {
    const container = this.add.container(0, 0).setDepth(200);
    const bg = this.add.rectangle(640, 360, 1280, 720, 0x000000, 0.55);
    const title = this.add.text(640, 240, '🔗 连连看', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '两拐角以内折线相连的同图案方块可消除', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

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
      const rules = getGameRule('link');
      if (rules) showGameRules(this, getGameName('link'), rules);
    });

    // 开始按钮
    const startBtn = this.add.text(140, 0, '🚀 开始游戏', { ...FONT, fontSize: '28px', color: '#ffe082', backgroundColor: '#000000aa', padding: { x: 28, y: 14 } })
      .setOrigin(0.5).setInteractive({ useHandCursor: true });
    startBtn.on('pointerover', () => startBtn.setStyle({ ...startBtn.style, color: '#fff176' }));
    startBtn.on('pointerout', () => startBtn.setStyle({ ...startBtn.style, color: '#ffe082' }));
    startBtn.on('pointerdown', () => {
      container.destroy();
      this.overlay = undefined;
      this.timerEvent = this.time.addEvent({ delay: 5000, loop: true, callback: () => this.tick() }); // 5秒更新一次，降低CPU占用
    });

    btnContainer.add([ruleBtnBg, ruleBtnText, startBtn]);
    container.add([bg, title, hint, btnContainer]);
    this.overlay = container;
  }

  private tick() {
    if (this.finished) return;
    this.seconds += 5; // 每次增加5秒
    const m = String(Math.floor(this.seconds / 60)).padStart(2, '0');
    const s = String(this.seconds % 60).padStart(2, '0');
    this.timeText.setText(`⏱ ${m}:${s}`);
  }

  private updateStatus() {
    this.scoreText.setText(`得分 ${this.score}`);
    this.restText.setText(`剩余 ${remaining(this.board)}`);
  }

  private cellPos(p: Point): { px: number; py: number } {
    return { px: BOARD_X + (p.x + 1) * CELL, py: BOARD_Y + (p.y + 1) * CELL };
  }

  private onClick(pointer: Phaser.Input.Pointer) {
    if (this.finished || this.overlay) return;
    const x = Math.floor((pointer.x - BOARD_X) / CELL) - 1;
    const y = Math.floor((pointer.y - BOARD_Y) / CELL) - 1;
    if (x < 0 || x >= COLS || y < 0 || y >= ROWS) return;
    if (this.board[y][x] === 0) return;

    const p: Point = { x, y };
    if (!this.selected) {
      this.selected = p;
      this.redraw();
      return;
    }
    if (this.selected.x === x && this.selected.y === y) {
      this.selected = null;
      this.redraw();
      return;
    }
    const path = findPath(this.board, this.selected, p);
    if (path) {
      this.removePair(path, this.selected, p);
    } else {
      this.selected = p; // 无法相连：改选新方块
      this.redraw();
    }
  }

  private removePair(path: Point[], a: Point, b: Point) {
    this.selected = null;
    clearHint(this);
    this.drawPath(path);
    this.board[a.y][a.x] = 0;
    this.board[b.y][b.x] = 0;
    this.score += 10;
    this.time.delayedCall(260, () => {
      this.lineG.clear();
      if (remaining(this.board) === 0) {
        this.finished = true;
        this.updateStatus();
        this.redraw();
        this.showOverlay('🎉 全部消除');
        return;
      }
      if (!findAnyPair(this.board)) {
        reshuffle(this.board); // 死局自动重排
      }
      this.updateStatus();
      this.redraw();
    }, [], this);
    this.updateStatus();
    this.redraw();
  }

  private drawPath(path: Point[]) {
    const g = this.lineG;
    g.clear();
    g.lineStyle(4, 0xffe082, 0.95);
    for (let i = 0; i + 1 < path.length; i++) {
      const s = this.cellPos(path[i]);
      const e = this.cellPos(path[i + 1]);
      g.lineBetween(s.px + CELL / 2, s.py + CELL / 2, e.px + CELL / 2, e.py + CELL / 2);
    }
  }

  /** 提示：高亮一对当前可相连的同图案（与其他单机游戏同款表现） */
  private showHint() {
    if (this.finished || this.overlay) return;
    const pair = findAnyPair(this.board);
    if (!pair) {
      clearHint(this);
      showHintText(this, '已没有可相连的一对，点「🔀 重排」洗牌');
      return;
    }
    const [a, b] = pair;
    highlightBoxes(this, [a, b].map((p) => {
      const { px, py } = this.cellPos(p);
      return cellBox(px + CELL / 2, py + CELL / 2, CELL, 1, 0x69f0ae);
    }));
    showHintText(this, `这两个「${ICONS[(this.board[a.y][a.x] - 1) % ICONS.length]}」连线最多拐两个弯，依次点一下即可消除`);
  }

  private doShuffle() {
    if (this.finished) return;
    this.selected = null;
    clearHint(this);
    reshuffle(this.board);
    this.redraw();
  }

  /** 计算状态哈希（只检查关键变化点） */
  private computeStateHash(): string {
    let hash = `${this.score}|${this.seconds}|${this.selected ? `${this.selected.x},${this.selected.y}` : 'n'}|`;
    for (let y = 0; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        const v = this.board[y][x];
        if (v !== 0) hash += `${y},${x},${v};`;
      }
    }
    return hash;
  }

  private redraw() {
    // 状态哈希检测：无变化则跳过渲染
    const stateHash = this.computeStateHash();
    if (stateHash === this.lastStateHash) return;
    this.lastStateHash = stateHash;

    // 首次渲染：创建所有基础结构
    if (this.tileGrid.length === 0) {
      for (let y = 0; y < ROWS; y++) {
        const row: Array<{ bg: Phaser.GameObjects.Rectangle; icon: Phaser.GameObjects.Text } | null> = [];
        for (let x = 0; x < COLS; x++) {
          const { px, py } = this.cellPos({ x, y });
          const bg = this.add.rectangle(px + CELL / 2, py + CELL / 2, CELL - 4, CELL - 4, 0x26384a)
            .setStrokeStyle(1, 0xffffff, 0.25);
          const icon = this.add.text(px + CELL / 2, py + CELL / 2, '', {
            fontFamily: 'Arial', fontSize: '26px',
          }).setOrigin(0.5);
          this.tiles.add([bg, icon]);
          row.push({ bg, icon });
        }
        this.tileGrid.push(row);
      }
    }

    // 增量更新：只更新变化的方块
    for (let y = 0; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        const v = this.board[y][x];
        const cell = this.tileGrid[y][x];
        if (!cell) continue;

        const selected = this.selected?.x === x && this.selected?.y === y;

        if (v === 0) {
          // 空位：隐藏
          cell.bg.setVisible(false);
          cell.icon.setText('');
        } else {
          // 有方块：显示并更新样式
          cell.bg.setVisible(true);
          cell.bg.setFillStyle(0x26384a, 1);
          cell.bg.setStrokeStyle(selected ? 3 : 1, selected ? 0xffe082 : 0xffffff, selected ? 1 : 0.25);
          cell.icon.setText(ICONS[(v - 1) % ICONS.length]);
        }
      }
    }
  }

  private showOverlay(title: string) {
    clearHint(this);
    const c = this.add.container(640, 360).setDepth(200);
    const mask = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6).setInteractive();
    const panel = this.add.rectangle(0, 0, 520, 300, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    c.add([mask, panel]);
    c.add(this.add.text(0, -100, title, { ...FONT, fontSize: '32px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5));
    const m = String(Math.floor(this.seconds / 60)).padStart(2, '0');
    const s = String(this.seconds % 60).padStart(2, '0');
    c.add(this.add.text(0, -30, `得分 ${this.score}    用时 ${m}:${s}`, {
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

  /** 空update方法，避免Phaser每帧检查开销 */
  update() {
    // 连连看是静态场景，不需要每帧更新
    // 所有变化都通过事件驱动（点击、计时器等）
  }
}
