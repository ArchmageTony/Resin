# Tasks

## 1. 合并与冲突解决

- [x] 1.1 清理临时引用 `refs/remotes/upstream-tmp/master`，添加正式 remote `upstream`（`https://github.com/Resinat/Resin.git`，fetch-only），`git fetch upstream` 验证 HEAD 为 `8e9f74e`（上游 19 提交、v1.4.0+v1.4.1）
- [x] 1.2 执行 `git merge upstream/master`（merge commit，不改上游提交），确认文本冲突仅 `internal/state/migrate.go`，其余 112 文件自动合入；验证 `git diff --stat` 与已知 diffstat（113 文件 +6459/-783）一致
- [x] 1.3 重命名上游迁移 `internal/state/migrations/state/000010_subscription_chain_proxy.{up,down}.sql` → `000011_...`（`git mv`，文件内容不动），确认 `internal/state/migrations/state/` 下无重复版本号：`ls` 列出 000001–000011 各恰一对 up/down
- [x] 1.4 解决 `internal/state/migrate.go` 冲突：保留 `stateVersionPlatformLatencyTestURL = 10`，新增 `stateVersionSubscriptionChainProxy = 11`，`stateLatestVersion = stateVersionSubscriptionChainProxy`；`go build ./internal/state/` 通过且 `grep -n "= 1[01]" internal/state/migrate.go` 显示版本常量如预期

## 2. 语义验证（design D3 热点）

- [x] 2.1 热点 `internal/topology/pool.go`：核对上游 relay 抽离（`topology/relay.go`、`topology/feedback.go`）后，本地 `topology/platform_latency.go` 的挂接点仍被 `evaluateNode` 调用链引用（`grep -n platform_latency\|LatencyTestURL internal/topology/*.go`），并确认 `internal/topology/` 测试通过：`go test ./internal/topology/...`
- [x] 2.2 热点 `internal/probe/manager.go`：核对本地 `internal/probe/platform_probe.go` 与上游链路探测（`internal/probe/chain_test.go` 覆盖）共存，`go test ./internal/probe/...` 通过
- [x] 2.3 全量验证：`go build ./...`、`go vet ./...`、`go test ./...` 全绿（含上游新增 chain/relay/feedback/migrate 测试）；webui `npm ci && npm run build` 通过
- [x] 2.4 迁移健全性：本地起一次性检查——用临时目录空库跑 `go test ./internal/state/ -run Migrate`（覆盖 state 至 000011、cache 至 000003），确认迁移链完整、down 迁移可回退

## 3. 提交与推送

- [ ] 3.1 以 merge commit 提交（消息注明 v1.4.0+v1.4.1 合并与迁移重编号决策），`git log --oneline -3` 可见 merge 节点；`openspec/changes/merge-upstream-v1-4-1-chain-proxy/` 四个 artifact 一并入库
- [ ] 3.2 推送 origin master，`git status` 干净、`git rev-parse origin/master` 等于本地 HEAD

## 4. bero 部署验证（design Migration Plan）

- [x] 4.1 部署前备份：bero 上备份 `resin_state` / `resin_cache` volumes（或确认外部备份计划覆盖今日快照），记录备份文件路径
- [x] 4.2 `docker compose build && docker compose up -d`，`docker logs` 观察启动迁移日志，确认 state `10 → 11`、cache `2 → 3` 顺延应用、无 dirty 报错；python3 只读复查 `schema_migrations` 值为 `(11, 0)` / `(3, 0)`，`subscriptions` 含 `chain_proxy`/`chain_relay` 列且默认全 0
- [x] 4.3 功能冒烟：代理端口连通性正常（平台探测 URL 行为不回退），WebUI 订阅页出现链式代理控件且默认关闭、节点池显示 relay 绑定列、请求日志无 relay 字段报错；WebUI 平台页探测 URL 表单仍可保存

## Workflow follow-up

- 全部任务完成后归档 change（`openspec archive`），归档时在总结中记录最终迁移编号约定（本地已应用编号不动、上游撞号顺延重编号），供将来同步参考。
- 更新 Hindsight：以 "Correction/后续" 文档记录合并落地结果与生产库最终迁移版本，替代 2026-11-21 检查文档中的"合并前"状态。
