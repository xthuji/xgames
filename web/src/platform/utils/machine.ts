/**
 * 机器码生成器：基于浏览器指纹生成稳定的设备标识
 * 用于局域网对战时的用户唯一标识
 */

const MACHINE_ID_KEY = 'ddz_machine_id';

/**
 * 生成或获取机器码
 * - 优先从 localStorage 读取已保存的机器码
 * - 不存在时基于浏览器特征生成并保存
 */
export function getOrCreateMachineID(): string {
  let machineID = localStorage.getItem(MACHINE_ID_KEY);
  
  if (!machineID) {
    machineID = generateMachineID();
    localStorage.setItem(MACHINE_ID_KEY, machineID);
  }
  
  return machineID;
}

/**
 * 基于浏览器特征生成机器码（SHA256 hash）
 */
function generateMachineID(): string {
  const features = [
    navigator.userAgent,
    navigator.language,
    screen.width + 'x' + screen.height,
    screen.colorDepth,
    new Date().getTimezoneOffset(),
    navigator.hardwareConcurrency || 'unknown',
    (navigator as any).deviceMemory || 'unknown',
  ].join('|');
  
  // 简单 hash 算法（非加密级，仅用于标识）
  let hash = 0;
  for (let i = 0; i < features.length; i++) {
    const char = features.charCodeAt(i);
    hash = ((hash << 5) - hash) + char;
    hash = hash & hash; // Convert to 32bit integer
  }
  
  // 转换为 hex 字符串（16 字符）
  return Math.abs(hash).toString(16).padStart(16, '0');
}
