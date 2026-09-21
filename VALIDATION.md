# 验收记录

验收日期：2026-08-22

## 静态质量

- `go test ./...`：通过。
- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- `go build ./...`：通过。
- `npm run typecheck`：通过。
- `npm run build`：通过。Vite 仅报告主包大于 500 kB 的性能提示，不影响构建与运行。
- 非测试 Go 代码：3147 行、38 个 `.go` 文件，符合提示词 3000–4200 行、30–42 文件要求。

## 空卷 Compose 与 API

执行 `KEEP_RUNNING=1 ./scripts/validate.sh` 前，脚本已运行 `docker compose down -v --remove-orphans`。PostgreSQL、Redis、MinIO、后端和前端均从空卷启动并通过健康检查。

- viewer 可以读取业务数据；写接口与审计接口均返回 403。
- operator 创建优先级 v1 并补充证据形成 v2；直接定稿返回 403。
- reviewer 自己拟制后自行复核返回 422。
- 独立 reviewer 将 operator 草稿定为 urgent v3。
- v1/v2/v3 的证据、actor 和 request ID 均按原值保留。
- 终态后再次编辑返回 422。
- 审计汇总包含 create、update 和 transition。

## 内置 Browser

仅使用 Codex 内置 Browser 验证，没有调用外部 Chrome 或独立 Playwright。

- viewer 登录后看不到新增、推进和审计导航；直接访问 `/audit` 被守卫重定向到 `/bridges`。
- operator 在 `/defects` 创建缺陷并从 new 推进到 verified；`SeverityBadge` 和证据摘要正常显示。
- `/inspections` 与 `/defects` 均渲染共享 `EvidenceGallery`。
- operator 在 `/priorities` 看不到定稿按钮；reviewer 可对他人拟制的 PD-001 定稿，自己拟制的草稿无操作按钮。
- `/audit` 正确显示操作者、状态迁移和 request ID。
- `/bridges`、`/inspections`、`/defects`、`/priorities`、`/audit` 在 390×844 下的 `innerWidth`、`body.scrollWidth`、`documentElement.scrollWidth` 均为 390。
- 浏览器控制台 error/warning：0。

默认 `./scripts/validate.sh` 会在结束时执行 `docker compose down -v --remove-orphans`，不会保留本项目容器、网络或命名卷。

## 严重缺陷触发优先级复查（2026-09-21）

- `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`：通过，新增 `service/priority_recheck_test.go` 覆盖触发、去重、三种处理、职责分离与失败保护。
- `npm run typecheck`、`npm run build`：通过。
- SQLite 开发模式端到端验证（`DATABASE_DRIVER=sqlite`）：
  - 严重缺陷（critical）核实且同桥存在 observe/restrict 终态决定时，`/api/rechecks` 生成一条 pending 复查，原决定状态与版本不变。
  - 同一缺陷再次核实（verified→monitoring→verified）仍只有一条复查（唯一约束去重）。
  - 普通缺陷（high）核实、同桥无终态决定时均不触发。
  - viewer/operator 处理复查返回 403；原拟制人处理返回 422；空替代依据返回 400。
  - 维持/解除后原决定状态、版本与版本链不变；升级为 urgent 后决定版本 +1 并追加含替代依据的不可变版本。
  - 已处理复查再次处理返回 422，决定与版本链保持原值。
  - `/api/audits/PriorityRecheck/:id` 可回读 recheck-trigger 与 recheck-resolve 记录及替代依据。
- `scripts/validate.sh` 已纳入上述 API 验收步骤（含种子数据 DF-004 → PD-002 待复查事项检查）。
