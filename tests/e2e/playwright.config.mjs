// @ts-check
import { defineConfig } from '@playwright/test';

/**
 * XGames E2E 测试配置
 * 
 * 自动启动后端服务，运行浏览器测试。
 * 游戏运行在 Phaser canvas 中，通过 page.evaluate() 与运行时交互。
 */
export default defineConfig({
  testDir: './flows',
  timeout: 180_000,       // 单测试最长 3 分钟（完整对局可能较久）
  expect: { timeout: 30_000 },
  fullyParallel: false,   // 单 worker，避免端口冲突
  workers: 1,
  retries: 0,
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: './playwright-report' }],
  ],
  use: {
    baseURL: 'http://localhost:3030',
    headless: true,
    viewport: { width: 1280, height: 720 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    // 忽略 HTTPS 错误（本地 HTTP 服务）
    ignoreHTTPSErrors: true,
  },
  // 自动启动后端服务
  webServer: {
    command: 'cd ../.. && go build -o ddz . && ./ddz -no-window -no-open',
    url: 'http://localhost:3030/healthz',
    timeout: 30_000,
    reuseExistingServer: true,
  },
});
