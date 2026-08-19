import { defineConfig } from 'vite';

export default defineConfig({
  base: './',
  publicDir: 'assets',
  build: {
    outDir: 'dist',
    assetsInlineLimit: 0,
    rollupOptions: {
      // 多页面：index.html 为外壳（侧边栏 + iframe），game.html 为游戏帧（Phaser）
      input: {
        main: 'index.html',
        game: 'game.html',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // 开发模式：WS 代理到本地 Go 服务（config.yaml server.port=3030）
      '/ws': {
        target: 'ws://127.0.0.1:3030',
        ws: true,
      },
    },
  },
});
