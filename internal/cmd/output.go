package cmd

import (
	"encoding/json"
	"fmt"
	"os"
)

// PrintResult 统一输出函数：JSON 模式时序列化 data 到 stdout；否则调用 humanFn 格式化输出
func PrintResult(data any, humanFn func(any) string) {
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(data); err != nil {
			fmt.Fprintf(os.Stderr, "JSON 输出失败: %v\n", err)
		}
		return
	}
	fmt.Print(humanFn(data))
}

// PrintError 统一错误输出：JSON 模式时输出结构化错误到 stdout；人类模式时输出到 stderr
// 注意：命令内部不应自行调用 PrintError，统一通过返回 error 让 main.go 处理
func PrintError(err error) {
	code := ExitCode(err)
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{
			"error":    err.Error(),
			"exitCode": code,
		})
		return
	}
	fmt.Fprintln(os.Stderr, err)
}
