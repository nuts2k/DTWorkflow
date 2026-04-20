# 技术债记录

> 记录项目中已识别的技术债务，按状态分区管理。

## 待处理

### TD-003: 飞书限流时"开始"通知静默丢失

- **位置**: `internal/queue/processor.go` — `sendStartNotification`
- **问题**: `sendStartNotification` 在飞书返回限流错误（code=11232 `frequency limited`）时仅记录日志，不重试、不降级。生产复现：2026-04-19 PR #114（wangzhiyong/sunrate）"评审开始"飞书卡片丢失，"评审完成"卡片正常送达。
- **根因**: 飞书 Webhook bot 存在调用频率上限，短时间多 PR 并发评审时首通知即触碰限流；`sendCompletionNotification` 在任务执行完（约 4 分钟后）发出时限流窗口已过，因此完成通知成功。
- **风险**: 用户无感知评审已启动，在高并发场景（多 PR 同时推送）下概率更高
- **建议修复方向**:
  - 方案 A：为 `sendStartNotification` 加指数退避重试（1s/2s/4s，最多 3 次）
  - 方案 B：飞书限流时降级为 Gitea PR 评论（"⏳ 评审已开始，飞书通知受限流影响未送达"）
- **记录时间**: 2026-04-20

---

### TD-002: 三个 Issue 相关通知事件已定义但未使用

- **位置**: `internal/notify/notifier.go`
- **问题**: 以下事件类型已定义常量但无生产代码触发：
  - `EventIssueAnalysisDone` (`issue.analysis.done`)
  - `EventIssueNeedInfo` (`issue.need_info`)
  - `EventFixPRCreated` (`fix.pr.created`)
- **风险**: 如后续启用这些事件，需同步更新远程服务器 `dtworkflow.yaml` 的飞书路由规则，否则飞书不会收到通知（与 issue #95 同类问题）
- **建议**: 启用新事件时，同步更新 `configs/` 下的配置模板和部署文档
- **记录时间**: 2026-04-14

---

## 已完成

### TD-001: serve.go 文件过大

- **位置**: `internal/cmd/serve.go`（原 ~837 行）
- **问题**: 装配层（`BuildServiceDeps`）、通知构造（`buildNotifier`、`configDrivenNotifier`）、配置适配器（`configAdapter`）全部集中在同一个文件中，职责过多
- **解决方案**: 按职责拆分为 4 个文件
  - `serve.go` 386 行 — Cobra 命令定义 + 服务生命周期
  - `serve_notify.go` 190 行 — 通知构造（`configDrivenNotifier` + `buildNotifyRules` + `buildNotifier`）
  - `serve_deps.go` 242 行 — 依赖装配（`BuildServiceDeps` + `buildServeConfigFromManager` + 辅助函数）
  - `adapter.go` 86 行 — 适配器（`giteaCommentAdapter` + `configAdapter`）
- **记录时间**: 2026-04-08
- **完成时间**: 2026-04-08
