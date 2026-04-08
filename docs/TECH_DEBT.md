# 技术债记录

> 记录项目中已识别的技术债务，按状态分区管理。

## 待处理

暂无。

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
