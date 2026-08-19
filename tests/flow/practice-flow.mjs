// 完整人机对战流程测试脚本
// 连接后端 → 选择难度 → 验证叫地主/出牌/结算链路

import WebSocket from 'ws';
import { setTimeout } from 'timers/promises';

const WS_URL = 'ws://127.0.0.1:3030/ws';
const DIFFICULTY = process.env.DIFFICULTY || 'normal';
const MAX_MESSAGES = 800;

const Msgs = {
  Connected: 'connected',
  Reconnected: 'reconnected',
  PracticeMatch: 'practice_match',
  RoomJoined: 'room_joined',
  Ready: 'ready',
  GameStart: 'game_start',
  DealCards: 'deal_cards',
  BidTurn: 'bid_turn',
  BidResult: 'bid_result',
  Landlord: 'landlord',
  PlayTurn: 'play_turn',
  CardPlayed: 'card_played',
  PlayerPass: 'player_pass',
  GameOver: 'game_over',
  Error: 'error',
};

const EVENTS = [
  Msgs.GameStart, Msgs.DealCards, Msgs.BidTurn, Msgs.BidResult,
  Msgs.Landlord, Msgs.PlayTurn, Msgs.CardPlayed, Msgs.PlayerPass,
  Msgs.GameOver, Msgs.Error,
];

function log(tag, msg) {
  const ts = new Date().toISOString().slice(11, 23);
  console.log(`[${ts}] ${tag} ${msg}`);
}

function cardToStr(c) {
  const suits = ['♠', '♥', '♣', '♦', '★'];
  const ranks = ['3', '4', '5', '6', '7', '8', '9', '10', 'J', 'Q', 'K', 'A', '2', 'BJ', 'RJ'];
  return `${suits[c.suit] ?? '?'}${ranks[c.rank - 3] ?? c.rank}`;
}

function cardsToStr(cards) {
  return cards.map(cardToStr).join(' ');
}

function sortDesc(cards) {
  return [...cards].sort((a, b) => b.rank - a.rank || b.suit - a.suit);
}

function findPlayable(hand, mustPlay, canBeat, lastPlayed) {
  if (mustPlay) {
    // 领出：出最小单张
    const sorted = sortDesc(hand);
    return [sorted[sorted.length - 1]];
  }
  if (!canBeat || !lastPlayed?.length) return null;
  // 跟牌：尝试用手中最小的大牌去压
  const targetRank = Math.max(...lastPlayed.map((c) => c.rank));
  const targetCount = lastPlayed.length;

  if (targetCount === 1) {
    // 对方出单张，压一张更大的单
    const sorted = sortDesc(hand);
    const beat = sorted.find((c) => c.rank > targetRank);
    if (beat) return [beat];
  } else if (targetCount === 2) {
    // 对方出对子，找更大的对子
    const byRank = {};
    hand.forEach((c) => (byRank[c.rank] = byRank[c.rank] || []).push(c));
    const sortedRanks = Object.keys(byRank)
      .map(Number)
      .filter((r) => byRank[r].length >= 2 && r > targetRank)
      .sort((a, b) => a - b);
    if (sortedRanks.length > 0) return byRank[sortedRanks[0]].slice(0, 2);
  }
  // 尝试炸弹
  const byRank = {};
  hand.forEach((c) => (byRank[c.rank] = byRank[c.rank] || []).push(c));
  for (const r of Object.keys(byRank).map(Number).sort((a, b) => a - b)) {
    if (byRank[r].length === 4) return byRank[r];
  }
  // 尝试王炸
  const hasRJ = hand.some((c) => c.rank === 17);
  const hasBJ = hand.some((c) => c.rank === 16);
  if (hasRJ && hasBJ) {
    return [hand.find((c) => c.rank === 17), hand.find((c) => c.rank === 16)];
  }
  return null;
}

async function run() {
  log('TEST', `=== 人机对战流程测试 (difficulty=${DIFFICULTY}) ===`);

  const ws = new WebSocket(WS_URL);
  const received = [];
  let connected = false;

  ws.on('open', () => {
    log('WS', '连接成功');
  });

  ws.on('message', (data) => {
    try {
      const msg = JSON.parse(data.toString());
      received.push(msg);
      const tag = msg.type?.padEnd(16);
      let detail = '';
      if (msg.type === Msgs.Connected) {
        detail = `player_id=${msg.payload?.player_id}`;
      } else if (msg.type === Msgs.GameStart) {
        detail = `difficulty=${msg.payload?.difficulty}, players=${msg.payload?.players?.length}`;
      } else if (msg.type === Msgs.DealCards) {
        detail = `hand=${cardsToStr(sortDesc(msg.payload?.cards ?? []))}`;
      } else if (msg.type === Msgs.BidTurn) {
        detail = `player=${msg.payload?.player_id}, phase=${msg.payload?.phase}, high_bid=${msg.payload?.high_bid}`;
      } else if (msg.type === Msgs.BidResult) {
        detail = `player=${msg.payload?.player_id}, phase=${msg.payload?.phase}, score=${msg.payload?.score}, double=${msg.payload?.double}, multiplier=${msg.payload?.multiplier}`;
      } else if (msg.type === Msgs.Landlord) {
        detail = `${msg.payload?.player_name} 成为地主, bottom=${cardsToStr(msg.payload?.bottom_cards ?? [])}`;
      } else if (msg.type === Msgs.PlayTurn) {
        detail = `player=${msg.payload?.player_name}, must_play=${msg.payload?.must_play}, can_beat=${msg.payload?.can_beat}`;
      } else if (msg.type === Msgs.CardPlayed) {
        detail = `player=${msg.payload?.player_name}, type=${msg.payload?.hand_type}, cards=${cardsToStr(msg.payload?.cards ?? [])}`;
      } else if (msg.type === Msgs.PlayerPass) {
        detail = `player=${msg.payload?.player_name}`;
      } else if (msg.type === Msgs.GameOver) {
        detail = `winner=${msg.payload?.winner_name}, multiplier=${msg.payload?.multiplier}`;
      } else if (msg.type === Msgs.Error) {
        detail = `code=${msg.payload?.code}, msg=${msg.payload?.message}`;
      }
      log(' ← ', `${tag} ${detail}`);
    } catch (e) {
      log('ERR', `解析失败: ${e.message}`);
    }
  });

  ws.on('error', (e) => log('ERR', `WS 错误: ${e.message}`));
  ws.on('close', () => log('WS', '连接关闭'));

  // 等待 connected
  await setTimeout(500);
  const connectedMsg = received.find((m) => m.type === Msgs.Connected);
  if (!connectedMsg) {
    log('FAIL', '未收到 connected 消息');
    process.exit(1);
  }
  log('OK', '已收到 connected');

  // 发送 practice_match
  log(' → ', `发送 practice_match (difficulty=${DIFFICULTY})`);
  ws.send(JSON.stringify({ type: Msgs.PracticeMatch, payload: { difficulty: DIFFICULTY } }));

  let readySent = false;

  let myPlayerID = connectedMsg.payload.player_id;
  let mySeat = 0;
  let myHand = [];
  let landlordID = null;
  let gameOver = false;
  let turnCount = 0;

  for (let i = 0; i < MAX_MESSAGES; i++) {
    await setTimeout(300);

    // 检查新消息
    const latest = received[received.length - 1];
    if (!latest) continue;

    if (latest.type === Msgs.RoomJoined && !readySent) {
      // 人机对战与普通房间流程一致：机器人已就绪，真人需手动准备后才开局
      readySent = true;
      log(' → ', '发送 ready（真人准备）');
      ws.send(JSON.stringify({ type: Msgs.Ready, payload: {} }));
    }

    if (latest.type === Msgs.GameStart) {
      const diff = latest.payload?.difficulty;
      log('CHK', `GameStart.difficulty = ${diff}`);
      if (diff !== DIFFICULTY) {
        log('WARN', `difficulty 不匹配: 期望 ${DIFFICULTY}, 实际 ${diff}`);
      }
      mySeat = latest.payload.players.find((p) => p.id === myPlayerID)?.seat ?? 0;
      log('INFO', `我的座位: ${mySeat}`);
    }

    if (latest.type === Msgs.DealCards) {
      myHand = latest.payload.cards;
      log('INFO', `发牌完成, 手牌 ${myHand.length} 张`);
    }

    if (latest.type === Msgs.Landlord) {
      landlordID = latest.payload.player_id;
      if (landlordID === myPlayerID) {
        myHand = [...myHand, ...latest.payload.bottom_cards];
        log('INFO', `我成为地主, 底牌 ${cardsToStr(latest.payload.bottom_cards)}`);
      }
    }

    if (latest.type === Msgs.BidTurn) {
      if (latest.payload.player_id === myPlayerID) {
        if (latest.payload.phase === 'double') {
          // 简单策略: 一律不加倍
          log(' → ', 'double(false)');
          ws.send(JSON.stringify({ type: 'double', payload: { double: false } }));
        } else {
          // 简单策略: 有大王或两个2就叫 1 分
          const hasRJ = myHand.some((c) => c.rank === 17);
          const two2 = myHand.filter((c) => c.rank === 15).length >= 2;
          const score = (hasRJ || two2) && (latest.payload.high_bid ?? 0) < 1 ? 1 : 0;
          log(' → ', `bid(${score}) [hasRJ=${hasRJ}, two2=${two2}, high=${latest.payload.high_bid}]`);
          ws.send(JSON.stringify({ type: 'bid', payload: { score } }));
        }
      }
    }

    if (latest.type === Msgs.PlayTurn) {
      if (latest.payload.player_id === myPlayerID) {
        turnCount++;
        const played = findPlayable(
          myHand,
          latest.payload.must_play,
          latest.payload.can_beat,
          latest.payload.last_played,
        );
        if (played && played.length > 0) {
          played.forEach((c) => {
            const idx = myHand.findIndex((h) => h.suit === c.suit && h.rank === c.rank);
            if (idx >= 0) myHand.splice(idx, 1);
          });
          log(' → ', `play [${cardsToStr(played)}] (turn#${turnCount})`);
          ws.send(JSON.stringify({ type: 'play_cards', payload: { cards: played } }));
        } else {
          log(' → ', `pass (turn#${turnCount})`);
          ws.send(JSON.stringify({ type: 'pass', payload: {} }));
        }
      }
    }

    if (latest.type === Msgs.GameOver) {
      gameOver = true;
      log('TEST', '=== 游戏结束 ===');
      log('OK', `赢家: ${latest.payload.winner_name} (${latest.payload.is_landlord ? '地主' : '农民'})`);
      log('OK', `倍数: ×${latest.payload.multiplier}`);
      latest.payload.scores?.forEach((s) => {
        log('OK', `  ${s.player_name}: ${s.score > 0 ? '+' : ''}${s.score}`);
      });
      latest.payload.player_hands?.forEach((h) => {
        log('OK', `  ${h.player_name} 剩余: ${cardsToStr(h.cards)}`);
      });
      break;
    }

    if (latest.type === Msgs.Error) {
      log('FAIL', `错误: ${latest.payload.message}`);
      ws.close();
      process.exit(1);
    }
  }

  if (!gameOver) {
    log('WARN', `未在 ${MAX_MESSAGES} 条消息内完成，已处理 ${turnCount} 次出牌`);
  } else {
    log('TEST', `链路验证通过 ✓ (共 ${received.length} 条消息, ${turnCount} 次我方出牌)`);
  }

  ws.close();
  process.exit(gameOver ? 0 : 1);
}

run().catch((e) => {
  console.error('FATAL:', e);
  process.exit(1);
});