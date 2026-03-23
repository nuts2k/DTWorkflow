package notify

// sentinelError 是不可变的 sentinel error 类型，确保外部无法覆盖。
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

const (
	// ErrNotifierNotFound 通知渠道未注册
	ErrNotifierNotFound = sentinelError("通知渠道未注册")
	// ErrInvalidTarget 无效的通知目标
	ErrInvalidTarget = sentinelError("无效的通知目标")
	// ErrInvalidMessage 无效的通知消息（如 EventType 为空）
	ErrInvalidMessage = sentinelError("无效的通知消息")
	// ErrSendFailed 标识通知发送失败。用于调用者通过 errors.Is 区分发送失败与其他类型错误（如参数无效）。
	// 后续如需区分可重试/不可重试错误，建议定义 ErrRetryable / ErrPermanent。
	ErrSendFailed = sentinelError("通知发送失败")
	// ErrNoChannelMatched 无匹配的通知渠道
	ErrNoChannelMatched = sentinelError("无匹配的通知渠道")
)
