# PoC 测试用示例项目

本目录是 DTWorkflow M1.0 PoC 验证阶段使用的示例 Go 项目，**仅用于测试目的**。

## 用途

在验证 Claude Code CLI 的代码操作能力（验证项3）时，将本目录挂载到 Docker 容器中，
让 Claude Code CLI 对这些文件进行读取、修改、测试等操作，验证以下能力：

- 读取现有 Go 源码文件
- 理解项目结构（go.mod、main.go、main_test.go）
- 生成新文件或修改现有文件
- 通过 `go test ./...` 运行测试

## 文件说明

| 文件 | 说明 |
|------|------|
| `go.mod` | Go module 定义，模块名 `example.com/hello` |
| `main.go` | 简单 HTTP 服务器，提供 `/` 和 `/health` 两个端点 |
| `main_test.go` | 对应的单元测试文件 |
| `README.md` | 本说明文件 |

## 本地运行

```bash
# 运行服务器
go run .

# 运行测试
go test ./... -v
```

## 在 Docker 容器中使用

```bash
docker run --rm \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  -v "$(pwd):/workspace" \
  -w /workspace \
  dtworkflow-poc:latest \
  claude -p "请为 main.go 中的 helloHandler 函数添加请求日志" \
    --allowedTools "Read,Write,Bash" \
    --output-format text
```

---

> 注意：本目录中的代码变更可能被 PoC 测试脚本覆盖，请勿在此处存放重要代码。
