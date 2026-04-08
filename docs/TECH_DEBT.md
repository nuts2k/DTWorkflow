# 技术债记录

## TD-001: serve.go 文件过大

- **位置**: `internal/cmd/serve.go`（~837 行）
- **问题**: 装配层（`BuildServiceDeps`）、通知构造（`buildNotifier`、`configDrivenNotifier`）、配置适配器（`configAdapter`）全部集中在同一个文件中，职责过多
- **建议拆分方向**:
  - `internal/cmd/serve_notify.go` — `configDrivenNotifier` + `buildNotifyRules` + `buildNotifier`
  - `internal/cmd/serve_adapter.go` — `configAdapter` + `giteaCommentAdapter`
  - `internal/cmd/serve_deps.go` — `BuildServiceDeps` + `buildServeConfigFromManager` + 辅助构造函数
  - `internal/cmd/serve.go` — 仅保留 Cobra 命令定义、`runServe`、`runServeWithConfig`、优雅关闭
- **优先级**: 低（不影响功能，仅影响可维护性）
- **记录时间**: 2026-04-08
- **状态**: 已完成（2026-04-08）
- **实际拆分结果**:
  - `serve.go` 837 → 386 行（Cobra 命令 + 服务生命周期）
  - `serve_notify.go` 190 行（通知构造）
  - `serve_deps.go` 242 行（依赖装配）
  - `adapter.go` 86 行（`giteaCommentAdapter` + `configAdapter`）
