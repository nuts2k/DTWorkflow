package notify

import "errors"

var (
	// ErrNotifierNotFound 通知渠道未注册
	ErrNotifierNotFound = errors.New("通知渠道未注册")
	// ErrInvalidTarget 无效的通知目标
	ErrInvalidTarget = errors.New("无效的通知目标")
	// ErrSendFailed 通知发送失败
	ErrSendFailed = errors.New("通知发送失败")
	// ErrNoChannelMatched 无匹配的通知渠道
	ErrNoChannelMatched = errors.New("无匹配的通知渠道")
)
