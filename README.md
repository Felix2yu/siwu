# 司寤 siwu

**把任意网页变成可订阅的 RSS，并在变化发生时立刻通知你。**

司寤（sī wù）定时抓取一个页面，按你框选的区域抽出条目或快照，
去重后输出 RSS 2.0 / Atom / JSON Feed，并通过 [apprise-go](https://github.com/unraid/apprise-go)
推送到 ntfy、Telegram、Bark、企业微信等 200+ 通道。

## 功能

- **四种数据源**：HTML 列表、JSON 接口（可在浏览器上下文里请求以穿透 WAF 签名）、整页/区域快照、已有 RSS 转发
- **可视化框选**：抓取预览 → 在页面上点选 → 自动给出绝对 XPath、重复块泛化选择器与字段推断，并实时显示抽取结果
- **按源自选抓取通道**：`http` 走纯请求，`browser` 走 CDP（本机 Chrome 或外置 headless-shell）
- **清洗规则**：正则替换/提取、去前后缀、日期归一、去标签等 12 个算子，保存时校验 RE2
- **过滤**：按标题/摘要/链接/分类做关键词或正则的包含与排除；条目级 `exclude` 选择器可剔除广告位
- **正文入库**：跟进详情页取正文（选择器优先、readability 兜底），写入 RSS 的 `content:encoded`
- **去重与更新**：`entry_key` + `content_hash` 双字段，支持只看新增 / 变更更新 / 变更留版本；`normalize` 正则让「只有访问量变了」不算更新
- **页面快照**：区域选择、噪声正则、`min_changed_lines` 阈值、行级 diff，变化才产出条目并留存版本
- **调度**：固定间隔或 cron（`0 9 * * 1-5`、`@every 2h`）、±10% 抖动、失败指数退避（上限 1h）、DB 原子租约
- **通知策略**：5 类触发（新增 / 更新 / 失败 / 恢复 / 连续空跑）× 立即 / 30s 合并 / 每日汇总 × 三档详略，另有同源节流、静默时段、失败重试 3 次
- **订阅与集成**：RSS 2.0（`content:encoded`）/ Atom / JSON Feed、gzip、ETag/Last-Modified/304、`?k=` 订阅令牌、OPML 导出
- **实时状态**：SSE 推送运行开始/结束与通知结果，列表页直接看到「哪个源在跑」
- **单二进制**：前端 `go:embed`，SQLite 纯 Go 驱动，`CGO_ENABLED=0`

## 快速开始

```bash
# Docker（推荐）
docker compose up -d
# 打开 http://localhost:8788

# 本地构建
./scripts/build.sh
./bin/siwu serve --addr :8788 --data ./data
```

首次使用：新建监控源 → 填页面地址 → 点「抓取预览」→ 在右侧页面里点一条内容 →
选「重复块（N 条）」→「套用字段推断」→ 核对抽取结果 → 保存。
之后在 RSS 阅读器里订阅 `http://<你的地址>:8788/f/<slug>.xml`。

## 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `SIWU_ADDR` | `:8788` | 监听地址 |
| `SIWU_DATA_DIR` | `./data` | 数据目录（SQLite、密钥） |
| `SIWU_BASE_URL` | 自动推导 | 对外地址，决定订阅链接与通知里的绝对地址 |
| `SIWU_PASSWORD` | 空 | 非空则全站需登录（`/api/healthz`、`/f/*` 除外） |
| `SIWU_FEED_TOKEN` | 空 | 非空则订阅需带 `?k=`，用于公开部署时保护订阅内容 |
| `SIWU_BROWSER_WS` | 空 | 全局浏览器 CDP 地址，如 `ws://browser:9222` |
| `SIWU_CHROME_PATH` | 空 | 本机 Chrome/Chromium 路径，不配则自动查找 |
| `SIWU_WORKERS` | `4` | 并发抓取数 |
| `SIWU_ALLOW_PRIVATE` | `false` | 允许抓取内网/回环地址（默认拒绝，防 SSRF） |
| `SIWU_DEBUG` | `false` | 调试日志 |

订阅端点：`/f/<slug>.xml`（RSS）、`.atom`、`.json`（JSON Feed 1.1）。配了 `SIWU_FEED_TOKEN` 时需追加 `?k=<token>`，界面里的订阅地址会自动带上。

常用接口：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/sources` | 源列表（含条目数、三种订阅地址） |
| POST | `/api/sources/{id}/run` | 手动运行一次；同一源正在跑时返回 409 |
| POST | `/api/try` | 用当前配置试跑，不写库 |
| GET | `/api/runs?source_id=&limit=` | 运行记录（含 warnings） |
| GET | `/api/stats` | 概览：源数、条目、今日新增、24h 异常/空跑 |
| GET | `/api/events` | SSE：`run.start` / `run.finish` / `notify.ok` / `notify.fail` |
| GET | `/api/export/opml` | 导出全部启用订阅为 OPML |
| POST | `/api/notify/flush` | 立即冲刷 batch/digest 队列 |
| POST | `/api/probe/fetch`、`/api/probe/suggest`、`/api/probe/extract` | 框选：抓预览、推断选择器、试抽取 |

## 部署

```yaml
services:
  siwu:
    image: ghcr.io/felix2yu/siwu:latest
    ports: ["8788:8788"]
    environment:
      PUID: "1000"
      PGID: "1000"
      SIWU_BASE_URL: https://siwu.example.com
    volumes: ["./data:/app/data"]
```

数据全在 `./data`（`siwu.db` + `secret.key`），备份目录即可。
需要 JS 渲染或风控站点时启用浏览器侧车：`docker compose --profile browser up -d`，
再设 `SIWU_BROWSER_WS=ws://browser:9222`（或按源单独填）。

## 开发

```bash
make dev            # 后端 :8788
cd web && pnpm dev  # 前端 :5173（代理到 8788）

make test           # go test ./internal/... + 前端单测
make lint           # gofmt + go vet + tsc + eslint
make build          # 产出 bin/siwu
make smoke          # 起临时实例验证健康检查与前端外壳
```

后端 Go 1.27，前端 Node 26 + pnpm（版本见 `web/package.json` 的 `packageManager`）。
TypeScript 固定在 6.0.3：`typescript-eslint` 尚未支持 TS 7，升 7 会让 lint 无法启动。

## 结构

```
cmd/siwu/            入口与版本注入
internal/
  fetch/             抓取（HTTP / CDP）
  extract/           选择器 DSL（XPath / CSS / JSONPath）、快照
  transform/         清洗规则（12 个算子）
  article/           正文抽取与净化
  pipeline/          一次运行的唯一编排路径
  scheduler/         调度：cron / 等待 / 租约 / 退避
  cronexpr/          cron 解析与下次时刻推算
  notify/            apprise 通知策略与聚合队列
  events/            进程内事件总线（SSE 源）
  probe/             预览代理、框选支撑
  store/             SQLite 与迁移
  secretbox/         通知地址的 AES-GCM 加密
  httpapi/           REST / SSE / 订阅端点
web/                 React 前端（构建后内嵌）
```

## 许可

MIT
