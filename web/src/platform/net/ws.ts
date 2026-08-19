import { ConnectedPayload, Message, MessageType, MsgTypes, ReconnectPayload, ReconnectedPayload } from '../protocol';
import { getOrCreateMachineID } from '../utils/machine';
import { registerFlowCleanup } from '../flow/gameFlow';
import { store } from '../state/store';

type Handler = (payload: any) => void;

/** 连接身份（connected/reconnected 均携带）：供场景在消息丢失后兜底恢复 */
interface IdentityInfo {
  player_id: string;
  player_name: string;
  score: number;
  rank: number;
}

const TOKEN_KEY = 'ddz_reconnect_token';
const PID_KEY = 'ddz_player_id';
const HEARTBEAT_MS = 30000; // 30秒心跳，降低网络和处理开销
const MAX_MISSED_PONG = 3;
const MAX_BACKOFF_MS = 15000;
const MAX_PENDING = 200;
const RECONNECT_ACK_MS = 5000; // 重连恢复应答的安全网超时

/**
 * 关键状态消息白名单：到达时若尚无订阅者（如场景切换间隙），
 * 缓存等待订阅后重放，避免 connected/game_start 等一次性消息丢失。
 * 平台消息内置；游戏私有消息由各游戏经 registerReplayable 登记（见 platform/registry）。
 */
const REPLAYABLE: Set<MessageType> = new Set([
  MsgTypes.MsgConnected,
  MsgTypes.MsgReconnected,
  MsgTypes.MsgRoomCreated,
  MsgTypes.MsgRoomJoined,
  MsgTypes.MsgPlayerJoined,
  MsgTypes.MsgPlayerLeft,
  MsgTypes.MsgPlayerReady,
  MsgTypes.MsgMatchFound,
  MsgTypes.MsgGameStart,
  MsgTypes.MsgGameOver,
  MsgTypes.MsgPlayerOffline,
  MsgTypes.MsgPlayerOnline,
  MsgTypes.MsgMaintenancePush,
  MsgTypes.MsgProfileUpdated,
  MsgTypes.MsgGameSyncResult,
  MsgTypes.MsgGameState,
]);

/** 游戏登记自己的可重放关键消息（游戏模块加载时调用） */
export function registerReplayable(types: MessageType[]): void {
  for (const t of types) REPLAYABLE.add(t);
}

/**
 * NetClient WebSocket 封装：JSON 信封收发、15s 心跳、断线指数退避自动重连，
 * 重连成功自动用 localStorage 中的令牌发 reconnect（技术设计 9.3）。
 */
class NetClient {
  private ws: WebSocket | null = null;
  private url = '';
  private handlers = new Map<MessageType, Set<Handler>>();
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private missedPong = 0;
  private backoff = 1000;
  private closedByUser = false;
  private connecting = false;
  private pending: Message[] = []; // 无订阅者的关键消息，订阅后重放
  private awaitingReconnect = false; // 已发 reconnect，等待 reconnected/error 应答
  private reconnectAckTimer: ReturnType<typeof setTimeout> | null = null;
  private tempConnected: Message | null = null; // 重连等待期间暂存的握手临时身份消息
  private lastIdentity: IdentityInfo | null = null; // 最近一次连接身份缓存（不受订阅者/缓冲清理影响）

  /** 重连中回调（UI 显示遮罩用） */
  onReconnecting: (attempt: number) => void = () => {};
  /** 连接彻底可用（首连或重连完成） */
  onOpen: () => void = () => {};

  /** 当前连接的最近身份（可能为 null：尚未收到 connected/reconnected） */
  get identity(): IdentityInfo | null {
    return this.lastIdentity;
  }

  get connected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  connect(url: string) {
    this.url = url;
    this.closedByUser = false;
    this.open();
  }

  private open() {
    if (this.connecting || this.connected) return;
    this.connecting = true;
    
    // 在 URL 中附加机器码（WebSocket 不支持自定义 header）
    const machineID = getOrCreateMachineID();
    const urlWithMachine = `${this.url}?machine_id=${machineID}`;
    
    const ws = new WebSocket(urlWithMachine);
    this.ws = ws;

    ws.onopen = () => {
      this.connecting = false;
      this.backoff = 1000;
      this.missedPong = 0;
      this.startHeartbeat();
      // 若本地存有身份令牌，自动发起重连恢复
      const token = localStorage.getItem(TOKEN_KEY);
      const pid = localStorage.getItem(PID_KEY);
      if (token && pid) {
        this.awaitingReconnect = true;
        this.send(MsgTypes.MsgReconnect, { token, player_id: pid } satisfies ReconnectPayload);
        // 安全网：令牌失效（服务端回 error 而非 reconnected）或应答丢失时，
        // 回退为新玩家继续，避免 onOpen 永不触发、重连遮罩永久挡住界面
        this.reconnectAckTimer = setTimeout(() => this.abortReconnect('超时'), RECONNECT_ACK_MS);
      } else {
        this.onOpen();
      }
    };

    ws.onmessage = (ev) => {
      let msg: Message;
      try {
        msg = JSON.parse(ev.data as string);
      } catch {
        return;
      }
      // 身份与令牌持久化（connected / reconnected 均刷新）
      if (msg.type === MsgTypes.MsgConnected) {
        const p = msg.payload as ConnectedPayload;
        if (this.awaitingReconnect) {
          // 握手后的临时新身份：旧身份恢复以 reconnected 为准；
          // 暂存而非丢弃，恢复失败时回退以此消息继续，避免身份消息彻底丢失、界面永久卡在"连接中"
          this.tempConnected = msg;
          return;
        }
        localStorage.setItem(PID_KEY, p.player_id);
        localStorage.setItem(TOKEN_KEY, p.reconnect_token);
        this.lastIdentity = { player_id: p.player_id, player_name: p.player_name, score: p.score, rank: p.rank };
        this.onOpen();
      } else if (msg.type === MsgTypes.MsgReconnected) {
        const p = msg.payload as ReconnectedPayload;
        localStorage.setItem(PID_KEY, p.player_id);
        localStorage.setItem(TOKEN_KEY, p.reconnect_token);
        this.tempConnected = null; // 旧身份已恢复，临时身份作废
        this.lastIdentity = { player_id: p.player_id, player_name: p.player_name, score: p.score, rank: p.rank };
        this.finishReconnectWait();
        this.onOpen();
      } else if (msg.type === MsgTypes.MsgError && this.awaitingReconnect) {
        // 重连被拒（令牌无效/过期）：清除本地身份，以握手时分配的新玩家继续
        this.abortReconnect('令牌失效');
      } else if (msg.type === MsgTypes.MsgPong) {
        this.missedPong = 0;
      }
      this.dispatch(msg);
    };

    ws.onclose = () => {
      this.connecting = false;
      this.stopHeartbeat();
      this.finishReconnectWait();
      this.awaitingReconnect = false;
      if (this.closedByUser) return;
      this.scheduleReconnect();
    };

    ws.onerror = () => {
      /* onclose 会随后触发重连 */
    };
  }

  private scheduleReconnect() {
    this.onReconnecting(this.backoff / 1000);
    setTimeout(() => this.open(), this.backoff);
    this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF_MS);
  }

  /** 正常结束重连等待（收到 connected/reconnected） */
  private finishReconnectWait() {
    this.awaitingReconnect = false;
    this.tempConnected = null;
    if (this.reconnectAckTimer) {
      clearTimeout(this.reconnectAckTimer);
      this.reconnectAckTimer = null;
    }
  }

  /** 重连失败回退：清除本地身份，以当前连接的新玩家身份继续（界面恢复可用） */
  private abortReconnect(reason: string) {
    if (!this.awaitingReconnect) return;
    // 先取出暂存的临时身份（finishReconnectWait 会清空它）
    const fallback = this.tempConnected;
    this.finishReconnectWait();
    console.warn(`重连恢复失败（${reason}），以新玩家身份继续`);
    this.clearIdentity();
    // 回退到手握的握手临时身份：持久化并派发给场景，
    // 确保 store.playerID / 名字栏等正常初始化，不再永久卡在"连接中"
    if (fallback) {
      const p = fallback.payload as { player_id: string; player_name: string; reconnect_token: string; score?: number; rank?: number };
      localStorage.setItem(PID_KEY, p.player_id);
      localStorage.setItem(TOKEN_KEY, p.reconnect_token);
      this.lastIdentity = { player_id: p.player_id, player_name: p.player_name, score: p.score ?? 0, rank: p.rank ?? 0 };
      this.dispatch(fallback);
    }
    this.onOpen();
  }

  private startHeartbeat() {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      if (!this.connected) return;
      this.missedPong++;
      if (this.missedPong >= MAX_MISSED_PONG) {
        this.ws?.close(); // 触发 onclose → 重连
        return;
      }
      this.send(MsgTypes.MsgPing, { timestamp: Date.now() });
    }, HEARTBEAT_MS);
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  /** 注册消息处理器（返回取消函数）；注册时重放该类型的已缓冲消息 */
  on(type: MessageType, handler: Handler): () => void {
    let set = this.handlers.get(type);
    if (!set) {
      set = new Set();
      this.handlers.set(type, set);
    }
    set.add(handler);

    const queued = this.pending.filter((m) => m.type === type);
    if (queued.length > 0) {
      this.pending = this.pending.filter((m) => m.type !== type);
      // 微任务重放，避免同步递归（handler 内可能切场景触发新订阅）
      queueMicrotask(() => queued.forEach((m) => this.dispatch(m)));
    }
    return () => set!.delete(handler);
  }

  /** 清除所有缓冲的待重放消息（场景切换后旧消息已失效时调用） */
  clearPending() {
    this.pending = [];
  }

  send(type: MessageType, payload?: unknown) {
    if (!this.connected) return;
    this.ws!.send(JSON.stringify({ type, payload } satisfies Message));
  }

  /** 清空本地身份（重新开始） */
  clearIdentity() {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(PID_KEY);
  }

  private dispatch(msg: Message) {
    const set = this.handlers.get(msg.type);
    if (!set || set.size === 0) {
      // 无订阅者：关键状态消息缓冲等待重放（场景切换间隙保护）
      if (REPLAYABLE.has(msg.type)) {
        this.pending.push(msg);
        if (this.pending.length > MAX_PENDING) this.pending.shift();
      }
      return;
    }
    for (const h of [...set]) {
      try {
        h(msg.payload);
      } catch (e) {
        console.error(`处理消息 ${msg.type} 失败`, e);
      }
    }
  }
}

export const net = new NetClient();

// 注册状态机驱动的清理回调：退出 GAME 状态时自动清理 pending 缓冲消息
registerFlowCleanup(() => net.clearPending());

/**
 * 身份兜底恢复：connected/reconnected 到达时若无订阅者（场景切换间隙）且
 * 缓冲被 clearPending 清理，store.playerID 会永久丢失 —— 导致落子守卫
 * （currentTurn !== playerID）静默丢弃所有点击、面板找不到自己而错乱。
 * 场景创建时调用：用网络层缓存的最近连接身份同步 store（该身份始终
 * 代表当前 WS 连接，覆盖旧值是安全的）。
 * @returns true 表示本次执行了恢复
 */
export function hydrateIdentity(): boolean {
  const id = net.identity;
  if (!id || !id.player_id) return false;
  if (store.playerID === id.player_id) return false;
  store.playerID = id.player_id;
  if (id.player_name) store.playerName = id.player_name;
  return true;
}
