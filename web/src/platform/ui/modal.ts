import Phaser from 'phaser';

export interface ModalOption {
  label: string;
  value: string;
}

export interface ModalConfig {
  title: string;
  content: string;
  width?: number;
  height?: number;
  options?: ModalOption[];
  onConfirm?: (value?: string) => void;
  onCancel?: () => void;
}

/**
 * 统一的游戏规则弹窗组件
 * 支持纯文本展示、选项列表两种模式
 */
export function showRuleModal(scene: Phaser.Scene, config: ModalConfig): Phaser.GameObjects.Container {
  const {
    title,
    content,
    width = 700,
    height = 500,
    options,
    onConfirm,
    onCancel,
  } = config;

  const centerX = scene.cameras.main.centerX;
  const centerY = scene.cameras.main.centerY;

  // 创建深度较高的容器（确保在最上层）
  const modal = scene.add.container(centerX, centerY).setDepth(1000);

  // 全屏半透明遮罩（阻止底层交互）
  const overlay = scene.add.rectangle(0, 0, scene.cameras.main.width, scene.cameras.main.height, 0x000000, 0.6)
    .setOrigin(0.5)
    .setInteractive();

  // 弹窗主体背景
  const bg = scene.add.rectangle(0, 0, width, height, 0x1b2f3f, 0.98)
    .setStrokeStyle(3, 0xffd54f);

  // 标题栏背景
  const titleBarHeight = 60;
  const titleBar = scene.add.rectangle(0, -height / 2 + titleBarHeight / 2, width, titleBarHeight, 0x2a4a5f, 1);

  // 标题文字
  const titleText = scene.add.text(0, -height / 2 + titleBarHeight / 2, title, {
    fontFamily: 'Arial',
    fontSize: '28px',
    color: '#ffe082',
    fontStyle: 'bold',
    stroke: '#1a1a00',
    strokeThickness: 3,
  }).setOrigin(0.5);

  // 关闭按钮
  const closeBtnSize = 30;
  const closeBtnX = width / 2 - closeBtnSize;
  const closeBtnY = -height / 2 + titleBarHeight / 2;
  const closeBtn = scene.add.rectangle(closeBtnX, closeBtnY, closeBtnSize, closeBtnSize, 0xff6b6b, 1)
    .setStrokeStyle(2, 0xffffff)
    .setInteractive({ useHandCursor: true });
  const closeText = scene.add.text(closeBtnX, closeBtnY, '✕', {
    fontFamily: 'Arial',
    fontSize: '18px',
    color: '#ffffff',
  }).setOrigin(0.5);

  // 内容区域
  const contentStartY = -height / 2 + titleBarHeight + 30;
  const contentWidth = width - 80;
  // 纯文本内容可视区（标题栏下方 ~ 弹窗底部）
  const contentAreaTop = -height / 2 + titleBarHeight + 20;
  const contentAreaBottom = height / 2 - 20;
  const contentAreaHeight = contentAreaBottom - contentAreaTop;

  let contentElement: Phaser.GameObjects.Text | Phaser.GameObjects.Container;

  if (options && options.length > 0) {
    const optionContainer = scene.add.container(0, 0);
    const optionHeight = 50;
    const startY = contentStartY;

    options.forEach((opt, index) => {
      const y = startY + index * (optionHeight + 10);
      const optBg = scene.add.rectangle(0, y, contentWidth, optionHeight, 0x3a5a6f, 0.8)
        .setStrokeStyle(2, 0xaac7ff)
        .setInteractive({ useHandCursor: true });

      const optText = scene.add.text(0, y, `${index + 1}. ${opt.label}`, {
        fontFamily: 'Arial',
        fontSize: '18px',
        color: '#ffffff',
      }).setOrigin(0.5);

      optBg.on('pointerover', () => {
        optBg.setFillStyle(0x4a6a7f, 1);
        optText.setStyle({ color: '#ffe082' });
      });
      optBg.on('pointerout', () => {
        optBg.setFillStyle(0x3a5a6f, 0.8);
        optText.setStyle({ color: '#ffffff' });
      });
      optBg.on('pointerdown', () => {
        if (onConfirm) onConfirm(opt.value);
        destroyModal();
      });

      optionContainer.add([optBg, optText]);
    });

    contentElement = optionContainer;
  } else {
    // 纯文本模式：水平垂直居中；内容超过可视区时支持滚动
    contentElement = scene.add.text(0, contentAreaTop, content, {
      fontFamily: 'Arial',
      fontSize: '16px',
      color: '#ffffff',
      lineSpacing: 8,
      align: 'center',
      wordWrap: { width: contentWidth, useAdvancedWrap: true },
    }).setOrigin(0.5, 0);

    const textEl = contentElement as Phaser.GameObjects.Text;
    const naturalHeight = textEl.height;
    const maxScroll = Math.max(0, naturalHeight - contentAreaHeight);
    let scrollY = 0;

    const applyPos = () => {
      if (maxScroll <= 0) {
        // 内容不超出：垂直居中显示
        textEl.setY(contentAreaTop + (contentAreaHeight - naturalHeight) / 2);
      } else {
        textEl.setY(contentAreaTop - scrollY);
      }
    };
    applyPos();

    if (maxScroll > 0) {
      // 用遮罩裁剪超长内容，仅展示可视区；左右依然居中
      // 注意：GeometryMask 的 Graphics 不能作为 Container 的子节点（Phaser 已知问题会导致被遮罩对象不可见），
      // 因此将 maskShape 加在场景根节点，并按弹窗的世界坐标绘制。
      const maskShape = scene.add.graphics();
      maskShape.fillRect(centerX - contentWidth / 2, centerY + contentAreaTop, contentWidth, contentAreaHeight);
      const mask = maskShape.createGeometryMask();
      textEl.setMask(mask);

      // 滚轮滚动
      const onWheel = (
        _p: Phaser.Input.Pointer,
        _objs: unknown[],
        _dx: number,
        dy: number,
      ) => {
        scrollY = Phaser.Math.Clamp(scrollY + dy, 0, maxScroll);
        applyPos();
      };
      // 拖拽滚动
      let lastY = 0;
      textEl.setInteractive();
      textEl.on('pointerdown', (p: Phaser.Input.Pointer) => { lastY = p.y; });
      const onMove = (p: Phaser.Input.Pointer) => {
        if (p.isDown) {
          const delta = lastY - p.y;
          lastY = p.y;
          scrollY = Phaser.Math.Clamp(scrollY + delta, 0, maxScroll);
          applyPos();
        }
      };

      scene.input.on('wheel', onWheel);
      scene.input.on('pointermove', onMove);
      modal.once(Phaser.GameObjects.Events.DESTROY, () => {
        scene.input.off('wheel', onWheel);
        scene.input.off('pointermove', onMove);
        maskShape.destroy();
      });
    }
  }

  // 底部按钮（如果有回调）
  const buttons: Phaser.GameObjects.GameObject[] = [];

  if (onCancel || onConfirm) {
    const btnY = height / 2 - 50;
    const btnWidth = 120;
    const btnHeight = 45;

    if (onCancel) {
      const cancelBtn = scene.add.rectangle(-btnWidth / 2 - 10, btnY, btnWidth, btnHeight, 0x6c757d, 1)
        .setStrokeStyle(2, 0xffffff)
        .setInteractive({ useHandCursor: true });
      const cancelText = scene.add.text(-btnWidth / 2 - 10, btnY, '取消', {
        fontFamily: 'Arial',
        fontSize: '18px',
        color: '#ffffff',
      }).setOrigin(0.5);

      cancelBtn.on('pointerover', () => cancelBtn.setFillStyle(0x7c858d, 1));
      cancelBtn.on('pointerout', () => cancelBtn.setFillStyle(0x6c757d, 1));
      cancelBtn.on('pointerdown', () => {
        if (onCancel) onCancel();
        destroyModal();
      });

      buttons.push(cancelBtn, cancelText);
    }

    if (onConfirm && !options) {
      const confirmBtn = scene.add.rectangle(btnWidth / 2 + 10, btnY, btnWidth, btnHeight, 0x28a745, 1)
        .setStrokeStyle(2, 0xffffff)
        .setInteractive({ useHandCursor: true });
      const confirmText = scene.add.text(btnWidth / 2 + 10, btnY, '确定', {
        fontFamily: 'Arial',
        fontSize: '18px',
        color: '#ffffff',
      }).setOrigin(0.5);

      confirmBtn.on('pointerover', () => confirmBtn.setFillStyle(0x38b755, 1));
      confirmBtn.on('pointerout', () => confirmBtn.setFillStyle(0x28a745, 1));
      confirmBtn.on('pointerdown', () => {
        if (onConfirm) onConfirm();
        destroyModal();
      });

      buttons.push(confirmBtn, confirmText);
    }
  }

  // 组装弹窗
  modal.add([overlay, bg, titleBar, titleText, closeBtn, closeText, contentElement, ...buttons]);

  // 点击遮罩关闭
  overlay.on('pointerdown', () => {
    if (onCancel) onCancel();
    destroyModal();
  });

  // 关闭按钮
  closeBtn.on('pointerdown', () => {
    if (onCancel) onCancel();
    destroyModal();
  });

  function destroyModal() {
    modal.destroy();
  }

  return modal;
}

/**
 * 显示游戏规则弹窗（简化版，仅标题和内容）
 */
export function showGameRules(scene: Phaser.Scene, gameName: string, rules: string) {
  return showRuleModal(scene, {
    title: `📖 ${gameName} - 游戏规则`,
    content: rules,
    width: 750,
    height: 550,
  });
}
