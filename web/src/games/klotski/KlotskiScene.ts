import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { arrowOf, clearHint, highlightBoxes, labelBox, showHintText, type HintBox } from '../../platform/ui/hint';
import {
  COLS,
  ROWS,
  LAYOUTS,
  canMove,
  createPieces,
  findHint,
  isWin,
  movePiece,
  type Piece,
} from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

const CELL = 96;
const BOARD_W = COLS * CELL;
const BOARD_H = ROWS * CELL;
const BOARD_X = (1280 - BOARD_W) / 2;
const BOARD_Y = 130;

const KIND_COLORS: Record<Piece['kind'], number> = {
  caocao: 0xc62828,
  horizontal: 0x1565c0,
  vertical: 0x2e7d32,
  pawn: 0x8d6e63,
};

/** 华容道（单机）：点击选子，方向键/按钮移动，把曹操移到底部中央出口 */
export class KlotskiScene extends Phaser.Scene {
  private pieces: Piece[] = [];
  private layoutIdx = 0;
  private selected: Piece | null = null;
  private moves = 0;
  private seconds = 0;
  private won = false;
  private started = false;

  private layer!: Phaser.GameObjects.Container;
  private layoutText!: Phaser.GameObjects.Text;
  private movesText!: Phaser.GameObjects.Text;
  private timeText!: Phaser.GameObjects.Text;
  private timerEvent?: Phaser.Time.TimerEvent;
  private overlay?: Phaser.GameObjects.Container;

  constructor() {
    super('KlotskiGame');
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/klotski') location.hash = '/game/klotski';

    this.buildTopBar();
    this.buildStatus();
    this.buildPad();
    this.layer = this.add.container(0, 0);

    this.bindKeys();
    this.newGame();
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      this.input.keyboard?.removeAllListeners();
      this.input.keyboard?.removeCapture('LEFT,RIGHT,UP,DOWN');
      this.input.removeAllListeners();
    });
  }

  private buildTopBar() {
    this.add.text(20, 18, '🧩 华容道', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });
    this.layoutText = this.add.text(640, 24, '', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

    const mkBtn = (x: number, label: string, cb: () => void) => {
      const t = this.add.text(x, 24, label, {
        ...FONT, fontSize: '16px', color: '#ffd54f', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
      }).setOrigin(0.5).setInteractive({ useHandCursor: true });
      t.on('pointerdown', cb);
    };
    mkBtn(760, '💡 提示', () => this.showHint());
    mkBtn(880, '🔀 换布局', () => {
      this.layoutIdx = (this.layoutIdx + 1) % LAYOUTS.length;
      this.newGame();
    });
    mkBtn(1010, '🔄 重新开始', () => this.newGame());
    const exitBtn = this.add.text(1260, 24, '🚪 退出', {
      ...FONT, fontSize: '16px', color: '#ff8a80', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
    }).setOrigin(0.5).setInteractive({ useHandCursor: true });
    exitBtn.on('pointerdown', () => this.exitToLobby());
  }

  private buildStatus() {
    this.movesText = this.add.text(BOARD_X, BOARD_Y + BOARD_H + 16, '步数 0', { ...FONT, fontSize: '20px', color: '#ffe082' });
    this.timeText = this.add.text(BOARD_X + BOARD_W - 110, BOARD_Y + BOARD_H + 16, '⏱ 00:00', { ...FONT, fontSize: '20px', color: '#ffffff' });
  }

  /** 屏幕方向按钮（无键盘也能玩） */
  private buildPad() {
    const cx = BOARD_X + BOARD_W + 150;
    const cy = BOARD_Y + BOARD_H / 2;
    const mk = (x: number, y: number, label: string, dx: number, dy: number) => {
      const bg = this.add.rectangle(cx + x, cy + y, 64, 64, 0x26384a).setStrokeStyle(1, 0xffffff, 0.4).setInteractive({ useHandCursor: true });
      bg.on('pointerdown', () => { if (this.started) this.moveSelected(dx, dy); });
      this.add.text(cx + x, cy + y, label, { ...FONT, fontSize: '26px' }).setOrigin(0.5);
    };
    mk(0, -70, '▲', 0, -1);
    mk(-70, 0, '◀', -1, 0);
    mk(70, 0, '▶', 1, 0);
    mk(0, 70, '▼', 0, 1);
    this.add.text(cx, cy + 150, '点击选子后\n按方向移动', { ...FONT, fontSize: '14px', color: '#8899aa', align: 'center', lineSpacing: 6 }).setOrigin(0.5);
  }

  private bindKeys() {
    const kb = this.input.keyboard;
    if (!kb) return;
    kb.addCapture('LEFT,RIGHT,UP,DOWN');
    const dirMap: Record<string, [number, number]> = {
      'keydown-UP': [0, -1], 'keydown-W': [0, -1],
      'keydown-DOWN': [0, 1], 'keydown-S': [0, 1],
      'keydown-LEFT': [-1, 0], 'keydown-A': [-1, 0],
      'keydown-RIGHT': [1, 0], 'keydown-D': [1, 0],
    };
    for (const [ev, [dx, dy]] of Object.entries(dirMap)) {
      kb.on(ev, () => { if (this.started) this.moveSelected(dx, dy); });
    }
  }

  private newGame() {
    this.pieces = createPieces(LAYOUTS[this.layoutIdx]);
    this.selected = null;
    this.moves = 0;
    this.seconds = 0;
    this.won = false;
    this.started = false;
    clearHint(this);
    this.overlay?.destroy();
    this.overlay = undefined;
    this.layoutText.setText(`布局：${LAYOUTS[this.layoutIdx].name}`);
    this.timeText.setText('⏱ 00:00');
    this.timerEvent?.remove(false);
    this.timerEvent = undefined;  // 延迟到开始按钮后再创建
    this.movesText.setText('步数 0');
    this.redraw();
    this.showStartOverlay();
  }

  private showStartOverlay() {
    const container = this.add.container(0, 0).setDepth(200);
    const bg = this.add.rectangle(640, 360, 1280, 720, 0x000000, 0.55);
    const title = this.add.text(640, 240, '🧩 华容道', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '点击选子 · 方向键/按钮移动 · 曹操到底部出口即胜', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

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
      const rules = getGameRule('klotski');
      if (rules) showGameRules(this, getGameName('klotski'), rules);
    });

    // 开始按钮
    const startBtn = this.add.text(140, 0, '🚀 开始游戏', { ...FONT, fontSize: '28px', color: '#ffe082', backgroundColor: '#000000aa', padding: { x: 28, y: 14 } })
      .setOrigin(0.5).setInteractive({ useHandCursor: true });
    startBtn.on('pointerover', () => startBtn.setStyle({ ...startBtn.style, color: '#fff176' }));
    startBtn.on('pointerout', () => startBtn.setStyle({ ...startBtn.style, color: '#ffe082' }));
    startBtn.on('pointerdown', () => {
      this.started = true;
      container.destroy();
      this.overlay = undefined;
      this.timerEvent = this.time.addEvent({ delay: 5000, loop: true, callback: () => this.tick() }); // 5秒更新一次，降低CPU占用
    });

    btnContainer.add([ruleBtnBg, ruleBtnText, startBtn]);
    container.add([bg, title, hint, btnContainer]);
    this.overlay = container;
  }

  private tick() {
    if (this.won) return;
    this.seconds += 5; // 每次增加5秒
    const m = String(Math.floor(this.seconds / 60)).padStart(2, '0');
    const s = String(this.seconds % 60).padStart(2, '0');
    this.timeText.setText(`⏱ ${m}:${s}`);
  }

  private moveSelected(dx: number, dy: number) {
    if (this.won || !this.selected || !this.started) return;
    if (!canMove(this.pieces, this.selected, dx, dy)) return; // 没推动：局面未变，提示仍有效
    clearHint(this);
    movePiece(this.selected, dx, dy);
    this.moves++;
    this.movesText.setText(`步数 ${this.moves}`);
    this.redraw();
    if (isWin(this.pieces)) {
      this.won = true;
      this.showOverlay('🎉 曹操脱险');
    }
  }

  private redraw() {
    this.layer.removeAll(true);
    const g = this.add.graphics();
    g.fillStyle(0x000000, 0.35);
    g.fillRect(BOARD_X - 8, BOARD_Y - 8, BOARD_W + 16, BOARD_H + 16);
    g.lineStyle(2, 0xffd54f, 0.6);
    g.strokeRect(BOARD_X - 8, BOARD_Y - 8, BOARD_W + 16, BOARD_H + 16);
    // 出口标记
    g.lineStyle(4, 0x69f0ae, 0.9);
    g.lineBetween(BOARD_X + CELL, BOARD_Y + BOARD_H + 6, BOARD_X + 3 * CELL, BOARD_Y + BOARD_H + 6);
    this.layer.add(g);

    for (const p of this.pieces) {
      const sel = this.selected?.id === p.id;
      const px = BOARD_X + p.x * CELL;
      const py = BOARD_Y + p.y * CELL;
      const bg = this.add.rectangle(
        px + (p.w * CELL) / 2,
        py + (p.h * CELL) / 2,
        p.w * CELL - 8,
        p.h * CELL - 8,
        KIND_COLORS[p.kind],
      ).setStrokeStyle(sel ? 4 : 1, sel ? 0xffe082 : 0xffffff, sel ? 1 : 0.35).setInteractive({ useHandCursor: true });
      bg.on('pointerdown', (_: unknown, _x: number, _y: number, ev: Phaser.Types.Input.EventData) => {
        ev.stopPropagation();
        if (!this.started) return;
        this.selected = p;
        this.redraw();
      });
      const label = this.add.text(px + (p.w * CELL) / 2, py + (p.h * CELL) / 2, p.label, {
        ...FONT, fontSize: p.kind === 'pawn' ? '20px' : '26px', fontStyle: 'bold',
      }).setOrigin(0.5);
      this.layer.add([bg, label]);
    }
  }

  /**
   * 提示：高亮推荐棋子（黄）与它的目标位置（绿），并标出移动方向。
   *
   * 最优解需遍历整个可达状态空间（最多 1.2s），先把“求解中”发出去再让出主线程，
   * 否则点下去界面像卡死一样没反应。
   */
  private showHint() {
    if (!this.started || this.won) return;
    clearHint(this);
    showHintText(this, '💡 正在搜索最短解…', { duration: 2500 });
    this.time.delayedCall(50, () => this.applyHint());
  }

  private applyHint() {
    const hint = findHint(this.pieces);
    if (!hint) {
      showHintText(this, '曹操已经在出口了');
      return;
    }
    if (hint.dead) {
      showHintText(this, '这个布局已无解，换布局或重新开始');
      return;
    }
    const p = this.pieces[hint.piece];
    const box = (dx: number, dy: number, color: number): HintBox => ({
      x: BOARD_X + (p.x + p.w / 2 + dx) * CELL,
      y: BOARD_Y + (p.y + p.h / 2 + dy) * CELL,
      w: p.w * CELL - 12,
      h: p.h * CELL - 12,
      color,
    });
    const arrow = hint.dy !== 0 ? arrowOf(hint.dy > 0 ? 'down' : 'up') : arrowOf(hint.dx > 0 ? 'right' : 'left');
    highlightBoxes(this, [
      box(0, 0, 0xffd54f),
      box(hint.dx, hint.dy, 0x69f0ae),
      labelBox(BOARD_X + (p.x + p.w / 2 + hint.dx / 2) * CELL, BOARD_Y + (p.y + p.h / 2 + hint.dy / 2) * CELL, arrow, 40),
    ]);
    const step = arrow === '▲' ? '↑' : arrow === '▼' ? '↓' : arrow === '◀' ? '←' : '→';
    showHintText(this, `选「${p.label}」再按 ${step} · ` +
      (hint.rest === null ? '就近腾位（预算内未算出完整解）' : `按最优解还需 ${hint.rest} 步到出口`));
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
    c.add(this.add.text(0, -30, `布局 ${LAYOUTS[this.layoutIdx].name}    步数 ${this.moves}    用时 ${m}:${s}`, {
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
