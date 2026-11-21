# Design

## Context

本地 fork 与上游的分叉点为 `5473c7e`（v1.3.0 release notes）。本地领先 2 个提交：`e5a5142`（平台专属探测 URL，含 state 迁移 `000010_platforms_add_latency_test_url`）与 `be42f3b`（openspec init）。上游领先 19 个提交（v1.4.0 + v1.4.1，核心为订阅级链式代理），并新增 state 迁移 `000010_subscription_chain_proxy` 与 cache 迁移 `000003_nodes_dynamic_chain_proxy`。

bero 生产库实测状态（2026-11-21 只读检查，详见 proposal）：state.db `schema_migrations = (10, 0)` 且 `platforms.latency_test_url` 列存在；cache.db `(2, 0)`。

## Goals / Non-Goals

**Goals:**

- 本地 master 干净地包含上游 v1.4.0 + v1.4.1 全部内容
- 迁移编号冲突以一种使生产库**顺延升级、零手工干预**的方式解决
- 合并后的语义兼容性（本地平台探测 vs 上游链式探测/拓扑重构）得到编译与测试验证

**Non-Goals:**

- 不为链式代理能力建立本地正式 spec（见 proposal）
- 不在本次合并中对上游代码做任何本地行为修改（重编号除外）
- 不处理上游之外的本地遗留问题

## Decisions

### D1：合并方式 —— merge commit（非 rebase）

沿用项目既有惯例（`1a6954e Merge remote master with local fixes`）。保留上游完整历史使后续同步持续低成本；本地仅 2 个提交，rebase 收益可忽略且会重写已推送历史。

### D2：迁移重编号 —— 保留本地 000010，上游重编号为 000011（方案 A）

唯一使生产库顺延升级的选择。若反向（保留上游 000010、本地改 000011）：生产库已在 version 10，迁移器会把新 `000010_subscription_chain_proxy` 视为已应用而跳过（chain 列缺失），同时把 `000011`（latency SQL）重复应用而报 duplicate column。

落点（两处本地编辑）：

1. `internal/state/migrations/state/000010_subscription_chain_proxy.{up,down}.sql` → 重命名为 `000011_...`（文件内容不动）
2. `internal/state/migrate.go`：保留 `stateVersionPlatformLatencyTestURL = 10`，新增 `stateVersionSubscriptionChainProxy = 11`，`stateLatestVersion = stateVersionSubscriptionChainProxy`（该常量仅在 migrate.go 内部引用，无其他落点）

备选：不重编号、直接删本地迁移——被否，破坏本地功能与生产库一致性。

### D3：语义验证 —— 编译 + 全量测试 + 针对性检查热点

`git merge-tree` 试合并显示文本冲突仅 `internal/state/migrate.go`，但两处 auto-merge 文件存在语义风险，合并后必须验证：

- `internal/topology/pool.go`：上游将 relay 选择/反馈抽离至 `topology/relay.go` + `topology/feedback.go`（净删约 170 行）；本地 `topology/platform_latency.go` 的挂接点需确认仍被调用（P2C 第 5 步分叉，见平台专属探测 URL 方案 A 页）
- `internal/probe/manager.go`：上游为链路探测重做约 140 行；本地 `probe/platform_probe.go` 依赖的平台探测路径需确认共存

验证手段：`go build ./...`、`go vet ./...`、`go test ./...`（上游自带大量新测试：chain/relay/feedback/probe 等），webui 构建通过。热点文件人工核对本地调用链仍连通。

### D4：OpenSpec 记录粒度 —— skip_specs，零 spec delta

链式代理行为由上游文档承载（其 DESIGN.md 同步更新了 156 行）；本地唯一的 durable 决策（D2）由本 design 记录。将来若对链式代理做本地定制，再以新 change 补正式 spec。

## Risks / Trade-offs

- [上游测试覆盖不到本地平台探测与链式探测的交互] → 合并后全量测试 + 手工核对 platform_latency/platform_probe 调用链；部署后观察启动日志 `Loaded ... from state.db` 与迁移日志
- [重编号导致将来再次同步上游时迁移文件持续偏移] → 已知代价，上游后续新增迁移若再撞号，按同一策略处理（本地已应用的编号不动，上游的顺延重编号）；proposal 已记录该约定
- [生产库升级失败使迁移进入 dirty 状态] → 升级前备份 volumes（compose 已带 `docker-volume-backup.stop-during-backup` 标签）；部署窗口内 `schema_migrations` 若 dirty，回滚镜像并恢复库文件
- [webui 自动合并处（subscriptions/platforms 页面）双方都改] → 构建通过 + 部署后人工过一遍订阅页链式代理控件与平台页探测 URL 表单

## Migration Plan

1. 合并 + 重编号 + 本地验证（build/test/webui build）
2. 推送 fork（origin master）
3. bero：`docker compose build && docker compose up -d`（重构镜像含新迁移文件）
4. 观察启动日志确认 state `10 → 11`、cache `2 → 3` 顺延应用；核对 `subscriptions` 新列存在、链式代理默认关闭
5. 回滚策略：镜像回退到上一版本即可恢复运行；生产库不回退（新增列默认值无害，旧版本代码不引用）

## Open Questions

（无 —— 关键前置问题"生产库是否已应用本地 000010"已于 2026-11-21 在 bero 上以只读检查证实。）
