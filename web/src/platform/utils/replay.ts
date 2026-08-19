/**
 * 复盘功能配置工具
 * 
 * 提供统一的接口来读取和管理复盘功能的启用状态
 */

const REPLAY_ENABLED_KEY = 'replay_enabled';

/**
 * 检查复盘功能是否启用
 * @returns true 表示启用，false 表示禁用
 */
export function isReplayEnabled(): boolean {
  const saved = localStorage.getItem(REPLAY_ENABLED_KEY);
  // 默认启用（如果未设置过）
  return saved === null ? true : saved === 'true';
}

/**
 * 启用或禁用复盘功能
 * @param enabled 是否启用
 */
export function setReplayEnabled(enabled: boolean): void {
  localStorage.setItem(REPLAY_ENABLED_KEY, String(enabled));
}

/**
 * 获取复盘功能设置的原始值（用于调试）
 */
export function getReplaySettingRaw(): string | null {
  return localStorage.getItem(REPLAY_ENABLED_KEY);
}

/**
 * 检查是否应该显示复盘按钮
 * 如果复盘功能被禁用，则不显示复盘按钮
 */
export function shouldShowReplayButton(): boolean {
  return isReplayEnabled();
}
