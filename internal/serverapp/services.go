//go:build server

package serverapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/docs"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	channelconnectionaction "github.com/runforyou-ai/luway/internal/actions/channelconnection"
	conversationfileaction "github.com/runforyou-ai/luway/internal/actions/conversationfile"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	wecombotaction "github.com/runforyou-ai/luway/internal/actions/wecombot"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/common/license"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/ingress"
	acmeintegration "github.com/runforyou-ai/luway/internal/integration/acme"
	"github.com/runforyou-ai/luway/internal/integration/control"
	"github.com/runforyou-ai/luway/internal/integration/documentconvert"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	wechatintegration "github.com/runforyou-ai/luway/internal/integration/wechat"
	wecomintegration "github.com/runforyou-ai/luway/internal/integration/wecom"
	"github.com/runforyou-ai/luway/internal/productdocs"
	"github.com/runforyou-ai/luway/internal/productsite"
	"github.com/runforyou-ai/luway/internal/publicweb"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/luway/pkg/searchtext"
)

// applicationServices 组装服务端入口、业务服务和后台任务，按启动顺序返回服务端组件；extend 为空时只有核心功能，非空时在任务运行时与邮件发送创建后、任务注册前调用一次；routes 是已登记 Web 应用与客户端下载的路由，在其上登记产品站、业务、文件、健康检查与公开聊天路由；clients 是客户端下载服务，filesDirectory 是本地文件目录，instance 是本进程在部署中的登记，deployment 是本进程的部署状态，quit 使服务端退出。
func applicationServices(ctx context.Context, extend func(Host) (Extension, error), appStorage *serverstorage.Store, config serverconfig.Config, routes *http.ServeMux, clients *clientrelease.Catalog, filesDirectory string, instance *serverInstance, deployment *deploymentaction.DeploymentState, quit func()) ([]component, error) {
	db := appStorage.DB()
	// HTTPS 入口使用部署地址的证书，证书签发质询的应答经数据库在各服务器间共享。
	acmeChallenges := serverstorage.NewACMEChallenges(db)
	certificateIssuer := acmeintegration.NewIssuer(acmeintegration.DirectoryURL, acmeChallenges)

	// 初始化本地文件存储，对象存储配置、部署地址与邮件发送配置取自部署状态。
	localFiles, err := serverfilecontent.NewLocalStore(filesDirectory)
	if err != nil {
		return nil, err
	}
	fileS3 := serverfilecontent.S3Settings(deployment.S3)
	fileBackend := func() domain.FileStorageBackend { return deployment.S3().Backend() }
	fileReader := serverfilecontent.NewReader(localFiles, fileS3)
	emailSender := deployment.Mailer()

	// 消息总线、实时发布器与任务运行时由服务生命周期统一启停；运行流按运行登记的执行实例定位；任务经 NATS JetStream 投递与执行。
	bus := instance.bus
	realtimePublisher := realtime.NewPublisher(bus)
	tasks, err := servertask.New(ctx, instance.jobs.JS, servertask.Options{Namespace: config.NATS.Namespace, Replicas: config.NATS.Replicas}, db, appStorage.LeaseDB(), instance.report.ID)
	if err != nil {
		return nil, err
	}
	if err := instance.jobs.RegisterRevocations(tasks, db); err != nil {
		return nil, err
	}
	// 程序组成在任务运行时与邮件发送创建后、任务注册前创建挂接内容，产品站按挂接的首页价格区块登记。
	var extension Extension
	if extend != nil {
		if extension, err = extend(Host{DB: db, Config: config, Tasks: tasks, Mail: emailSender, PublicURL: deployment.PublicURL}); err != nil {
			return nil, fmt.Errorf("extend server: %w", err)
		}
	}
	routes.Handle("/", productsite.NewService(productdocs.StylesheetPath(), clients, productHome(deployment, extension.HomePricing)))

	// 知识库分词词典在启动时加载一次，供分段写入与词法召回共用。
	if err := searchtext.LoadDictionary(); err != nil {
		return nil, err
	}
	agentRuntime, err := agentruntime.New()
	if err != nil {
		return nil, err
	}
	// 运行期通过附件读取器读取会话附件，按配置版本绑定的知识库执行混合检索；客服 AI 写回复以单次模型调用同步生成回复候选。
	agentRunScheduler := agentrunaction.NewScheduler(tasks)
	agentAttachments := agentrunaction.NewAttachmentReader(db, fileReader, serverfilecontent.NewLinks(deployment.PublicURL, fileS3))
	modelInvoker := modelcall.New(db, modelcall.DefaultUpstreams(), extension.ModelCallLifecycle)
	knowledgeRetrieval := knowledgeaction.NewRetrievalService(db, modelInvoker)
	// AI 员工的文件工具读写运行所属会话的共享文件区，PDF、Word 等文档转换为只读文本。
	executeAgentRun := agentrunaction.NewExecuteAction(db, tasks, agentRuntime, modelInvoker, agentAttachments, knowledgeRetrieval, emailSender,
		conversationfileaction.NewStore(db, fileBackend, serverfilecontent.NewWriter(localFiles, fileS3), fileReader), documentconvert.NewConverter())
	serviceReplySuggestions := agentrunaction.NewGenerateServiceReplySuggestionsAction(db, agentRuntime, modelInvoker, agentAttachments)
	telegramAPI := telegramintegration.NewClient(connectiontest.NewHTTPClient())
	// 微信客户端供第三方平台凭据获取与公众号接口调用共用，素材下载的超时由取回任务控制。
	wechatClient := wechatintegration.NewClient(&http.Client{Timeout: 10 * time.Second}, wechatintegration.WithDownloadClient(&http.Client{}))
	// 渠道适配器按渠道类型登记外部平台的外发、媒体、头像与长连接能力；长连接渠道的外发任务按渠道路由由持有连接的实例执行。
	channelAdapters := channeladapter.NewRegistry()
	channelConnections := channelconnectionaction.New(db, channelAdapters, tasks)
	channelAdapters.Register(domain.ChannelTypeTelegram, telegramaction.NewAdapter(db, telegramAPI, telegramAPI, telegramAPI))
	channelAdapters.Register(domain.ChannelTypeWeComBot, wecombotaction.NewAdapter(db, wecomintegration.NewDialer(), connectiontest.NewHTTPClient(), channelConnections))
	channelAdapters.Register(domain.ChannelTypeWechatKey, wechataction.NewAdapter(db, wechatClient, domain.ChannelTypeWechatKey))
	channelAdapters.Register(domain.ChannelTypeWechatAuthorization, wechataction.NewAdapter(db, wechatClient, domain.ChannelTypeWechatAuthorization))
	// control 客户端用服务器的身份签名请求，在线授权与运行指标、错误上报共用。
	controlClient := control.New(control.BaseURL, buildinfo.Version, func(ctx context.Context) (control.Identity, error) {
		return licenseaction.ControlIdentity(ctx, db)
	})
	onlineLicense := licenseaction.NewOnlineLicenseAction(db, license.PublicKeys(), controlClient, deployment)
	if err := registerServerTasks(serverTaskDeps{
		db: db, tasks: tasks, publicURL: deployment.PublicURL, localFiles: localFiles, fileS3: fileS3, fileBackend: fileBackend, fileReader: fileReader,
		emailSender: emailSender, agentRuntime: agentRuntime, modelInvoker: modelInvoker, agentSchedule: agentRunScheduler, agentRun: executeAgentRun, channelAdapters: channelAdapters,
		onlineLicense: onlineLicense, controlClient: controlClient, certificates: certificateaction.NewCertificates(db, certificateIssuer),
		refreshWechatPlatformToken: wechataction.NewRefreshPlatformTokenAction(db, wechatClient),
		applyWechatAuthorization:   wechataction.NewApplyAuthorizationEventAction(db, wechatClient),
		replyWechatReleaseTest:     wechataction.NewReplyReleaseTestAction(db, wechatClient),
	}); err != nil {
		return nil, err
	}
	// 程序组成挂接的后台任务与定时计划在核心任务之后注册。
	if err := tasks.Registry().Add(extension.Tasks...); err != nil {
		return nil, err
	}
	for _, schedule := range extension.Schedules {
		tasks.RegisterSchedule(schedule)
	}

	// 业务入口部署配置中由挂接内容决定的部分与集成测试共用，HTTP 与应用内帮助共用其中加载的产品文档。
	directDeployment, err := ExtensionDeployment(extension)
	if err != nil {
		return nil, err
	}
	productDocsService, err := productdocs.NewService(directDeployment.ProductDocs)
	if err != nil {
		return nil, err
	}

	// 组装企业成员与网站匿名访客各自的业务入口。
	realtimePrefix := config.NATS.RealtimePrefix()
	directDeployment.RunSnapshots, directDeployment.RealtimePrefix = realtimePublisher, realtimePrefix
	directDeployment.Deployment, directDeployment.LicenseKeys = deployment, license.PublicKeys()
	directDeployment.Control, directDeployment.Wechat, directDeployment.CertificateIssuer = controlClient, wechatClient, certificateIssuer
	directDeployment.InstanceID, directDeployment.TaskMonitor = instance.report.ID, tasks
	translator := translationaction.NewTranslator(db, modelInvoker)
	directBackend := direct.New(db, directDeployment, localFiles, agentRunScheduler, agentrunaction.NewRunCancellation(db, tasks), tasks, serviceReplySuggestions, translator, knowledgeRetrieval)
	websiteVisitorBackend := direct.NewWebsiteVisitorBackend(db, agentRunScheduler, tasks, localFiles, fileS3, emailSender, knowledgeRetrieval)
	// 实时服务复用业务身份解析并统一使用部署实时命名空间。
	websiteVisitorBackend.SetRealtimePrefix(realtimePrefix)
	memberRealtime, err := members.New(instance.jobs, directBackend, db, realtimePrefix, config.NATS.Replicas)
	if err != nil {
		return nil, err
	}
	realtimePublisher.SetMembers(memberRealtime)
	realtimePublisher.SetRunTransport(instance.jobs.Conn, realtimePrefix, memberRealtime.PublishRun)
	proxy, err := instance.jobs.Proxy()
	if err != nil {
		return nil, err
	}
	routes.Handle("/nats", proxy)
	telegramWebhook := telegramaction.NewReceiveUpdateAction(db, tasks)

	// 将业务入口适配为 HTTP API，并为公开网站渠道提供配置查询。
	apiOptions := []api.ServiceOption{
		api.WithComputers(directBackend),
		api.WithWebsiteVisitor(websiteVisitorBackend, func() bool { return strings.HasPrefix(deployment.PublicURL(), "https://") }),
		api.WithTelegramWebhook(telegramWebhook),
		api.WithChannelIdentityAssertion(customerchataction.NewAssertChannelIdentityAction(db, tasks)),
		api.WithWechatPlatformEvents(wechataction.NewReceivePlatformEventAction(db, tasks)),
		api.WithWechatMessages(wechataction.NewReceiveMessageAction(db, tasks)),
		api.WithWechatAuthorization(wechataction.NewAuthorizationPageQuery(db, deployment.PublicURL), wechataction.NewCompleteAuthorizationAction(db, wechatClient)),
	}
	httpAPI := api.NewService(directBackend, append(apiOptions, ExtensionAPIOptions(extension)...)...)
	publicLookup := channelaction.NewGetPublicWebsiteChannelQuery(db).Execute

	// 登记健康检查、业务与文件路由及公开聊天入口，各处理器接收去掉路由前缀后的路径。
	routes.Handle("/healthz", api.NewLiveness())
	routes.Handle("/readyz", api.NewReadiness(db, bus.Connected))
	routes.Handle("/api/", http.StripPrefix("/api", httpAPI))
	routes.Handle(domain.LocalFilePublicPath+"/", http.StripPrefix(domain.LocalFilePublicPath+"/", api.NewLocalObjectService(direct.NewLocalObjectAuthorizer(db), localFiles)))
	routes.Handle("/embed/", http.StripPrefix("/embed", publicweb.NewEmbedService(publicLookup)))
	routes.Handle("/chat/", http.StripPrefix("/chat/", publicweb.NewChatService(publicLookup)))
	routes.Handle("/docs", http.StripPrefix("/docs", productDocsService))
	routes.Handle("/docs/", http.StripPrefix("/docs", productDocsService))

	// 先确定串联编号并解析请求来源 IP 与国家代码，再开放接口跨域访问、拦截接口版本过旧的原生端请求并按路由处理；配置了来源 IP 请求头时取该请求头作为请求来源 IP。
	handler := api.TraceMiddleware(api.ClientOriginMiddleware(config.Server.ClientIPHeader, config.Server.CountryHeader)(api.CORSMiddleware(api.ClientVersionMiddleware(routes))))

	// 错误记录与上报最先启动、最后停止，覆盖其他组件启停时的错误；HTTP 入口在实时通知之后启动，任务运行时、渠道长连接与进程心跳随后启动。
	return []component{
		&telemetryLifecycle{instanceID: instance.report.ID, deployment: deployment, control: controlClient},
		&realtimeLifecycle{bus: bus, publisher: realtimePublisher, members: memberRealtime},
		ingress.New(config.Server, deployment, acmeChallenges, handler),
		&serverTaskLifecycle{runtime: tasks},
		&channelConnectionLifecycle{runtime: channelConnections},
		&serverInstanceLifecycle{instance: instance, deployment: deployment, interval: serverinstanceaction.InstanceHeartbeatInterval, quit: quit},
	}, nil
}

// ExtensionDeployment 返回业务入口部署配置中由挂接内容决定的部分：工作区初始化器、基础来源与挂接来源合成的产品文档、席位上限与首页价格区块开关；服务端与集成测试在此基础上补齐其余依赖。
func ExtensionDeployment(extension Extension) (direct.DeploymentConfig, error) {
	productDocs, err := productdocs.Load(docs.Content, extension.ProductDocs)
	if err != nil {
		return direct.DeploymentConfig{}, err
	}
	return direct.DeploymentConfig{
		WorkspaceInitializer: extension.WorkspaceInitializer, ProductDocs: productDocs,
		Seats: seataction.New(extension.SeatLimit), HomePricing: extension.HomePricing != nil,
	}, nil
}

// ExtensionAPIOptions 返回挂接内容对 HTTP 接口的选项：其他模块路由注册在核心路由之后。
func ExtensionAPIOptions(extension Extension) []api.ServiceOption {
	if extension.APIRoutes == nil {
		return nil
	}
	return []api.ServiceOption{api.WithRoutes(extension.APIRoutes)}
}
