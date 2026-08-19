import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { addHintButton, arrowOf, cellBox, clearHint, dirNameOf, highlightBoxes, showHintText } from '../../platform/ui/hint';
import { COLS, ROWS, createGame, findHint, speed, step, turn, type Direction, type SnakeState } from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

const CELL = 20;
const BOARD_W = COLS * CELL;
const BOARD_H = ROWS * CELL;
const BOARD_X = 220;
const BOARD_Y = 80;
const PANEL_X = 1060;

/** 贪吃蛇（单机）：40×30 网格，穿墙模式，吃食物计分加速 */
export class SnakeScene extends Phaser.Scene {
  private st!: SnakeState;
  private paused = false;
  private started = false;
  private acc = 0;
  private lastTime = 0;

  private g!: Phaser.GameObjects.Graphics;
  private scoreText!: Phaser.GameObjects.Text;
  private lenText!: Phaser.GameObjects.Text;
  private pauseText!: Phaser.GameObjects.Text;
  private overlay?: Phaser.GameObjects.Container;

  constructor() {
    super('SnakeGame');
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/snake') location.hash = '/game/snake';

    this.buildTopBar();
    this.buildPanel();
    this.g = this.add.graphics();
    this.pauseText = this.add.text(BOARD_X + BOARD_W / 2, BOARD_Y + BOARD_H / 2, '⏸ 已暂停（空格 继续）', {
      ...FONT, fontSize: '24px', color: '#ffe082', backgroundColor: '#000000aa', padding: { x: 12, y: 8 },
    }).setOrigin(0.5).setDepth(100).setVisible(false);

    this.bindKeys();
    this.newGame();
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      this.input.keyboard?.removeAllListeners();
      this.input.keyboard?.removeCapture('LEFT,RIGHT,UP,DOWN,SPACE');
    });
  }

  private buildTopBar() {
    this.add.text(20, 18, '🐍 贪吃蛇', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });

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
      return this.add.text(PANEL_X, y + 24, '', { ...FONT, fontSize: '22px', color: '#ffe082' });
    };
    this.scoreText = mk(140, '得分');
    this.lenText = mk(220, '长度');
    this.add.text(PANEL_X, 320, '方向键 / WASD 转向\n空格 暂停', {
      ...FONT, fontSize: '14px', color: '#8899aa', lineSpacing: 6,
    });
  }

  private bindKeys() {
    const kb = this.input.keyboard;
    if (!kb) return;
    kb.addCapture('LEFT,RIGHT,UP,DOWN,SPACE');
    const dirMap: Record<string, Direction> = {
      'keydown-UP': 'up', 'keydown-W': 'up',
      'keydown-DOWN': 'down', 'keydown-S': 'down',
      'keydown-LEFT': 'left', 'keydown-A': 'left',
      'keydown-RIGHT': 'right', 'keydown-D': 'right',
    };
    for (const [ev, dir] of Object.entries(dirMap)) {
      kb.on(ev, () => { if (this.started && !this.st.over) turn(this.st, dir); });
    }
    kb.on('keydown-SPACE', () => { if (this.started) this.togglePause(); });
  }

  private newGame() {
    this.st = createGame();
    this.paused = false;
    this.started = false;
    this.acc = 0;
    this.lastTime = 0;
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
    const title = this.add.text(640, 240, '🐍 贪吃蛇', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '方向键 / WASD 转向 · 空格 暂停', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

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
      const rules = getGameRule('snake');
      if (rules) showGameRules(this, getGameName('snake'), rules);
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
    this.scoreText.setText(String(this.st.score));
    this.lenText.setText(String(this.st.snake.length));
  }

  private togglePause() {
    if (this.st.over) return;
    this.paused = !this.paused;
    this.pauseText.setVisible(this.paused);
    if (!this.paused) clearHint(this); // 跑起来之后旧推荐立即过期
  }

  /**
   * 提示：沿 BFS 最短路径高亮到食物的格子。
   *
   * 蛇在自动前进，不暂停的话高亮路径两帧就作废，所以点提示顺带把游戏停下来（空格可继续）。
   */
  private showHint() {
    if (!this.started || this.st.over) return;
    if (!this.paused) this.togglePause();
    const hint = findHint(this.st);
    if (!hint) {
      clearHint(this);
      showHintText(this, '已无安全的转向，这局到头了');
      return;
    }
    highlightBoxes(this, hint.path.map((p) => cellBox(BOARD_X + p.x * CELL + CELL / 2, BOARD_Y + p.y * CELL + CELL / 2, CELL, 1, 0x69f0ae)));
    const dir = `向${dirNameOf(hint.dir)}（${arrowOf(hint.dir)}）`;
    showHintText(this, hint.reachable
      ? `已暂停·${dir}走，沿高亮 ${hint.path.length} 步可吃到食物`
      : `已暂停·食物暂时不可达，先${dir}走保住蛇身`);
  }

  update(time: number) {
    if (!this.started) { this.lastTime = time; return; }
    const delta = this.lastTime === 0 ? 0 : time - this.lastTime;
    this.lastTime = time;
    if (this.st.over || this.paused) return;
    this.acc += delta;
    const interval = speed(this.st.score);
    if (this.acc >= interval) {
      this.acc = 0;
      step(this.st);
      this.updatePanel();
      this.redraw();
      if (this.st.over) this.showOverlay('💥 游戏结束');
    }
  }

  private redraw() {
    const g = this.g;
    g.clear();
    g.fillStyle(0x000000, 0.3);
    g.fillRect(BOARD_X, BOARD_Y, BOARD_W, BOARD_H);
    g.lineStyle(1, 0xffffff, 0.06);
    for (let x = 1; x < COLS; x++) {
      g.lineBetween(BOARD_X + x * CELL, BOARD_Y, BOARD_X + x * CELL, BOARD_Y + BOARD_H);
    }
    for (let y = 1; y < ROWS; y++) {
      g.lineBetween(BOARD_X, BOARD_Y + y * CELL, BOARD_X + BOARD_W, BOARD_Y + y * CELL);
    }
    g.lineStyle(2, 0xffd54f, 0.6);
    g.strokeRect(BOARD_X, BOARD_Y, BOARD_W, BOARD_H);

    // 食物
    if (this.st.food.x >= 0) {
      g.fillStyle(0xff5252, 1);
      g.fillCircle(BOARD_X + this.st.food.x * CELL + CELL / 2, BOARD_Y + this.st.food.y * CELL + CELL / 2, CELL / 2 - 3);
    }

    // 蛇身（头部更亮）
    this.st.snake.forEach((p, i) => {
      g.fillStyle(i === 0 ? 0xb9f6ca : 0x4caf50, 1);
      g.fillRoundedRect(BOARD_X + p.x * CELL + 1, BOARD_Y + p.y * CELL + 1, CELL - 2, CELL - 2, 4);
    });
  }

  private showOverlay(title: string) {
    clearHint(this);
    const c = this.add.container(640, 360).setDepth(200);
    const mask = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6).setInteractive();
    const panel = this.add.rectangle(0, 0, 520, 300, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    c.add([mask, panel]);
    c.add(this.add.text(0, -100, title, { ...FONT, fontSize: '32px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5));
    c.add(this.add.text(0, -30, `得分 ${this.st.score}    长度 ${this.st.snake.length}`, {
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
