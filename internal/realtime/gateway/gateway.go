//go:build server

// Package gateway 在企业服务端内提供成员与网站访客的实时 SSE 事件流，按已认证身份订阅受众通知并转发为实时事件。
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
)

// Path 是成员实时事件流路径。
const Path = "/api/realtime"

// WorkspacesPath 是工作区动态事件流路径：只凭账号登录会话建立，下发本人在各工作区中的变化。
const WorkspacesPath = "/api/realtime/workspaces"

// DevicePath 是本机设备事件流路径：凭登录会话与设备编号建立，只下发该设备的工作水位。
const DevicePath = "/api/realtime/device"

// RunPath 是运行过程流路径前缀，其后是运行编号。
const RunPath = "/api/realtime/runs/"

// flushTimeout 是等待 NATS 确认订阅生效的上限。
const flushTimeout = 5 * time.Second

// VisitorBackend 解析网站访客的渠道身份。
type VisitorBackend interface {
	// AuthenticateVisitor 校验启用的网站渠道与访客身份并返回事件流受众，渠道停用或尚未建立身份时返回访客业务错误。
	AuthenticateVisitor(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string) (direct.WebsiteVisitorAudience, error)
	// VerifyCustomer 按渠道所属企业当前的客户身份密钥校验签名身份，失效时返回访客业务错误。
	VerifyCustomer(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, token string) (appservice.WebsiteVisitorCustomer, error)
}

// MemberBackend 解析成员登录令牌并读取同步探针值。
type MemberBackend interface {
	// AuthenticateMember 校验请求携带的登录令牌并返回成员会话，令牌无效或账号不可用时返回登录会话错误。
	AuthenticateMember(ctx context.Context, meta appservice.RequestMeta) (direct.MemberSession, error)
	// AuthenticateAccountMembers 校验账号登录令牌并返回账号会话及其全部有效成员身份，令牌无效或账号不可用时返回登录会话错误。
	AuthenticateAccountMembers(ctx context.Context, meta appservice.RequestMeta) (direct.AccountMembersSession, error)
	// AuthenticateDevice 校验登录令牌与请求携带的本人未撤销设备并返回成员会话。
	AuthenticateDevice(ctx context.Context, meta appservice.RequestMeta) (direct.MemberSession, error)
	// MemberSyncHeads 返回指定成员会话的同步探针值。
	MemberSyncHeads(ctx context.Context, session direct.MemberSession) (appservice.SyncHeads, error)
	// AuthorizeAgentRunStream 校验指定成员会话对运行所属会话的阅读资格，并返回运行所属会话编号。
	AuthorizeAgentRunStream(ctx context.Context, meta appservice.RequestMeta, session direct.MemberSession, runID string) (string, error)
	// SubscribeAgentRunStream 订阅本进程中该运行当前执行尝试的过程流，返回订阅时的快照与取消订阅函数；
	// 回调在运行流锁内串行执行，不得阻塞。运行不在本进程执行时返回 false，调用方按持久事实收敛。
	SubscribeAgentRunStream(runID string, onDelta func(runstream.Delta), onEnd func()) (runstream.Snapshot, func(), bool)
}

// Options 定义事件流心跳、时限与发送队列。
type Options struct {
	PingInterval    time.Duration
	MaxLifetime     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	QueueSize       int
	// RunSnapshotPartBytes 是运行过程流快照单个分片的文本预算。
	RunSnapshotPartBytes int
	// RunPendingTextBytes 是运行过程流待发增量合并后的文本上限，超过即按慢消费者结束该流；
	// 单个事件连同编码开销必须小于原生端读取单行事件的上限。
	RunPendingTextBytes int
}

// DefaultOptions 返回首版事件流参数：每 25 秒发送心跳、最长存活 1 小时。
func DefaultOptions() Options {
	return Options{
		PingInterval:         25 * time.Second,
		MaxLifetime:          time.Hour,
		WriteTimeout:         10 * time.Second,
		ShutdownTimeout:      5 * time.Second,
		QueueSize:            256,
		RunSnapshotPartBytes: 32 * 1024,
		RunPendingTextBytes:  256 * 1024,
	}
}

// memberFrameTypes 是成员事件流可下发的变更通知事件。
var memberFrameTypes = []protocol.Type{
	protocol.TypeServerHello, protocol.TypeConversationChanged, protocol.TypeConversationRemoved,
	protocol.TypeConversationStateChanged, protocol.TypeConversationTyping, protocol.TypeIdentityProfileChanged,
	protocol.TypePinOrderChanged, protocol.TypeServiceAttention, protocol.TypeKnowledgeGapsChanged, protocol.TypeServiceReportsChanged,
	protocol.TypeAssistantMemoryChanged,
}

// workspaceActivityKinds 是工作区动态事件流转发的成员变更通知，均影响本人的提醒数量或新消息提示。
var workspaceActivityKinds = map[protocol.Type]bool{
	protocol.TypeConversationChanged: true, protocol.TypeConversationRemoved: true, protocol.TypeConversationStateChanged: true,
	protocol.TypeServiceAttention: true, protocol.TypeIdentityProfileChanged: true,
}

// deviceFrameTypes 是设备事件流可下发的事件。
var deviceFrameTypes = []protocol.Type{protocol.TypeServerHello, protocol.TypeDeviceWorkAdvanced}

// visitorFrameTypes 是网站访客事件流可下发的公开事件。
var visitorFrameTypes = []protocol.Type{protocol.TypeVisitorHello, protocol.TypeConversationChanged, protocol.TypeVisitorTyping, protocol.TypeReceptionChanged}

// streamRoute 是一条已授权事件流的受众、撤销标识、可下发事件与授权到期时间。
type streamRoute struct {
	subjects       []string
	allowed        []protocol.Type
	tokenSessionID string
	// deviceID 是事件流携带的已认证设备编号，只有该设备的工作水位通知会下发。
	deviceID string
	// workspaces 按受众 Subject 记录所属工作区，非空表示工作区动态事件流：变更通知按工作区转为工作区动态事件。
	workspaces map[string]string
	// expiresAt 是事件流授权的绝对到期时间，零值表示只受最长存活时间约束。
	expiresAt  time.Time
	attributes []any
	// greet 在受众订阅生效后复核授权并返回首个事件。
	greet func(ctx context.Context, connectionID string) (protocol.Frame, error)
}

// Gateway 管理本节点的实时事件流与受众订阅。
type Gateway struct {
	backend   MemberBackend
	visitor   VisitorBackend
	namespace string
	options   Options

	mu           sync.Mutex
	nats         *nats.Conn
	closing      bool
	streams      map[audienceStream]struct{}
	audiences    map[string]*audience
	running      sync.WaitGroup
	shutdownOnce sync.Once
}

// audienceStream 是加入受众订阅的事件流；登出与停用的撤销控制据此结束事件流。
type audienceStream interface {
	// tokenSession 返回事件流所属登录会话编号。
	tokenSession() string
	// audienceSubjects 返回事件流加入的受众 Subject。
	audienceSubjects() []string
	// revoke 因撤销控制清除未发送的事件并结束事件流。
	revoke(kind realtime.Kind)
	// shutdown 在网关下线时停止接收新事件，由写协程发送剩余事件后结束事件流。
	shutdown()
	// abort 取消请求处理，让阻塞中的写入立即超时。
	abort()
}

// audience 是一个受众 Subject 的 NATS 订阅及本节点订阅该受众的事件流。
type audience struct {
	subscription *nats.Subscription
	streams      map[audienceStream]struct{}
}

// New 创建使用指定 NATS 命名空间的实时网关，visitor 为 nil 时不提供访客事件流。
func New(backend MemberBackend, visitor VisitorBackend, namespace string, options Options) *Gateway {
	return &Gateway{
		backend:   backend,
		visitor:   visitor,
		namespace: namespace,
		options:   options,
		streams:   map[audienceStream]struct{}{},
		audiences: map[string]*audience{},
	}
}

// Start 使用指定 NATS 连接开始接收事件流请求。
func (g *Gateway) Start(connection *nats.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nats = connection
	slog.Info("实时网关已启动", "namespace", g.namespace, "path", Path)
}

// Middleware 在 Wails 资源服务之前处理成员、设备、工作区动态事件流与运行过程流请求，其余请求交给下一个处理器。
func (g *Gateway) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			if request.URL.Path == Path {
				meta := appservice.RequestMetaFromHTTP(request.Header)
				g.stream(writer, request, meta, func(ctx context.Context) (streamRoute, error) {
					return g.memberRoute(ctx, meta)
				})
				return
			}
			if request.URL.Path == DevicePath {
				meta := appservice.RequestMetaFromHTTP(request.Header)
				g.stream(writer, request, meta, func(ctx context.Context) (streamRoute, error) {
					return g.deviceRoute(ctx, meta)
				})
				return
			}
			if request.URL.Path == WorkspacesPath {
				// 工作区动态事件流只凭账号会话建立，忽略请求携带的目标工作区。
				meta := appservice.RequestMetaFromHTTP(request.Header)
				meta.WorkspaceID, meta.DeviceID = "", ""
				g.stream(writer, request, meta, func(ctx context.Context) (streamRoute, error) {
					return g.workspacesRoute(ctx, meta)
				})
				return
			}
			if runID, ok := strings.CutPrefix(request.URL.Path, RunPath); ok && runID != "" && !strings.Contains(runID, "/") {
				g.serveRun(writer, request, runID)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

// ServeVisitor 处理已通过访客授权的网站访客事件流请求，访客元信息、channelID 与 externalID 由公开路由的访客授权得到。
func (g *Gateway) ServeVisitor(writer http.ResponseWriter, request *http.Request, meta appservice.WebsiteVisitorMeta, channelID, externalID string) {
	g.stream(writer, request, appservice.RequestMeta{Locale: appservice.Locale(meta.Locale)}, func(ctx context.Context) (streamRoute, error) {
		return g.visitorRoute(ctx, meta, channelID, externalID)
	})
}

// Shutdown 停止接收新请求，结束现有事件流并在时限内等待其退出；重复调用等待首次调用完成。
func (g *Gateway) Shutdown() {
	g.shutdownOnce.Do(func() {
		g.mu.Lock()
		g.closing = true
		streams := make([]audienceStream, 0, len(g.streams))
		for current := range g.streams {
			streams = append(streams, current)
		}
		g.mu.Unlock()

		for _, current := range streams {
			current.shutdown()
		}
		done := make(chan struct{})
		go func() {
			g.running.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(g.options.ShutdownTimeout):
			slog.Warn("实时事件流未在时限内结束，强制断开", "count", len(streams))
			for _, current := range streams {
				current.abort()
			}
			<-done
		}
		slog.Info("实时网关已停止", "namespace", g.namespace, "closed", len(streams))
	})
}

// memberRoute 认证成员登录令牌，返回本人用户受众与本企业客服共享受众。
func (g *Gateway) memberRoute(ctx context.Context, meta appservice.RequestMeta) (streamRoute, error) {
	identity, err := g.backend.AuthenticateMember(ctx, meta)
	if err != nil {
		return streamRoute{}, err
	}
	organizationID := identity.OrganizationID
	return streamRoute{
		subjects: []string{
			realtime.Subject(g.namespace, organizationID, realtime.AudienceUser, identity.UserID),
			// 当前阶段所有成员均可阅读客户会话，成员连接都接收客服共享受众通知。
			realtime.Subject(g.namespace, organizationID, realtime.AudienceCustomerInbox, organizationID),
		},
		allowed:        memberFrameTypes,
		tokenSessionID: identity.SessionID,
		// 事件流最长存活时间不晚于登录会话到期。
		expiresAt:  identity.ExpiresAt,
		attributes: []any{"organization_id", organizationID, "user_id", identity.UserID},
		greet: func(ctx context.Context, connectionID string) (protocol.Frame, error) {
			// 订阅生效后再次校验登录会话，之后提交的登出或停用经受众通知送达。
			if _, err := g.backend.AuthenticateMember(ctx, meta); err != nil {
				return nil, err
			}
			heads, err := g.backend.MemberSyncHeads(ctx, identity)
			if err != nil {
				return nil, err
			}
			return protocol.ServerHello{ConnectionID: connectionID, SyncHeads: heads}, nil
		},
	}, nil
}

// deviceRoute 认证登录令牌与请求携带的本人设备，只订阅本人用户受众，下发该设备的工作水位与登录会话撤销。
func (g *Gateway) deviceRoute(ctx context.Context, meta appservice.RequestMeta) (streamRoute, error) {
	identity, err := g.backend.AuthenticateDevice(ctx, meta)
	if err != nil {
		return streamRoute{}, err
	}
	return streamRoute{
		subjects:       []string{realtime.Subject(g.namespace, identity.OrganizationID, realtime.AudienceUser, identity.UserID)},
		allowed:        deviceFrameTypes,
		tokenSessionID: identity.SessionID,
		deviceID:       meta.DeviceID,
		expiresAt:      identity.ExpiresAt,
		attributes:     []any{"organization_id", identity.OrganizationID, "user_id", identity.UserID, "device_id", meta.DeviceID},
		greet: func(ctx context.Context, connectionID string) (protocol.Frame, error) {
			// 订阅生效后再次校验登录会话与设备，设备重连后按 ServerHello 比较一次工作水位。
			if _, err := g.backend.AuthenticateDevice(ctx, meta); err != nil {
				return nil, err
			}
			return protocol.ServerHello{ConnectionID: connectionID}, nil
		},
	}, nil
}

// workspacesRoute 认证账号登录会话，返回账号在各工作区的本人用户受众与客服共享受众；账号之后加入的工作区在重新建立事件流后订阅。
func (g *Gateway) workspacesRoute(ctx context.Context, meta appservice.RequestMeta) (streamRoute, error) {
	session, err := g.backend.AuthenticateAccountMembers(ctx, meta)
	if err != nil {
		return streamRoute{}, err
	}
	subjects := make([]string, 0, len(session.Members)*2)
	workspaces := make(map[string]string, len(session.Members)*2)
	for _, member := range session.Members {
		for _, subject := range []string{
			realtime.Subject(g.namespace, member.OrganizationID, realtime.AudienceUser, member.UserID),
			realtime.Subject(g.namespace, member.OrganizationID, realtime.AudienceCustomerInbox, member.OrganizationID),
		} {
			subjects = append(subjects, subject)
			workspaces[subject] = member.OrganizationID
		}
	}
	return streamRoute{
		subjects:       subjects,
		allowed:        []protocol.Type{protocol.TypeServerHello, protocol.TypeWorkspaceActivity},
		tokenSessionID: session.SessionID,
		workspaces:     workspaces,
		expiresAt:      session.ExpiresAt,
		attributes:     []any{"account_id", session.AccountID, "workspaces", len(session.Members)},
		greet: func(ctx context.Context, connectionID string) (protocol.Frame, error) {
			// 订阅生效后再次校验登录会话与成员身份：之后提交的登出、成员停用经受众通知送达；
			// 期间成员身份已变化（停用通知可能早于订阅生效）时拒绝本次连接，由客户端按新的成员身份重连。
			current, err := g.backend.AuthenticateAccountMembers(ctx, meta)
			if err != nil {
				return nil, err
			}
			if !slices.Equal(current.Members, session.Members) {
				slog.Info("工作区动态事件流建立期间成员身份变化，拒绝本次连接", "account_id", session.AccountID)
				return nil, appservice.UnavailableError(meta, i18n.ErrorServerUnavailable, nil).WithStatus(http.StatusServiceUnavailable)
			}
			return protocol.ServerHello{ConnectionID: connectionID}, nil
		},
	}, nil
}

// visitorRoute 解析访客渠道身份，返回其访客目录受众与所在渠道的撤销受众。
func (g *Gateway) visitorRoute(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string) (streamRoute, error) {
	if g.visitor == nil {
		return streamRoute{}, appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindUnavailable, i18n.MessengerRequestFailed, nil).WithStatus(http.StatusServiceUnavailable)
	}
	target, err := g.visitor.AuthenticateVisitor(ctx, meta, channelID, externalID)
	if err != nil {
		return streamRoute{}, err
	}
	subjects := []string{
		realtime.Subject(g.namespace, target.OrganizationID, realtime.AudienceVisitorDirectory, target.ChannelIdentityID),
		// 渠道停用的撤销控制按渠道发送，该渠道全部访客事件流据此结束。
		realtime.Subject(g.namespace, target.OrganizationID, realtime.AudienceWebsiteChannel, target.ChannelID),
		// 接待状态变化按企业发送，企业全部访客事件流据此重新读取接待状态。
		realtime.Subject(g.namespace, target.OrganizationID, realtime.AudienceWebsiteVisitors, target.OrganizationID),
	}
	// 登录用户的事件流在签名身份过期时结束，并随客户身份密钥重新生成撤销。
	var expiresAt time.Time
	if meta.Customer != nil {
		subjects = append(subjects, realtime.Subject(g.namespace, target.OrganizationID, realtime.AudienceCustomerIdentity, target.OrganizationID))
		expiresAt = meta.Customer.ExpiresAt
	}
	return streamRoute{
		subjects:   subjects,
		allowed:    visitorFrameTypes,
		expiresAt:  expiresAt,
		attributes: []any{"organization_id", target.OrganizationID, "channel_id", target.ChannelID, "channel_identity_id", target.ChannelIdentityID},
		greet: func(ctx context.Context, connectionID string) (protocol.Frame, error) {
			// 订阅生效后再次校验渠道与访客身份，登录用户按当前密钥重新验签；之后提交的渠道停用与密钥重新生成经受众通知送达。
			if _, err := g.visitor.AuthenticateVisitor(ctx, meta, channelID, externalID); err != nil {
				return nil, err
			}
			if meta.Customer != nil {
				if _, err := g.visitor.VerifyCustomer(ctx, meta, channelID, meta.CustomerToken); err != nil {
					return nil, err
				}
			}
			return protocol.VisitorHello{ConnectionID: connectionID}, nil
		},
	}, nil
}

// stream 按 authorize 得到的受众安装订阅并复核授权后输出事件流，直到事件流结束。
func (g *Gateway) stream(writer http.ResponseWriter, request *http.Request, meta appservice.RequestMeta, authorize func(context.Context) (streamRoute, error)) {
	route, err := authorize(request.Context())
	if err != nil {
		writeError(writer, request, meta, err)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	current := newConnection(g, cancel, route)
	attributes := append([]any{"connection_id", current.id}, route.attributes...)
	if !g.register(current) {
		writeUnavailable(writer, request, meta)
		return
	}
	defer g.unregister(current)

	if err := g.joinAudiences(ctx, current); err != nil {
		slog.Warn("实时受众订阅失败", append(attributes, "error", err)...)
		writeUnavailable(writer, request, meta)
		return
	}
	hello, err := route.greet(ctx, current.id)
	if err != nil {
		writeError(writer, request, meta, err)
		return
	}

	controller, opened := openEventStream(writer, request, meta, current.attach, "connection_id", current.id)
	if !opened {
		return
	}
	current.send(hello)

	// 存活时长在计时器启动时结算，握手耗时计入授权到期时间之内。
	lifetime := g.options.MaxLifetime
	if !route.expiresAt.IsZero() {
		lifetime = min(lifetime, time.Until(route.expiresAt))
	}
	expiry := time.AfterFunc(lifetime, func() {
		slog.Info("实时事件流到达最长存活时间", "connection_id", current.id)
		current.close(true)
	})
	defer expiry.Stop()
	slog.Info("实时事件流已就绪", append(attributes, "lifetime", lifetime)...)
	current.run(ctx, writer, controller)
	slog.Info("实时事件流已结束", attributes...)
}

// openEventStream 清除服务器读超时、登记响应控制器并写出事件流响应头；事件流是长响应，写超时按每次写入设置，网关已开始下线或设置失败时输出服务暂不可用并返回 false。
func openEventStream(writer http.ResponseWriter, request *http.Request, meta appservice.RequestMeta, attach func(*http.ResponseController) bool, attributes ...any) (*http.ResponseController, bool) {
	controller := http.NewResponseController(writer)
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		slog.Warn("清除事件流读超时失败", append(attributes, "error", err)...)
		writeUnavailable(writer, request, meta)
		return nil, false
	}
	if !attach(controller) {
		writeUnavailable(writer, request, meta)
		return nil, false
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	return controller, true
}

// writeEventFrame 在写截止时间内以单条 SSE data 行写出事件并立即下发，编码失败时跳过该事件，写出失败时返回 false 结束事件流。
func writeEventFrame(writer http.ResponseWriter, controller *http.ResponseController, timeout time.Duration, frame protocol.Frame, attributes ...any) bool {
	data, err := protocol.Encode(frame)
	if err != nil {
		slog.Warn("编码事件流事件失败", append(attributes, "type", frame.FrameType(), "error", err)...)
		return true
	}
	err = controller.SetWriteDeadline(time.Now().Add(timeout))
	if err == nil {
		_, err = writer.Write(append(append([]byte("data: "), data...), '\n', '\n'))
	}
	if err == nil {
		err = controller.Flush()
	}
	if err != nil {
		slog.Warn("事件流写入失败，结束事件流", append(attributes, "type", frame.FrameType(), "error", err)...)
		return false
	}
	return true
}

// writeUnavailable 以业务错误体输出服务暂不可用。
func writeUnavailable(writer http.ResponseWriter, request *http.Request, meta appservice.RequestMeta) {
	writeError(writer, request, meta, appservice.UnavailableError(meta, i18n.ErrorServerUnavailable, nil).WithStatus(http.StatusServiceUnavailable))
}

// writeError 按业务 HTTP 接口的错误体输出业务错误，其余错误输出服务暂不可用。
func writeError(writer http.ResponseWriter, request *http.Request, meta appservice.RequestMeta, err error) {
	var applicationError *appservice.Error
	if !errors.As(err, &applicationError) {
		slog.Warn("实时事件流请求处理失败", "error", err)
		writeUnavailable(writer, request, meta)
		return
	}
	if applicationError.State != "" {
		slog.Warn("实时事件流认证失败", "state", applicationError.State)
	}
	appservice.WriteHTTPError(writer, request, applicationError)
}

// register 登记事件流，网关正在下线或 NATS 尚未就绪时返回 false。
func (g *Gateway) register(stream audienceStream) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing || g.nats == nil {
		return false
	}
	g.streams[stream] = struct{}{}
	g.running.Add(1)
	return true
}

// joinAudiences 让事件流加入其受众，受众的首个事件流建立 NATS 订阅，并在 NATS 确认订阅生效后返回。
func (g *Gateway) joinAudiences(ctx context.Context, stream audienceStream) error {
	g.mu.Lock()
	for _, subject := range stream.audienceSubjects() {
		target := g.audiences[subject]
		if target == nil {
			subscription, err := g.nats.Subscribe(subject, func(message *nats.Msg) {
				g.deliver(subject, message.Data)
			})
			if err != nil {
				g.mu.Unlock()
				return err
			}
			target = &audience{subscription: subscription, streams: map[audienceStream]struct{}{}}
			g.audiences[subject] = target
		}
		target.streams[stream] = struct{}{}
	}
	connection := g.nats
	g.mu.Unlock()

	// NATS 暂不可达时照常继续，期间丢失的通知由客户端兜底探针恢复。
	flushCtx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	if err := connection.FlushWithContext(flushCtx); err != nil {
		slog.Warn("实时订阅确认失败", "error", err)
	}
	return nil
}

// unregister 移除事件流及其受众登记，受众不再有事件流时取消 NATS 订阅。
func (g *Gateway) unregister(stream audienceStream) {
	g.mu.Lock()
	delete(g.streams, stream)
	for _, subject := range stream.audienceSubjects() {
		target := g.audiences[subject]
		if target == nil {
			continue
		}
		delete(target.streams, stream)
		if len(target.streams) == 0 {
			delete(g.audiences, subject)
			if err := target.subscription.Unsubscribe(); err != nil {
				slog.Warn("取消实时受众订阅失败", "subject", subject, "error", err)
			}
		}
	}
	g.mu.Unlock()
	g.running.Done()
}

// deliver 把受众通知转换为实时事件发给该受众的全部本节点事件流，撤销控制结束对应事件流。
func (g *Gateway) deliver(subject string, data []byte) {
	var payload realtime.Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		slog.Warn("解析实时通知失败", "subject", subject, "error", err)
		return
	}
	g.mu.Lock()
	var targets []audienceStream
	if target := g.audiences[subject]; target != nil {
		targets = make([]audienceStream, 0, len(target.streams))
		for current := range target.streams {
			targets = append(targets, current)
		}
	}
	g.mu.Unlock()

	var frame protocol.Frame
	switch payload.Kind {
	case realtime.KindConversationChanged:
		frame = protocol.ConversationChanged{ConversationID: payload.ConversationID, ConversationType: payload.ConversationType, Version: payload.Version, Changes: payload.Changes}
	case realtime.KindConversationRemoved:
		frame = protocol.ConversationRemoved{ConversationID: payload.ConversationID}
	case realtime.KindConversationStateChanged:
		frame = protocol.ConversationStateChanged{ConversationID: payload.ConversationID, Version: payload.Version}
	case realtime.KindConversationTyping:
		frame = protocol.ConversationTyping{ConversationID: payload.ConversationID, SenderSubjectID: payload.SenderSubjectID, Active: payload.Active}
	case realtime.KindVisitorTyping:
		frame = protocol.VisitorTyping{ConversationID: payload.ConversationID, Active: payload.Active}
	case realtime.KindReceptionChanged:
		frame = protocol.ReceptionChanged{}
	case realtime.KindKnowledgeGapsChanged:
		frame = protocol.KnowledgeGapsChanged{}
	case realtime.KindServiceReportsChanged:
		frame = protocol.ServiceReportsChanged{}
	case realtime.KindIdentityProfileChanged:
		frame = protocol.IdentityProfileChanged{Version: payload.Version}
	case realtime.KindPinOrderChanged:
		frame = protocol.PinOrderChanged{Version: payload.Version}
	case realtime.KindServiceAttention:
		frame = protocol.ServiceAttention{ConversationID: payload.ConversationID, ServiceSessionID: payload.ServiceSessionID, Reason: payload.AttentionReason}
	case realtime.KindDeviceWorkAdvanced:
		frame = protocol.DeviceWorkAdvanced{DeviceID: payload.DeviceID, WorkSeq: payload.Version}
	case realtime.KindAssistantMemoryChanged:
		frame = protocol.AssistantMemoryChanged{AssistantID: payload.AssistantID}
	case realtime.KindSessionLoggedOut:
		for _, current := range targets {
			if current.tokenSession() == payload.TokenSessionID {
				current.revoke(payload.Kind)
			}
		}
		return
	case realtime.KindUserDisabled, realtime.KindChannelDisabled, realtime.KindCustomerIdentityRevoked:
		for _, current := range targets {
			current.revoke(payload.Kind)
		}
		return
	default:
		return
	}
	// 变更通知只发给成员事件流；运行过程流在所属会话失权时结束，其余通知与它无关。
	for _, current := range targets {
		if member, ok := current.(*connection); ok {
			// 设备工作水位只发给携带该设备身份的事件流。
			if payload.Kind == realtime.KindDeviceWorkAdvanced && member.deviceID != payload.DeviceID {
				continue
			}
			// 工作区动态事件流把本人在各工作区的变更通知标上工作区后下发。
			if member.workspaces != nil {
				if activity, ok := workspaceActivity(member.workspaces[subject], frame); ok {
					member.send(activity)
				}
				continue
			}
			member.send(frame)
			continue
		}
		if run, ok := current.(*runStream); ok && payload.Kind == realtime.KindConversationRemoved && run.conversationID == payload.ConversationID {
			run.revoke(payload.Kind)
		}
	}
}

// workspaceActivity 把成员变更通知转为指定工作区的工作区动态事件，与本人提醒数量无关的通知返回 false。
func workspaceActivity(workspaceID string, frame protocol.Frame) (protocol.WorkspaceActivity, bool) {
	if workspaceID == "" || !workspaceActivityKinds[frame.FrameType()] {
		return protocol.WorkspaceActivity{}, false
	}
	activity := protocol.WorkspaceActivity{WorkspaceID: workspaceID, Kind: frame.FrameType()}
	switch value := frame.(type) {
	case protocol.ConversationChanged:
		activity.ConversationID, activity.Changes = value.ConversationID, value.Changes
	case protocol.ConversationRemoved:
		activity.ConversationID = value.ConversationID
	case protocol.ConversationStateChanged:
		activity.ConversationID = value.ConversationID
	case protocol.ServiceAttention:
		activity.ConversationID, activity.ServiceSessionID, activity.Reason = value.ConversationID, value.ServiceSessionID, value.Reason
	}
	return activity, true
}
