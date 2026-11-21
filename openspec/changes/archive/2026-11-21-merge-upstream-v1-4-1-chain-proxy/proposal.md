# Proposal

## Why

上游 Resinat/Resin 已发布 v1.4.0 + v1.4.1（订阅级链式代理、relay 稳定绑定与熔断反馈），本地 fork 需要同步以获取新能力并保持后续可合并性。合并存在一个必须在部署前解决的硬冲突：本地与上游各自新增了 state 迁移编号 000010，而 bero 生产库（state.db `schema_migrations = (10, 0)`）已应用本地的 `000010_platforms_add_latency_test_url`，编号重排方案直接决定生产库能否无手工干预地顺延升级。

## What Changes

- 合并上游 master（`5473c7e..8e9f74e`，19 个提交，对应 v1.4.0 + v1.4.1）：
  - 订阅级链式代理：`subscriptions.chain_proxy` / `chain_relay` 互斥角色列（默认关闭），cache 库 `nodes_dynamic` / `node_latency` 增加 `chain_proxy` 标记
  - relay 稳定绑定与熔断反馈：出口节点保持稳定 relay 绑定，熔断失败后重选
  - request log 记录 relay 来源与路径；WebUI 订阅页链式代理控件、节点池 relay 绑定展示等
- **迁移重编号**（本变更唯一的本地代码决策）：上游 `000010_subscription_chain_proxy.{up,down}.sql` 重命名为 `000011_...`，`internal/state/migrate.go` 相应调整为 `stateVersionPlatformLatencyTestURL = 10`（保留）、`stateVersionSubscriptionChainProxy = 11`、`stateLatestVersion = stateVersionSubscriptionChainProxy`
- 合并方式为 merge commit（沿用项目历史惯例，保持上游历史以便持续同步）
- 不为链式代理能力建立本地正式 spec：行为由上游定义、本地零改动，将来做本地定制时再补
- 文本冲突仅 `internal/state/migrate.go` 一处；两处热点 auto-merge 文件需合并后语义验证

## Capabilities

### New Capabilities

（无 —— 本次为纯上游同步，链式代理行为记录归上游（其 release notes 与 DESIGN.md），本地无新增可声明的行为决策。`.openspec.yaml` 声明 `skip_specs: true`。）

### Modified Capabilities

（无 —— 本地既有功能（平台专属探测 URL 等）需求不变。）

## Impact

- **代码**：上游 diff 共 113 文件 +6459/-783。本地决策仅限迁移重编号两处（migrations 目录 + `internal/state/migrate.go`）
- **语义验证热点**（auto-merge 成功但需 build/test 验证）：
  - `internal/topology/pool.go`：上游将 relay 选择/反馈逻辑抽离为新文件，本地平台延迟探测逻辑挂接于同一文件
  - `internal/probe/manager.go`：上游为链式代理重做探测（约 140 行改动），本地平台探测亦改动同一文件
- **依赖**：go.mod / go.sum 随上游更新（含 Go toolchain 与 sing-box 等）
- **部署（bero）**：合并后需重建 docker 镜像并重启；生产库升级路径为 state `10 → 11`、cache `2 → 3`，全部顺延、零手工干预（依据 2026-11-21 生产库只读检查）
- **回滚**：迁移重编号只影响新文件名，不改动已应用迁移内容；如需回滚合并，`git revert` merge commit 即可，生产库不回退（新列保留无害）
