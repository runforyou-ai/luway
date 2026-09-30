package appservice

// RealtimeFrameEventName 是原生端投递一条服务端实时事件的 Wails 事件名，事件数据为 RealtimeFrameEvent。
const RealtimeFrameEventName = "app:realtime:frame"

// RealtimeClosedEventName 是原生端实时事件流结束的 Wails 事件名，事件数据为 RealtimeClosedEvent。
const RealtimeClosedEventName = "app:realtime:closed"

// RealtimeRunFrameEventName 是原生端投递一条运行过程事件的 Wails 事件名，事件数据为 RealtimeFrameEvent。
const RealtimeRunFrameEventName = "app:realtime:run:frame"

// RealtimeRunClosedEventName 是原生端运行过程流结束的 Wails 事件名，事件数据为 RealtimeClosedEvent。
const RealtimeRunClosedEventName = "app:realtime:run:closed"

// RealtimeWorkspacesFrameEventName 是原生端投递一条工作区动态事件的 Wails 事件名，事件数据为 RealtimeFrameEvent。
const RealtimeWorkspacesFrameEventName = "app:realtime:workspaces:frame"

// RealtimeWorkspacesClosedEventName 是原生端工作区动态事件流结束的 Wails 事件名，事件数据为 RealtimeClosedEvent。
const RealtimeWorkspacesClosedEventName = "app:realtime:workspaces:closed"

// RealtimeConnection 是原生端本地事件流编号，事件据此区分新旧事件流。
type RealtimeConnection struct {
	ConnectionID string `json:"connectionId"`
}

// RealtimeFrameEvent 携带原生端收到的一条服务端实时事件 JSON 文本。
type RealtimeFrameEvent struct {
	ConnectionID string `json:"connectionId"`
	Frame        string `json:"frame"`
}

// RealtimeRunFrameEvent 携带原生端收到的一条运行过程事件，运行编号让订阅方在取得本地流编号前区分并发的运行过程流。
type RealtimeRunFrameEvent struct {
	ConnectionID string `json:"connectionId"`
	RunID        string `json:"runId"`
	Frame        string `json:"frame"`
}

// RealtimeRunClosedEvent 表示原生端指定运行过程流已结束。
type RealtimeRunClosedEvent struct {
	ConnectionID string `json:"connectionId"`
	RunID        string `json:"runId"`
}

// RealtimeClosedEvent 表示原生端实时事件流已结束。
type RealtimeClosedEvent struct {
	ConnectionID string `json:"connectionId"`
}
