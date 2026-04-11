# Gitea Webhook 配置指南

本文说明如何把 Gitea Webhook 指向 DTWorkflow 的接收器，并完成最小可用配置。

## 1. 启动 DTWorkflow 服务

启动服务时必须显式提供 Webhook 签名密钥：

```bash
dtworkflow serve --host 0.0.0.0 --port 8080 --webhook-secret "your-webhook-secret"
```

说明：
- `--webhook-secret` 不能为空
- Gitea 侧配置的 Secret 必须与这里完全一致
- Webhook 接收路径为 `/webhooks/gitea`

如果服务部署在 `https://dtworkflow.example.com`，则完整 Webhook URL 为：

```text
https://dtworkflow.example.com/webhooks/gitea
```

## 2. 在 Gitea 仓库中创建 Webhook

进入目标仓库后：

1. 打开“设置”
2. 进入 “Webhooks”
3. 选择 Gitea 支持的通用 Webhook 类型
4. 填写以下内容：
   - **Target URL**：DTWorkflow 的 `/webhooks/gitea` 地址
   - **HTTP Method**：`POST`
   - **Secret**：与 `--webhook-secret` 相同的值
   - **Content Type**：`application/json`

## 3. 推荐订阅事件

当前接收器已支持以下事件：

### Pull Request 事件
- 事件类型：`pull_request`
- 支持动作：
  - `opened`
  - `synchronized`

### Issue 事件
- 事件类型：`issues`
- 支持动作：
  - `labeled`
  - `unlabeled`
- 说明：Issue 事件中的 `ref` 字段由 Gitea 自动提供（对应 Issue 右侧边栏的"Ref"字段），DTWorkflow 会自动解析并校验该 Ref 是否为有效分支或 tag，无需在 Webhook 配置中额外设置

建议至少订阅：
- Pull Request 事件
- Issue 事件

## 4. 当前处理行为

接收器当前行为如下：

- 签名正确、事件受支持、负载合法：返回 `200 OK`
- 事件类型或动作暂不支持：返回 `200 OK`，表示已安全忽略
- 请求体为空、不是 JSON、JSON 非法、请求体超限：返回 `400 Bad Request`
- 缺少签名或签名校验失败：返回 `401 Unauthorized`
- 内部处理器返回错误：返回 `500 Internal Server Error`

## 5. 签名说明

服务使用 HMAC-SHA256 校验 `X-Gitea-Signature`。
当前兼容两种签名格式：

- Gitea 原生十六进制签名
- `sha256=<hex>` 前缀格式

## 6. 联调建议

建议按以下顺序检查：

1. 确认 DTWorkflow 服务已启动，并且 `--webhook-secret` 已配置
2. 确认 Gitea Webhook 的 Secret 与服务启动参数完全一致
3. 确认 Webhook 请求头的 `Content-Type` 为 `application/json`
4. 确认订阅的是当前已支持的事件与动作
5. 若收到 `400`，优先检查请求体格式与大小
6. 若收到 `401`，优先检查 Secret 与签名
