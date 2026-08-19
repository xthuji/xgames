import Phaser from 'phaser';
import { store } from '../state/store';
import { getGame } from '../registry';

/** BootScene 资源加载：图集/卡牌/音频（素材自 landlord/static 移植） */
export class BootScene extends Phaser.Scene {
  constructor() {
    super('Boot');
  }

  preload() {
    this.sound.setMute(store.muted);
    // 进度条
    const bar = this.add.rectangle(640, 360, 0, 12, 0xffd54f).setOrigin(0, 0.5);
    const frame = this.add.rectangle(640, 360, 404, 16, 0xffffff, 0.3).setStrokeStyle(2, 0xffffff);
    this.load.on('progress', (v: number) => bar.setSize(400 * v, 12));
    this.load.on('complete', () => {
      bar.destroy();
      frame.destroy();
    });

    // 图片与图集
    this.load.image('bg', 'i/bg.png');
    this.load.atlas('btn', 'i/btn.png', 'i/btn.json');
    this.load.spritesheet('poker', 'i/poker.png', { frameWidth: 90, frameHeight: 120 });

    // 音频（统一 MP3：WKWebView/Safari 不支持 Ogg Vorbis，解码失败会导致播放时抛异常）
    this.load.audio('music_room', 'audio/bg_room.mp3');
    this.load.audio('music_game', 'audio/bg_game.mp3');
    this.load.audio('deal', 'audio/deal.mp3');
    this.load.audio('win', 'audio/end_win.mp3');
    this.load.audio('lose', 'audio/end_lose.mp3');
    // m_score_* = 男声（叫分/加倍播报）
    // stone/chess_move/chess_capture = 棋类走子音效（五子棋落子、象棋走子/吃子）
    for (let i = 0; i <= 3; i++) {
      this.load.audio(`m_score_${i}`, `audio/m_score_${i}.mp3`);
    }
    // f_score_* = 女声（出牌报牌型）
    for (let i = 0; i <= 3; i++) {
      this.load.audio(`f_score_${i}`, `audio/f_score_${i}.mp3`);
    }
    this.load.audio('stone', 'audio/stone.mp3');
    this.load.audio('chess_move', 'audio/chess_move.mp3');
    this.load.audio('chess_capture', 'audio/chess_capture.mp3');
  }

  create() {
    // 单机游戏直达路由 #/game/<id>：书签/直链初次加载时直接进入对局场景，
    // 而不是总落在 localStorage 记录的游戏上（与 main.ts 的 hashchange 行为一致）。
    const m = /^#\/game\/(.+)$/.exec(location.hash);
    const g = m && getGame(m[1]);
    if (g?.singlePlayer) {
      store.setCurrentGame(g.id);
      this.scene.start(g.sceneKey);
      return;
    }
    this.scene.start('Lobby');
  }
}
