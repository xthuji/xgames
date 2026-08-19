/**
 * 复盘报告 API 客户端（各游戏 Scene 共用）。
 *
 * 服务端报告保存路径：<复盘根目录>/<game>/<roomCode>_<playerId>_<timestamp>.txt
 * （以真人玩家为中心的复盘报告，每个玩家一份；复盘根目录由后端按运行环境解析：
 *  开发模式在项目根 data/replays/，打包运行在用户持久化目录下），
 * 且报告在对局结束后异步生成落盘，因此：
 *  1. 列表/详情请求必须携带 game 参数定位到对应游戏的报告子目录；
 *  2. 刚结束时文件可能尚未写完，需要短暂重试。
 */

export interface ReplayFileInfo {
  filename: string;
  gameId: string;
  createdAt: number;
  size: number;
}

interface ReplayListResponse {
  total: number;
  items: ReplayFileInfo[];
}

/** 重试次数与间隔：报告异步落盘通常在 1s 内完成 */
const MAX_ATTEMPTS = 4;
const RETRY_DELAY_MS = 800;

/** 报告尚未生成（所有重试后列表仍为空）时抛出的哨兵错误文案 */
export const REPLAY_NOT_READY = '暂无复盘报告';

/**
 * 获取指定房间当前玩家最新一份复盘报告文本。
 * @param game 游戏 ID（chess / ddz / gomoku / mahjong）
 * @param roomCode 房间码
 * @param playerId 当前玩家 ID（新格式报告文件名含 playerId，用于只取本人报告）
 * @throws 报告未生成时抛出 message 为 REPLAY_NOT_READY 的 Error
 */
export async function fetchLatestReplayText(
  game: string,
  roomCode: string,
  playerId?: string,
): Promise<string> {
  const playerQuery = playerId ? `&playerId=${encodeURIComponent(playerId)}` : '';
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    const listRes = await fetch(
      `/api/replays?limit=1&roomCode=${encodeURIComponent(roomCode)}&game=${encodeURIComponent(game)}${playerQuery}`,
    );
    if (!listRes.ok) {
      throw new Error(`获取复盘报告列表失败 (${listRes.status})`);
    }
    const data = (await listRes.json()) as ReplayListResponse;
    const items = data.items ?? [];
    if (items.length > 0) {
      const detailRes = await fetch(
        `/api/replays/${encodeURIComponent(items[0].filename)}?game=${encodeURIComponent(game)}`,
      );
      if (!detailRes.ok) {
        throw new Error(`获取复盘报告详情失败 (${detailRes.status})`);
      }
      return await detailRes.text();
    }
    // 报告尚未落盘：短暂等待后重试（对局结束后服务端异步生成）
    if (attempt < MAX_ATTEMPTS) {
      await new Promise((r) => setTimeout(r, RETRY_DELAY_MS));
    }
  }
  throw new Error(REPLAY_NOT_READY);
}
