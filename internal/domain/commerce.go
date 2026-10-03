package domain

// CommerceSyncFailure 定义最近一次读取商业服务变更源失败的原因。
type CommerceSyncFailure string

const (
	CommerceSyncFailureUnavailable CommerceSyncFailure = "unavailable"
	CommerceSyncFailureNotPaired   CommerceSyncFailure = "not_paired"
	CommerceSyncFailureInvalidData CommerceSyncFailure = "invalid_data"
	CommerceSyncFailureFailed      CommerceSyncFailure = "failed"
)
