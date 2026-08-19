import Phaser from 'phaser';
import { store } from '../state/store';
import { getOrCreateMachineID } from '../utils/machine';
import { net } from '../net/ws';
import { MsgTypes, ProfileUpdatedPayload, UpdateReplaySettingPayload } from '../protocol';

const FONT = { fontFamily: 'Arial', fontSize: '18px', color: '#ffffff' } as Phaser.Types.GameObjects.Text.TextStyle;

/**
 * SettingsScene 用户设置页面
 * - 修改昵称（使用 DOM 输入框）
 * - 显示设备标识
 * - 清理本地缓存
 */
export class SettingsScene extends Phaser.Scene {
  private unsubscribers: Array<() => void> = [];
  private nameInput!: HTMLInputElement;
  private statusText!: Phaser.GameObjects.Text;
  private replayToggle!: Phaser.GameObjects.Rectangle;
  private replayToggleLabel!: Phaser.GameObjects.Text;
  private replayEnabled = true; // 默认启用复盘功能

  constructor() {
    super({ key: 'Settings' });
  }

  create() {
    const width = this.scale.width;
    const height = this.scale.height;

    // 从 localStorage 读取复盘功能设置
    const savedReplaySetting = localStorage.getItem('replay_enabled');
    if (savedReplaySetting !== null) {
      this.replayEnabled = savedReplaySetting === 'true';
    }

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

    // 昵称输入区域
    this.createNameInputSection(width, height);

    // 复盘功能开关
    this.createReplayToggleSection(width, height);

    // 状态提示文本
    this.statusText = this.add.text(width / 2, height / 2 + 140, '', {
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

  private createNameInputSection(width: number, height: number) {
    const centerY = height / 2 - 60;

    // 标签
    this.add.text(width / 2, centerY - 80, '昵称（1-20 字符）', {
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

    const domElement = this.add.dom(width / 2, centerY - 30).createFromHTML(html);
    this.nameInput = domElement.node.querySelector('input')!;

    // 保存按钮
    this.add.text(width / 2, centerY + 40, '保存昵称', {
      ...FONT,
      fontSize: '20px',
      color: '#4caf50',
      backgroundColor: '#2a2a3e',
      padding: { x: 25, y: 12 },
    })
      .setOrigin(0.5)
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => this.handleSaveName());
  }

  /** 创建复盘功能开关 */
  private createReplayToggleSection(width: number, height: number) {
    const centerY = height / 2 + 20;
    
    // 标题
    this.add.text(width / 2, centerY - 40, '对战游戏复盘功能', {
      ...FONT,
      fontSize: '20px',
      color: '#ffd54f',
    }).setOrigin(0.5);

    // 说明文字
    this.add.text(width / 2, centerY - 15, '启用后，对局结束时可查看详细的决策分析和改进建议', {
      ...FONT,
      fontSize: '14px',
      color: '#aaaaaa',
    }).setOrigin(0.5);

    // 开关背景
    const toggleX = width / 2 - 80;
    const toggleY = centerY + 20;
    
    this.replayToggle = this.add.rectangle(toggleX, toggleY, 80, 40, this.replayEnabled ? 0x4caf50 : 0x757575, 0.8)
      .setStrokeStyle(2, 0xffffff, 0.6)
      .setInteractive({ useHandCursor: true });
    
    // 开关滑块
    const sliderX = this.replayEnabled ? toggleX + 20 : toggleX - 20;
    const slider = this.add.circle(sliderX, toggleY, 16, 0xffffff);
    
    // 状态标签
    this.replayToggleLabel = this.add.text(toggleX + 60, toggleY, 
      this.replayEnabled ? '已启用' : '已禁用', 
      { ...FONT, fontSize: '16px', color: this.replayEnabled ? '#4caf50' : '#999999' }
    ).setOrigin(0, 0.5);
    
    // 点击切换
    this.replayToggle.on('pointerdown', () => {
      this.replayEnabled = !this.replayEnabled;
      
      // 更新开关外观
      this.replayToggle.setFillStyle(this.replayEnabled ? 0x4caf50 : 0x757575, 0.8);
      slider.x = this.replayEnabled ? toggleX + 20 : toggleX - 20;
      this.replayToggleLabel.setText(this.replayEnabled ? '已启用' : '已禁用');
      this.replayToggleLabel.setColor(this.replayEnabled ? '#4caf50' : '#999999');
      
      // 保存到 localStorage
      localStorage.setItem('replay_enabled', String(this.replayEnabled));
      
      // 发送消息到后端
      net.send(MsgTypes.MsgUpdateReplaySetting, { replay_enabled: this.replayEnabled } satisfies UpdateReplaySettingPayload);
      
      // 显示提示
      this.statusText.setText(this.replayEnabled ? '复盘功能已启用' : '复盘功能已禁用').setColor('#4caf50');
    });
    
    // 鼠标悬停效果
    this.replayToggle.on('pointerover', () => {
      this.replayToggle.setStrokeStyle(2, 0xffd54f, 0.8);
    });
    this.replayToggle.on('pointerout', () => {
      this.replayToggle.setStrokeStyle(2, 0xffffff, 0.6);
    });
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
