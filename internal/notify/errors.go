package notify

// sentinelError 是不可变的 sentinel error 类型，确保外部无法覆盖。
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

const (
	// ErrNotifierNotFound 通知渠道未注册
	ErrNotifierNotFound = sentinelError("通知渠道未注册")
	// ErrInvalidTarget 无效的通知目标
	ErrInvalidTarget = sentinelError("无效的通知目标")
	// ErrSendFailed 通知发送失败
	ErrSendFailed = sentinelError("通知发送失败")
	// ErrNoChannelMatched 无匹配的通知渠道
	ErrNoChannelMatched = sentinelError("无匹配的通知渠道")
)
