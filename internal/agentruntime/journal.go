package agentruntime

import "errors"

// ErrAwaitExternal 由电脑在可挂起的调用超过等待时长仍无结果时返回：调用交给电脑继续推进，运行在本批工具结束后挂起，结果上报后恢复。
var ErrAwaitExternal = errors.New("agent tool call awaits an external result")
