//go:build server

package main

import (
	"net/http"

	"github.com/runforyou-ai/luway/docs"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/license"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/ingress"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/productdocs"
	"github.com/runforyou-ai/luway/internal/productsite"
	"github.com/runforyou-ai/luway/internal/publicweb"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	mailintegration "github.com/runforyou-ai/luway/pkg/mail"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// applicationServices 组装服务端入口、业务服务和后台任务，并返回处理实时事件流的资源中间件与根路径下的产品首页。
func applicationServices(appStorage *serverstorage.Store, config serverconfig.Config) ([]application.Service, application.Middleware, http.Handler, error) {
	db := appStorage.DB()
	// 为 HTTPS 入口提供部署地址和证书缓存。
	httpsEntry := ingress.NewHTTPSEntry(config.TLS, config.Server, serverstorage.NewACMECache(db))

	// 初始化本地文件存储和部署级对象存储配置。
	localFiles, err := serverfilecontent.NewLocalStore(config.Storage.LocalDirectory)
	if err != nil {
		return nil, nil, nil, err
	}
	fileS3 := fileContentS3Config(config.Storage.S3)
	fileReader := serverfilecontent.NewReader(localFiles, fileS3)

	// 实时发布器与可靠任务运行时由服务生命周期统一启停。
	realtimePublisher := realtime.NewPublisher(config.NATS)
	tasks := servertask.New(db, config.NATS)

	// 知识库分词词典在启动时加载一次，供分段写入与词法召回共用。
	if err := searchtext.LoadDictionary(); err != nil {
		return nil, nil, nil, err
	}
	agentRuntime, err := agentruntime.New()
	if err != nil {
		return nil, nil, nil, err
	}
	// 部署配置了 SMTP 时启用邮件通知与邀请邮件。
	var emailSender customernotify.Sender
	if smtp := config.Email.SMTP; smtp.Enabled() {
		emailSender = mailintegration.NewClient(mailintegration.Config{
			Host: smtp.Host, Port: smtp.Port, Username: smtp.Username, Password: smtp.Password,
			Security: smtp.Security, FromAddress: smtp.FromAddress,
		})
	}

	// 运行期通过附件读取器读取会话附件，按配置版本绑定的知识库执行混合检索；客服 AI 写回复以单次模型调用同步生成回复候选。
	agentRunScheduler := agentrunaction.NewScheduler(tasks)
	agentAttachments := agentrunaction.NewAttachmentReader(db, fileReader, serverfilecontent.NewLinks(config.Server.PublicURL, fileS3.PublicBaseURL))
	modelInvoker := modelcall.New(db, modelcall.DefaultUpstreams())
	knowledgeRetrieval := knowledgeaction.NewRetrievalService(db, modelInvoker)
	executeAgentRun := agentrunaction.NewExecuteAction(db, tasks, agentRuntime, modelInvoker, agentAttachments, knowledgeRetrieval, emailSender)
	serviceReplySuggestions := agentrunaction.NewGenerateServiceReplySuggestionsAction(db, agentRuntime, modelInvoker, agentAttachments)
	telegramAPI := telegramintegration.NewClient(connectiontest.NewHTTPClient())
	if err := registerServerTasks(serverTaskDeps{
		db: db, maintenanceDB: appStorage.MaintenanceDB(), tasks: tasks, publicURL: config.Server.PublicURL, localFiles: localFiles, fileS3: fileS3, fileReader: fileReader,
		emailSender: emailSender, agentRuntime: agentRuntime, modelInvoker: modelInvoker, agentSchedule: agentRunScheduler, agentRun: executeAgentRun, telegramAPI: telegramAPI,
	}); err != nil {
		return nil, nil, nil, err
	}

	// 产品文档按部署状态过滤页面：当前部署使用实例授权且未配对商业服务。
	productDocs, err := productdocs.Load(docs.Content, productdocs.Conditions{productdocs.ConditionInstanceLicense: true})
	if err != nil {
		return nil, nil, nil, err
	}
	productDocsService, err := productdocs.NewService(productDocs)
	if err != nil {
		return nil, nil, nil, err
	}

	// 组装企业成员与网站匿名访客各自的业务入口。
	deployment := directDeploymentConfig(config, emailSender)
	deployment.ProductDocs = productDocs
	translator := translationaction.NewTranslator(db, agentRuntime, modelInvoker)
	directBackend := direct.New(db, deployment, localFiles, fileS3, agentRunScheduler, executeAgentRun, tasks, serviceReplySuggestions, translator, knowledgeRetrieval)
	boundService := appservice.New(directBackend)
	websiteVisitorBackend := direct.NewWebsiteVisitorBackend(db, agentRunScheduler, tasks, localFiles, fileS3, emailSender, knowledgeRetrieval)
	websiteVisitorService := appservice.NewWebsiteVisitorService(websiteVisitorBackend)
	// 实时网关复用成员业务调用的身份解析与同步探针，以及访客的渠道身份解析。
	realtimeGateway := gateway.New(directBackend, websiteVisitorBackend, config.NATS.Namespace, gateway.DefaultOptions())
	telegramWebhook := customerchataction.NewReceiveTelegramWebhookAction(db, agentRunScheduler, fileS3.Backend(), tasks)

	// 将业务入口适配为 HTTP API，并为公开网站渠道提供配置查询。
	httpAPI := api.NewService(
		boundService,
		api.WithDeviceRuns(directBackend),
		api.WithDeviceModelGateway(directBackend),
		api.WithDeviceRunAttachments(directBackend),
		api.WithWebsiteVisitor(websiteVisitorService, config.TLS.Mode != "off", config.Server.VisitorCountryHeader),
		api.WithWebsiteVisitorRealtime(realtimeGateway),
		api.WithTelegramWebhook(telegramWebhook),
	)
	publicLookup := channelaction.NewGetPublicWebsiteChannelQuery(db).Execute

	// 注册健康检查、业务与文件路由、公开聊天入口及后台服务生命周期。
	services := []application.Service{
		application.NewServiceWithOptions(api.NewLiveness(), application.ServiceOptions{Route: "/healthz"}),
		application.NewServiceWithOptions(api.NewReadiness(db), application.ServiceOptions{Route: "/readyz"}),
		application.NewService(&realtimeLifecycle{publisher: realtimePublisher, gateway: realtimeGateway}),
		application.NewService(&httpsLifecycle{service: httpsEntry}),
		application.NewServiceWithOptions(boundService, application.ServiceOptions{MarshalError: appservice.MarshalError}),
		application.NewServiceWithOptions(httpAPI, application.ServiceOptions{Route: "/api"}),
		application.NewServiceWithOptions(api.NewLocalObjectService(direct.NewLocalObjectAuthorizer(db), localFiles), application.ServiceOptions{Route: domain.LocalFilePublicPath + "/"}),
		application.NewService(&serverTaskLifecycle{runtime: tasks}),
		application.NewServiceWithOptions(publicweb.NewEmbedService(publicLookup), application.ServiceOptions{Route: "/embed"}),
		application.NewServiceWithOptions(publicweb.NewChatService(publicLookup), application.ServiceOptions{Route: "/chat/"}),
		application.NewServiceWithOptions(productDocsService, application.ServiceOptions{Route: "/docs"}),
	}
	return services, realtimeGateway.Middleware, productsite.NewService(productdocs.StylesheetPath()), nil
}

// fileContentS3Config 把部署级对象存储配置转换为文件内容层配置。
func fileContentS3Config(config serverconfig.S3Config) serverfilecontent.S3Config {
	return serverfilecontent.S3Config{
		Enabled: config.Enabled, Endpoint: config.Endpoint, PublicBaseURL: config.PublicBaseURL,
		Region: config.Region, Bucket: config.Bucket, AccessKeyID: config.AccessKeyID,
		SecretAccessKey: config.SecretAccessKey, ForcePathStyle: config.ForcePathStyle,
	}
}

// directDeploymentConfig 返回成员业务入口的部署配置。
func directDeploymentConfig(config serverconfig.Config, invitationMailer customernotify.Sender) direct.DeploymentConfig {
	return direct.DeploymentConfig{
		Name: config.Deployment.Name, PublicURL: config.Server.PublicURL, InvitationMailer: invitationMailer,
		LicenseKeys: license.PublicKeys(),
	}
}
