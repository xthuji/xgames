import Phaser from 'phaser';

/**
 * 单机小游戏通用提示 UI：顶栏「💡 提示」按钮、推荐落点呼吸高亮、推荐说明条。
 *
 * 本模块只负责表现；推荐算法一律放在各游戏的 logic.ts（纯函数、不依赖 Phaser）。
 * 每个场景同时只保留一份高亮与说明条：再次点提示会替换上一次结果，超时自动消失，
 * 玩家付诸行动或重开一局时由场景调用 clearHint 立即清除，避免过期推荐误导。
 */

/** 高亮框：中心坐标 + 尺寸（与 Phaser Rectangle 的定位口径一致），可逐框指定颜色 */
export interface HintBox {
  x: number;
  y: number;
  w: number;
  h: number;
  /** 覆盖默认高亮色（如扫雷用红色标雷、绿色标安全格） */
  color?: number;
  /** 框心的字形（如 2048 的滑动方向箭头） */
  label?: string;
  /** 字形字号（px），默认 26 */
  labelSize?: number;
}

/** 提示默认存活时长（ms）：够玩家看清并落子，又不至于长期遮挡牌面 */
export const HINT_DURATION = 4000;

const BTN_STYLE = {
  fontFamily: 'Arial', fontSize: '16px', color: '#ffd54f', backgroundColor: '#00000066', padding: { x: 8, y: 4 },
} as Phaser.Types.GameObjects.Text.TextStyle;

const TEXT_STYLE = {
  fontFamily: 'Arial', fontSize: '20px', color: '#ffe082', stroke: '#000000', strokeThickness: 3,
} as Phaser.Types.GameObjects.Text.TextStyle;

/** 按场景持有的高亮图层与说明条（场景销毁后随 WeakMap 一起回收） */
const glows = new WeakMap<Phaser.Scene, Phaser.GameObjects.GameObject[]>();
const banners = new WeakMap<Phaser.Scene, Phaser.GameObjects.Text>();

/** 顶栏提示按钮：与各场景「🔄 重新开始」同款文字按钮，中心锚定在 (x, 24) */
export function addHintButton(scene: Phaser.Scene, x: number, onTap: () => void): Phaser.GameObjects.Text {
  const btn = scene.add.text(x, 24, '💡 提示', BTN_STYLE)
    .setOrigin(0.5)
    .setInteractive({ useHandCursor: true });
  btn.on('pointerdown', onTap);
  return btn;
}

/** 由格子中心坐标与格宽生成高亮框（留出 2px 内缩，高亮框不与相邻格糊在一起） */
export function cellBox(centerX: number, centerY: number, size: number, inset = 2, color?: number): HintBox {
  return { x: centerX, y: centerY, w: size - inset * 2, h: size - inset * 2, color };
}

/** 只画字形、不画框的高亮项（方向箭头、步数标记等） */
export function labelBox(centerX: number, centerY: number, label: string, size = 32, color?: number): HintBox {
  return { x: centerX, y: centerY, w: 0, h: 0, label, labelSize: size, color };
}

/** 颜色数值 → CSS 色值（提示文字要与所在场景的暗底保持对比） */
function cssColor(color: number): string {
  return `#${color.toString(16).padStart(6, '0')}`;
}

/** 在若干格子上叠加呼吸高亮框，HINT_DURATION 后自动消失 */
export function highlightBoxes(
  scene: Phaser.Scene,
  boxes: HintBox[],
  opts: { color?: number; duration?: number; depth?: number } = {},
): void {
  clearHighlight(scene);
  if (boxes.length === 0) return;
  const fallback = opts.color ?? 0xffd54f;
  const depth = opts.depth ?? 120;
  const g = scene.add.graphics().setDepth(depth);
  const objs: Phaser.GameObjects.GameObject[] = [g];
  for (const b of boxes) {
    const color = b.color ?? fallback;
    if (b.w > 0 && b.h > 0) {
      g.fillStyle(color, 0.22);
      g.fillRect(b.x - b.w / 2, b.y - b.h / 2, b.w, b.h);
      g.lineStyle(3, color, 1);
      g.strokeRect(b.x - b.w / 2, b.y - b.h / 2, b.w, b.h);
    }
    if (!b.label) continue;
    objs.push(scene.add.text(b.x, b.y, b.label, {
      fontFamily: 'Arial', fontSize: `${b.labelSize ?? 26}px`, fontStyle: 'bold',
      color: cssColor(color), stroke: '#000000', strokeThickness: 4,
    }).setOrigin(0.5).setDepth(depth + 1));
  }
  // 呼吸幅度收敛在 0.55~1：再低就会在深色底上淡到看不清（连连看实测）
  scene.tweens.add({ targets: objs, alpha: 0.55, duration: 320, yoyo: true, repeat: -1 });
  scene.time.delayedCall(opts.duration ?? HINT_DURATION, () => {
    for (const o of objs) o.destroy();
    if (glows.get(scene) === objs) glows.delete(scene);
  });
  glows.set(scene, objs);
}

/** 清除全部高亮框（含字形） */
export function clearHighlight(scene: Phaser.Scene): void {
  const objs = glows.get(scene);
  if (!objs) return;
  for (const o of objs) {
    scene.tweens.killTweensOf(o);
    o.destroy();
  }
  glows.delete(scene);
}

/** 一行说明推荐操作的提示条（默认画布底部居中），到期淡出销毁 */
export function showHintText(
  scene: Phaser.Scene,
  msg: string,
  opts: { x?: number; y?: number; color?: string; duration?: number } = {},
): void {
  clearHintText(scene);
  const duration = opts.duration ?? HINT_DURATION;
  const t = scene.add.text(opts.x ?? 640, opts.y ?? 700, msg, {
    ...TEXT_STYLE, color: opts.color ?? '#ffe082', backgroundColor: '#000000cc', padding: { x: 12, y: 6 },
  }).setOrigin(0.5).setDepth(130);
  scene.tweens.add({
    targets: t, alpha: 0, delay: duration - 500, duration: 500,
    onComplete: () => {
      t.destroy();
      if (banners.get(scene) === t) banners.delete(scene);
    },
  });
  banners.set(scene, t);
}

export function clearHintText(scene: Phaser.Scene): void {
  const t = banners.get(scene);
  if (!t) return;
  scene.tweens.killTweensOf(t);
  t.destroy();
  banners.delete(scene);
}

/** 清除全部提示痕迹（高亮 + 说明条）：玩家已行动、重开一局或进入结算时调用 */
export function clearHint(scene: Phaser.Scene): void {
  clearHighlight(scene);
  clearHintText(scene);
}

/** 方向 → 箭头字形（提示文案用） */
export function arrowOf(dir: 'up' | 'down' | 'left' | 'right'): string {
  return dir === 'up' ? '▲' : dir === 'down' ? '▼' : dir === 'left' ? '◀' : '▶';
}

/** 方向 → 中文方位词（与箭头一并写出，避免看反） */
export function dirNameOf(dir: 'up' | 'down' | 'left' | 'right'): string {
  return dir === 'up' ? '上' : dir === 'down' ? '下' : dir === 'left' ? '左' : '右';
}
