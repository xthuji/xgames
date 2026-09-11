import Phaser from 'phaser';
import { store } from '../state/store';
import { getOrCreateMachineID } from '../utils/machine';
import { net } from '../net/ws';
import {
  MsgTypes,
  ProfileUpdatedPayload,
  UpdateReplaySettingPayload,
  UpdateLanPlayPayload,
} from '../protocol';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;

/**
 * SettingsScene 用户设置页面
 * - 修改昵称（使用 DOM 输入框）
 * - 复盘功能开关
 * - 局域网对战开关（重启生效）
 * - 显示设备标识 / 清理本地缓存
 *
 * 布局以屏幕中心为基准用「垂直游标」自上而下排布，避免固定坐标在窗口
 * 最小高度（700）下与底部按钮区重叠。
 */
export class SettingsScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];
  private nameInput!: HTMLInputElement;
  private statusText!: Phaser.GameObjects.Text;
  private replayEnabled = true; // 默认启用复盘功能
  private lanEnabled = false; // 默认关闭局域网对战（仅本机监听）

  constructor() {
    super({ key: 'Settings' });
  }

  create() {
    const width = this.scale.width;
    const height = this.scale.height;

    // 复盘开关初值（localStorage 持久）
    const savedReplay = localStorage.getItem('replay_enabled');
    if (savedReplay !== null) {
      this.replayEnabled = savedReplay === 'true';
    }

    // 局域网对战初值：以服务端回显的真实绑定状态（store.lanPlay）为准，
    // localStorage 仅作 WS/探测未就绪前的即时反射兜底。
    const savedLan = localStorage.getItem('lan_play');
    this.lanEnabled = store.lanPlay || savedLan === 'true';

    // 背景
    this.add.rectangle(0, 0, width, height, 0x1a1a2e).setOrigin(0);

    // 标题
    this.add.text(width / 2, 80, '用户设置', {
      ...FONT,
      fontSize: '36px',
      color: '#ffffff',
      stroke: '#000000',
      strokeThickness: 3,
    }).setOrigin(0.5);

    // 垂直游标：各区块依次向下排布
    let y = height / 2 - 160;

    // 昵称输入区域
    y = this.createNameInputSection(width, y);

    // 复盘功能开关
    this.createToggle(width, y, {
      title: '对战游戏复盘功能',
      desc: '启用后，对局结束时可查看详细的决策分析和改进建议',
      enabled: this.replayEnabled,
      onChange: (v: boolean) => {
        this.replayEnabled = v;
        localStorage.setItem('replay_enabled', String(v));
        net.send(MsgTypes.MsgUpdateReplaySetting, { replay_enabled: v } satisfies UpdateReplaySettingPayload);
        this.statusText.setText(v ? '复盘功能已启用' : '复盘功能已禁用').setColor('#4caf50');
      },
    });
    y += 88;

    // 局域网对战开关
    this.createToggle(width, y, {
      title: '局域网对战',
      desc: '开启后同一局域网内设备可联机对战（重启生效，可能弹防火墙授权）',
      enabled: this.lanEnabled,
      onChange: (v: boolean) => {
        this.lanEnabled = v;
        store.lanPlay = v;
        localStorage.setItem('lan_play', String(v));
        net.send(MsgTypes.MsgUpdateLanPlay, { lan_play: v } satisfies UpdateLanPlayPayload);
        this.statusText
          .setText(v ? '已开启局域网对战，重启后生效' : '已关闭局域网对战，重启后生效')
          .setColor('#ffb74d');
      },
    });
    y += 76;

    // 状态提示文本
    this.statusText = this.add.text(width / 2, y, '', {
      ...FONT,
      fontSize: '16px',
      color: '#81d4fa',
    }).setOrigin(0.5);

    // 设备标识显示
    const machineID = getOrCreateMachineID();
    this.add.text(width / 2, height - 180, `设备标识: ${machineID.substring(0, 8)}...`, {
      ...FONT,
      fontSize: '14px',
      color: '#888888',
    }).setOrigin(0.5);

    // 清理数据按钮
    this.add.text(width / 2, height - 130, '清理本地缓存', {
      ...FONT,
      fontSize: '18px',
      color: '#ff6b6b',
      backgroundColor: '#2a2a3e',
      padding: { x: 20, y: 10 },
    })
      .setOrigin(0.5)
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => this.handleClearData());

    // 返回按钮
    this.add.text(width / 2, height - 60, '← 返回大厅', {
      ...FONT,
      fontSize: '20px',
      color: '#ffd54f',
      backgroundColor: '#2a2a3e',
      padding: { x: 30, y: 12 },
    })
      .setOrigin(0.5)
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => this.scene.start('Lobby'));

    // 监听昵称更新响应
    const unsub = net.on(MsgTypes.MsgProfileUpdated, (payload: ProfileUpdatedPayload) => {
      store.playerName = payload.player_name;
      this.statusText.setText('昵称已更新').setColor('#4caf50');
      this.time.delayedCall(1000, () => this.scene.start('Lobby'));
    });
    this.unsubscribers.push(unsub);

    // URL hash 标记
    if (location.hash !== '#/settings') location.hash = '/settings';

    // Phaser 3.90 不会自动调用场景子类的 shutdown()，需手动接线，
    // 否则切场景时订阅与 DOM 输入框永不清理。
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this.shutdown());
  }

  shutdown() {
    for (const u of this.unsubscribers) u();
    this.unsubscribers = [];
    // 清理 DOM 输入框
    if (this.nameInput) {
      this.nameInput.remove();
    }
  }

  /** 绘制昵称输入区，返回游标向下推进后的新 y 值 */
  private createNameInputSection(width: number, y: number): number {
    this.add.text(width / 2, y, '昵称（1-20 字符）', {
      ...FONT,
      color: '#cccccc',
    }).setOrigin(0.5);

    // 使用 DOM 输入框（Phaser 支持）
    const inputStyle = `
      background: #2a2a3e;
      border: 2px solid #4a4a5e;
      border-radius: 8px;
      color: #ffffff;
      font-size: 20px;
      padding: 10px 15px;
      width: 280px;
      outline: none;
      font-family: Arial, sans-serif;
    `;

    const html = `
      <div style="display: flex; justify-content: center;">
        <input type="text" maxlength="20" value="${store.playerName || ''}" 
               style="${inputStyle}" placeholder="请输入昵称" />
      </div>
    `;

    const domElement = this.add.dom(width / 2, y + 42).createFromHTML(html);
    this.nameInput = domElement.node.querySelector('input')!;

    // 保存按钮
    this.add.text(width / 2, y + 92, '保存昵称', {
      ...FONT,
      fontSize: '20px',
      color: '#4caf50',
      backgroundColor: '#2a2a3e',
      padding: { x: 25, y: 12 },
    })
      .setOrigin(0.5)
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => this.handleSaveName());

    return y + 128;
  }

  /** 通用开关行：标题 + 说明 + 可点击滑块，点击时回调 onChange(新值) */
  private createToggle(
    width: number,
    y: number,
    opts: { title: string; desc: string; enabled: boolean; onChange: (v: boolean) => void },
  ): void {
    this.add.text(width / 2, y, opts.title, {
      ...FONT,
      fontSize: '20px',
      color: '#ffd54f',
    }).setOrigin(0.5);

    this.add.text(width / 2, y + 24, opts.desc, {
      ...FONT,
      fontSize: '13px',
      color: '#aaaaaa',
    }).setOrigin(0.5);

    let cur = opts.enabled;
    const toggleX = width / 2 - 80;
    const toggleY = y + 56;

    const bg = this.add.rectangle(toggleX, toggleY, 80, 40, cur ? 0x4caf50 : 0x757575, 0.8)
      .setStrokeStyle(2, 0xffffff, 0.6)
      .setInteractive({ useHandCursor: true });

    const slider = this.add.circle(cur ? toggleX + 20 : toggleX - 20, toggleY, 16, 0xffffff);

    const label = this.add.text(toggleX + 60, toggleY, cur ? '已启用' : '已禁用', {
      ...FONT,
      fontSize: '16px',
      color: cur ? '#4caf50' : '#999999',
    }).setOrigin(0, 0.5);

    const redraw = () => {
      bg.setFillStyle(cur ? 0x4caf50 : 0x757575, 0.8);
      slider.x = cur ? toggleX + 20 : toggleX - 20;
      label.setText(cur ? '已启用' : '已禁用');
      label.setColor(cur ? '#4caf50' : '#999999');
    };

    bg.on('pointerdown', () => {
      cur = !cur;
      redraw();
      opts.onChange(cur);
    });
    bg.on('pointerover', () => bg.setStrokeStyle(2, 0xffd54f, 0.8));
    bg.on('pointerout', () => bg.setStrokeStyle(2, 0xffffff, 0.6));
  }

  private handleSaveName() {
    const newName = this.nameInput.value.trim();

    if (!newName) {
      this.statusText.setText('昵称不能为空').setColor('#ff6b6b');
      return;
    }
    if (newName.length > 20) {
      this.statusText.setText('昵称不能超过 20 个字符').setColor('#ff6b6b');
      return;
    }
    if (newName === store.playerName) {
      this.statusText.setText('昵称未改变').setColor('#888888');
      return;
    }

    // 发送更新请求到服务端
    net.send(MsgTypes.MsgUpdateProfile, { name: newName });
    this.statusText.setText('正在更新...').setColor('#81d4fa');
  }

  private handleClearData() {
    if (window.confirm('确定要清理所有本地缓存数据吗？\n这将清除机器码、重连令牌等信息。')) {
      localStorage.clear();
      window.location.reload();
    }
  }
}
