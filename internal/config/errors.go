package config

import "errors"

// ErrInvalidConfig 配置校验失败的哨兵错误。
//
// 用途：调用方可通过 errors.Is(err, config.ErrInvalidConfig) 判断错误是否属于校验失败，
// 与 I/O 错误（如文件不存在）区分。
var ErrInvalidConfig = errors.New("配置校验失败")
