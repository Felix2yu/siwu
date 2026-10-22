import { defineConfig } from 'vitest/config'

// 与 vite.config.ts 分开：构建配置里不需要测试插件，测试也不需要 React/Tailwind 插件
// （当前用例全是纯函数与 fetch 桩，跑在 node 环境，起 DOM 只会拖慢）。
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      // CI 的 frontend-coverage-file 指向 web/coverage/lcov.info
      reporter: ['text', 'lcov'],
      reportsDirectory: 'coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}', 'src/main.tsx'],
    },
  },
})
