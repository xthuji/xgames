import Phaser from 'phaser';

/**
 * 安全播放音频：资源缺失（加载/解码失败，如 WKWebView 不支持的格式）时
 * 静默跳过并告警，而不是让 Phaser 抛出异常中断场景 create()，
 * 导致画面冻结、输入全部失效。
 */
export function safePlay(scene: Phaser.Scene, key: string, config?: Phaser.Types.Sound.SoundConfig): void {
  try {
    if (!scene.game.cache.audio.has(key)) {
      console.warn(`音频资源缺失，跳过播放: ${key}`);
      return;
    }
    // manager.play() 返回 boolean：false = 未启动播放（如自动播放策略拦截、解码中）
    const started = scene.sound.play(key, config);
    if (started === false) {
      console.warn(`音频播放未启动（返回 false）: ${key}`);
    }
  } catch (e) {
    console.warn(`音频播放失败: ${key}`, e);
  }
}
