# AGENTS.md

面向 AI 代理的项目约定。跨仓库契约以 [Felix2yu/.github](https://github.com/Felix2yu/.github) 为准。

## 目录

- `cmd/siwu/`：唯一 main 包；`version.go` 的 `version` 由 CI 用 `-X` 注入，必须保持被引用
- `internal/*`：业务包，禁止跨包反向依赖（`pipeline` 依赖能力包，能力包不依赖 `pipeline`）
- `internal/web/`：`//go:embed all:dist` 的占位与读取；`dist/` 只提交 `.gitkeep`
- `web/`：React 前端工程，三件套（`.nvmrc` 在仓库根、`packageManager`、`pnpm-lock.yaml`）
- `scripts/build.sh`：唯一构建实现；`--stage-only` 供 CI 复用，不要另写一份拷贝逻辑

## 命令

```bash
make dev / build / test / lint / fmt / docker / clean / smoke
```

前端一律用 pnpm，不要生成 `package-lock.json` / `yarn.lock` / `bun.lockb`。

## 技术约束

- Go 1.27+，`CGO_ENABLED=0`（SQLite 用 `modernc.org/sqlite`，不要引入 cgo 依赖）
- 新标准库优先：`http.ServeMux` 方法路由、`http.CrossOriginProtection`、`encoding/json/v2`、
  `os.Root`、`crypto/rand.Text`、`testing/synctest`
- 前端：React 19 + Vite 8（Rolldown）+ Tailwind 4（CSS-first `@theme`）+ React Compiler
- TypeScript 固定 6.0.3：`typescript-eslint@8` 的 peer 是 `<6.1.0`，升 7 会让 `eslint` 加载即失败

## 实现约定

- **运行只有一条写路径**：手动、定时、试跑都走 `pipeline.Execute`，只差 `dryRun`；不要在 handler 里另写抓取逻辑
- **通知绝不阻塞主流程**：`notify.Dispatch` 返回的告警写进 `runs.warnings`，不能把发送失败升级成运行失败
- **判重先清洗**：清洗规则必须在计算 `content_hash` 之前生效，否则「标题 NEW」与「标题」会成为两条
- **哈希语义变更要迁移**：改 `content_hash` 参与字段时，必须同时清空历史哈希，并让空哈希只补基线不算更新（见 `0002_rehash_baseline.sql`）
- **调度时间统一走 `cronexpr`**：不要在别处手写 cron 换算；失败退避与 cron 的组合规则只在 `planNext` 里
- **SSE 事件类型固定**：`run.start` / `run.finish` / `notify.ok` / `notify.fail`，前端 `web/src/live.ts` 依赖它们

## 改动后自查

1. `gofmt -l ./cmd ./internal` 无输出
2. `go vet ./...`
3. `go test ./internal/...`（`cmd` 带 embed，测试范围不含 `./...`）
4. `cd web && pnpm run check && pnpm run build && pnpm run test`（`check` = tsc + eslint；`test` 是 vitest，CI 用 `test:coverage`）
5. 改过前端或 embed 后：`./scripts/build.sh && ./scripts/smoke.sh`
6. 涉及抓取逻辑时：至少用一个真实站点跑一次「试跑」，确认命中数不为 0

## 不要做

- 不要用 `chi`/`gin` 等路由框架（标准库够用）
- 不要把 Chrome 打进 siwu 镜像（外挂 `browser` profile）
- 不要在预览代理里放行脚本：`probe.sanitize` 必须继续剥离 `script` / `on*` / CSP
- 不要把用户可见文案写成翻译腔；界面文案用自然中文
