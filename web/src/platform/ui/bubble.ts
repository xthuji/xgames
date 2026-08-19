import Phaser from 'phaser';

/**
 * 语音气泡：玩家头像旁的文字气泡（"不叫/不出/牌型名"），定时自动销毁（landlord say）。
 */
export function showBubble(scene: Phaser.Scene, x: number, y: number, text: string, durationMs = 2000) {
  const pad = 10;
  const label = scene.add.text(0, 0, text, {
    fontFamily: 'Arial',
    fontSize: '20px',
    color: '#333333',
  });
  const bg = scene.add.graphics();
  const w = label.width + pad * 2;
  const h = label.height + pad;
  bg.fillStyle(0xffffff, 0.92);
  bg.fillRoundedRect(-w / 2, -h / 2, w, h, 10);
  label.setOrigin(0.5);

  const container = scene.add.container(x, y, [bg, label]);
  container.setDepth(50);
  scene.time.delayedCall(durationMs, () => container.destroy());
  return container;
}
