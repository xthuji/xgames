import Phaser from 'phaser';
import { store } from '../../platform/state/store';
import { FlowState, setFlowState } from '../../platform/flow/gameFlow';
import { showGameRules } from '../../platform/ui/modal';
import { getGameRule, getGameName } from '../../platform/rules/gameRules';
import { addHintButton, cellBox, clearHint, highlightBoxes, showHintText } from '../../platform/ui/hint';
import {
  Board,
  LEVELS,
  checkResult,
  createBoard,
  findHint,
  firstClick,
  openCell,
  toggleFlag,
} from './logic';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff', stroke: '#000000', strokeThickness: 3 } as Phaser.Types.GameObjects.Text.TextStyle;

const LEVEL = LEVELS[1]; // 基础版默认中级 16×16 / 40 雷
const CELL = 32;
const BOARD_W = LEVEL.cols * CELL;
const BOARD_X = (1280 - BOARD_W) / 2;
const BOARD_Y = 130;

/** 经典数字配色（1-8） */
const NUM_COLORS = ['', '#4fc3f7', '#81c784', '#ff8a80', '#9575cd', '#ffab91', '#4db6ac', '#e0e0e0', '#b0bec5'];

function fmtTime(s: number): string {
  const m = Math.floor(s / 60);
  return `${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
}

/** 扫雷（单机）：左键翻开（0 格泛洪、首点安全）、右键标旗；全部非雷格翻开或标齐全部雷获胜 */
export class MinesweeperScene extends Phaser.Scene {
  private board!: Board;
  private flags = 0;
  private seconds = 0;
  private started = false;          // 首点已触发（地雷已生成）
  private startedByButton = false;  // "开始游戏" 按钮已点击
  private over = false;
  private cellObjs: Array<Array<{ rect: Phaser.GameObjects.Rectangle; label: Phaser.GameObjects.Text }>> = [];
  private hudText!: Phaser.GameObjects.Text;
  private timer?: Phaser.Time.TimerEvent;
  private overlay?: Phaser.GameObjects.Container;
  private layer!: Phaser.GameObjects.Container;

  constructor() {
    super('MinesGame');
  }

  create() {
    this.add.image(640, 360, 'bg').setDisplaySize(1280, 720);
    setFlowState(FlowState.GAME);
    if (location.hash !== '#/game/mines') location.hash = '/game/mines';
    this.input.mouse?.disableContextMenu();

    this.buildTopBar();
    this.layer = this.add.container(0, 0);
    this.newGame();

    this.timer = this.time.addEvent({
      delay: 5000, // 5秒更新一次，降低CPU占用
      loop: true,
      callback: () => {
        if (this.startedByButton && this.started && !this.over) {
          this.seconds += 5;
          this.updateHud();
        }
      },
    });
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.timer?.remove());
  }

  private buildTopBar() {
    this.add.text(20, 18, '💣 扫雷', { ...FONT, fontSize: '24px', color: '#ffe082', fontStyle: 'bold' });
    this.hudText = this.add.text(640, 24, '', { ...FONT, fontSize: '20px' }).setOrigin(0.5);

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

  private newGame() {
    this.board = createBoard(LEVEL);
    this.flags = 0;
    this.seconds = 0;
    this.started = false;
    this.startedByButton = false;
    this.over = false;
    clearHint(this);
    this.overlay?.destroy();
    this.overlay = undefined;
    this.buildCells();
    this.updateHud();
    this.showStartOverlay();
  }

  private showStartOverlay() {
    const container = this.add.container(0, 0).setDepth(200);
    const bg = this.add.rectangle(640, 360, 1280, 720, 0x000000, 0.55);
    const title = this.add.text(640, 240, '💣 扫雷', { ...FONT, fontSize: '52px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5);
    const hint = this.add.text(640, 300, '左键翻开 · 右键标旗 · 首点安全', { ...FONT, fontSize: '18px', color: '#aac7ff' }).setOrigin(0.5);

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
      const rules = getGameRule('mines');
      if (rules) showGameRules(this, getGameName('mines'), rules);
    });

    // 开始按钮
    const startBtn = this.add.text(140, 0, '🚀 开始游戏', { ...FONT, fontSize: '28px', color: '#ffe082', backgroundColor: '#000000aa', padding: { x: 28, y: 14 } })
      .setOrigin(0.5).setInteractive({ useHandCursor: true });
    startBtn.on('pointerover', () => startBtn.setStyle({ ...startBtn.style, color: '#fff176' }));
    startBtn.on('pointerout', () => startBtn.setStyle({ ...startBtn.style, color: '#ffe082' }));
    startBtn.on('pointerdown', () => { this.startedByButton = true; container.destroy(); this.overlay = undefined; });

    btnContainer.add([ruleBtnBg, ruleBtnText, startBtn]);
    container.add([bg, title, hint, btnContainer]);
    this.overlay = container;
  }

  private updateHud() {
    this.hudText.setText(`🚩 ${LEVEL.mines - this.flags}    ⏱ ${fmtTime(this.seconds)}    💣 ${LEVEL.rows}×${LEVEL.cols} / ${LEVEL.mines} 雷`);
  }

  // ── 渲染 ──

  private buildCells() {
    this.layer.removeAll(true);
    this.cellObjs = [];
    for (let r = 0; r < LEVEL.rows; r++) {
      const row: Array<{ rect: Phaser.GameObjects.Rectangle; label: Phaser.GameObjects.Text }> = [];
      for (let c = 0; c < LEVEL.cols; c++) {
        const x = BOARD_X + c * CELL + CELL / 2;
        const y = BOARD_Y + r * CELL + CELL / 2;
        const rect = this.add.rectangle(x, y, CELL - 2, CELL - 2, 0x3f5a75)
          .setStrokeStyle(1, 0xffffff, 0.25)
          .setInteractive({ useHandCursor: true });
        rect.on('pointerdown', (p: Phaser.Input.Pointer) => this.onCellDown(r, c, p.button));
        const label = this.add.text(x, y, '', { fontFamily: 'Arial', fontSize: '18px', fontStyle: 'bold', color: '#ffffff' }).setOrigin(0.5);
        this.layer.add([rect, label]);
        row.push({ rect, label });
      }
      this.cellObjs.push(row);
    }
  }

  private refreshCell(r: number, c: number) {
    const cell = this.board[r][c];
    const { rect, label } = this.cellObjs[r][c];
    if (cell.status === 'open') {
      rect.setFillStyle(cell.mine ? 0xaa3333 : 0x22384d, 1).setStrokeStyle(1, 0xffffff, 0.1);
      if (cell.mine) {
        label.setText('💣').setFontSize(16);
      } else if (cell.adjacent > 0) {
        label.setText(String(cell.adjacent)).setFontSize(18).setColor(NUM_COLORS[cell.adjacent]);
      } else {
        label.setText('');
      }
    } else if (cell.status === 'flagged') {
      rect.setFillStyle(0x3f5a75, 1);
      label.setText('🚩').setFontSize(16);
    } else {
      rect.setFillStyle(0x3f5a75, 1).setStrokeStyle(1, 0xffffff, 0.25);
      label.setText('');
    }
  }

  private refreshAll() {
    // 批量更新，避免不必要的DOM操作
    for (let r = 0; r < LEVEL.rows; r++) {
      for (let c = 0; c < LEVEL.cols; c++) {
        const cell = this.board[r][c];
        const { rect, label } = this.cellObjs[r][c];
        
        if (cell.status === 'open') {
          rect.setFillStyle(cell.mine ? 0xaa3333 : 0x22384d, 1).setStrokeStyle(1, 0xffffff, 0.1);
          if (cell.mine) {
            label.setText('💣').setFontSize(16);
          } else if (cell.adjacent > 0) {
            label.setText(String(cell.adjacent)).setFontSize(18).setColor(NUM_COLORS[cell.adjacent]);
          } else {
            label.setText('');
          }
        } else if (cell.status === 'flagged') {
          rect.setFillStyle(0x3f5a75, 1);
          label.setText('🚩').setFontSize(16);
        } else {
          rect.setFillStyle(0x3f5a75, 1).setStrokeStyle(1, 0xffffff, 0.25);
          label.setText('');
        }
      }
    }
  }

  // ── 交互 ──

  private onCellDown(r: number, c: number, button: number) {
    if (this.over || !this.startedByButton) return;
    clearHint(this); // 无论翻开还是插旗，局面都变了，旧推荐作废
    if (button === 2) {
      this.flags += toggleFlag(this.board, r, c);
      this.refreshCell(r, c);
      this.updateHud();
      if (checkResult(this.board, LEVEL.mines) === 'won') this.finish(true);
      return;
    }
    if (button !== 0) return;
    const cell = this.board[r][c];
    if (cell.status !== 'covered') return;
    if (!this.started) {
      this.started = true;
      firstClick(this.board, r, c);
    }
    const hit = openCell(this.board, r, c);
    this.refreshAll();
    if (hit) {
      this.revealMines(r, c);
      this.finish(false);
      return;
    }
    if (checkResult(this.board, LEVEL.mines) === 'won') this.finish(true);
  }

  /** 失败时揭示全部雷；踩中的高亮，错标格打叉 */
  private revealMines(hitR: number, hitC: number) {
    for (let r = 0; r < LEVEL.rows; r++) {
      for (let c = 0; c < LEVEL.cols; c++) {
        const cell = this.board[r][c];
        const { rect, label } = this.cellObjs[r][c];
        if (cell.mine && cell.status !== 'open') {
          label.setText('💣').setFontSize(16);
        } else if (!cell.mine && cell.status === 'flagged') {
          label.setText('❌').setFontSize(16);
        }
        if (r === hitR && c === hitC) rect.setFillStyle(0xd32f2f, 1);
      }
    }
  }

  private finish(won: boolean) {
    this.over = true;
    clearHint(this);
    if (won) {
      // 胜利时把剩余的雷自动标旗
      for (let r = 0; r < LEVEL.rows; r++) {
        for (let c = 0; c < LEVEL.cols; c++) {
          const cell = this.board[r][c];
          if (cell.mine && cell.status === 'covered') {
            cell.status = 'flagged';
            this.flags++;
          }
        }
      }
      this.refreshAll();
      this.updateHud();
    }
    this.showOverlay(won ? '🎉 排雷成功！' : '💥 踩雷了！');
  }

  /**
   * 提示：只用盘面上已公开的数字与旗子做约束推理（不读雷位），所以不会泄底。
   * 绿框 = 必安全可翻，红框 = 必为雷应插旗；推不出确定结论时改用琥珀框标出信息最少的猜测格。
   */
  private showHint() {
    if (this.over || !this.startedByButton) return;
    const hint = findHint(this.board);
    if (!hint) {
      clearHint(this);
      showHintText(this, '还没有可推理的数字，先随机翻开一格（首点保证安全）');
      return;
    }
    const cx = (c: number) => BOARD_X + c * CELL + CELL / 2;
    const cy = (r: number) => BOARD_Y + r * CELL + CELL / 2;
    if (hint.uncertain) {
      // 猜点用琥珀色而非绿色：绿色在本游戏里专指「推理出的必安全格」
      const [r, c] = hint.safe[0];
      highlightBoxes(this, [cellBox(cx(c), cy(r), CELL, 1, 0xffd54f)]);
      showHintText(this, '现有数字推不出确定的一格，高亮这格离已翻开数字最远、信息最少，只能赌一把');
      return;
    }
    highlightBoxes(this, [
      ...hint.safe.map(([r, c]) => cellBox(cx(c), cy(r), CELL, 1, 0x69f0ae)),
      ...hint.mines.map(([r, c]) => cellBox(cx(c), cy(r), CELL, 1, 0xff5252)),
    ]);
    const parts: string[] = [];
    if (hint.safe.length > 0) parts.push(`绿框 ${hint.safe.length} 格必安全，可直接翻开`);
    if (hint.mines.length > 0) parts.push(`红框 ${hint.mines.length} 格必为雷，右键插旗`);
    showHintText(this, parts.join(' · '));
  }

  // ── 结算 ──

  private showOverlay(title: string) {
    const c = this.add.container(640, 360).setDepth(200);
    const mask = this.add.rectangle(0, 0, 1280, 720, 0x000000, 0.6).setInteractive();
    const panel = this.add.rectangle(0, 0, 520, 300, 0x1b2f3f, 0.97).setStrokeStyle(2, 0xffd54f);
    c.add([mask, panel]);
    c.add(this.add.text(0, -100, title, { ...FONT, fontSize: '32px', color: '#ffe082', fontStyle: 'bold' }).setOrigin(0.5));
    c.add(this.add.text(0, -30, `用时 ${fmtTime(this.seconds)}    难度 ${LEVEL.rows}×${LEVEL.cols} / ${LEVEL.mines} 雷`, {
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
    // 扫雷是静态场景，不需要每帧更新
    // 所有变化都通过事件驱动（点击、计时器等）
  }
}
