//go:build server

package computer

const (
	// defaultMaxConcurrency 是执行器未给出有效上限时同时执行的操作数。
	defaultMaxConcurrency = 4
	// maxConcurrencyLimit 是同时执行的操作数上限。
	maxConcurrencyLimit = 32
)
