//go:build server

// Package direct 在服务端进程内实现应用服务契约，解析登录身份后调用 Action 与 Query。
package direct

import (
	"context"
	"errors"
	"time"

	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	businesssystemaction "github.com/runforyou-ai/luway/internal/actions/businesssystem"
	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	servicehandoffaction "github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/supportplane"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/internal/web/docs"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

var _ appservice.Backend = (*Backend)(nil)

// Backend 解析登录身份并把业务调用分发给已认证实现，分发层由 appservicegen 生成到 backend_gen.go，auth=public 的方法不解析身份。
type Backend struct {
	db          *bun.DB
	ops         *directOperations
	modelSource domain.AIModelSource
}

// directOperations 汇集会话守卫与各业务域实现，生成的分发层经嵌入字段调用各域方法。
type directOperations struct {
	dispatch.Guard
	*authOps
	*conversationOps
	*inboxOps
	*channelOps
	*telegramOps
	*contactOps
	*directoryOps
	*customerServiceOps
	*aiPerformanceOps
	*teamPerformanceOps
	*serviceIssueOps
	*knowledgeGapOps
	*agentOps
	*toolDecisionOps
	*agentEvaluationOps
	*personalAgentOps
	*knowledgeOps
	*integrationOps
	*computerOps
	*fileOps
	*translationOps
	*webSearchOps
	*webFetchOps
	*mediaSettingsOps
	*invitationOps
	*platformOps
	*seatOps
	*wechatOps
	*productDocsOps
}

// DeploymentConfig 定义直接后端的部署状态、产品文档、授权码验签公钥、control 客户端和微信客户端；部署名称、部署地址、对象存储与邮件发送取自部署状态。
// RunSnapshotReader 从实际执行实例读取业务快照。
type RunSnapshotReader interface {
	ReadRunSnapshot(context.Context, string, string, int) (*realtime.RunSnapshot, error)
}

type DeploymentConfig struct {
	// DataDirectory 是本进程日志和消息队列所在的数据目录。
	DataDirectory string
	// WorkspaceInitializer 在首次安装与普通创建工作区的事务内初始化附加数据。
	WorkspaceInitializer workspaceaction.Initializer
	// PlatformInitializer 在首次安装事务内初始化附加数据，为空时不初始化。
	PlatformInitializer installationaction.PlatformInitializer
	RunSnapshots        RunSnapshotReader
	Deployment          *deploymentaction.DeploymentState
	// CertificateIssuer 经 ACME 服务为部署地址签发证书。
	CertificateIssuer certificateaction.CertificateIssuer
	ProductDocs       *docs.Site
	LicenseKeys       license.Keys
	Control           *supportplane.Client
	Wechat            *wechat.Client
	// InstanceID 是本服务端进程编号，写入导出的诊断信息。
	InstanceID string
	// Seats 按程序组成提供的席位上限校验成员启用，零值不限席位。
	Seats seataction.Seats
	// ModelSource 是本部署的模型来源；零值表示仅工作区自配。
	ModelSource domain.AIModelSource
	// TaskMonitor 读取任务队列概况与最终失败的任务。
	TaskMonitor servertask.Monitor
}

// enrichContext 把部署模型来源写入请求 context，供模型解析与自配门禁读取。
func (b *Backend) enrichContext(ctx context.Context) context.Context {
	source := b.modelSource
	if source == "" {
		source = domain.AIModelSourceWorkspace
	}
	return aimodel.WithSource(ctx, source)
}

// New 创建直接访问服务端存储的应用后端。
func New(db *bun.DB, deployment DeploymentConfig, agentScheduler conversationaction.AgentMessageScheduler, runCancellation *agentrunaction.RunCancellation, taskEnqueuer servertask.TxEnqueuer, serviceReplySuggestions *agentrunaction.GenerateServiceReplySuggestionsAction, translator *translationaction.Translator, knowledgeRetrieval *knowledgebaseaction.RetrievalService) *Backend {
	s3 := deployment.Deployment.S3
	connectionRunner := connectiontest.NewRunner(10 * time.Second)
	connectionClient := connectiontest.NewHTTPClient()
	telegramAPI := telegram.NewClient(connectionClient)
	businessSystemTest := businesssystemaction.NewTestConnectionAction(businesssystemaction.NewDefaultConnector())
	toolsScheduler := businesssystemaction.NewToolsScheduler(taskEnqueuer)
	documentQuery := knowledgebaseaction.NewDocumentQuery(db)
	certificates := certificateaction.NewCertificates(db, deployment.CertificateIssuer)
	// 工具调用的确认、审批与电脑上报结果后的唤醒共用一份工具决定处理，运行所属会话的锁定与事件唤醒由 Agent 运行提供。
	toolDecisions := tooldecisionaction.New(db, taskEnqueuer, agentrunaction.NewRunScopes(taskEnqueuer))
	returner := servicehandoffaction.NewReturner(taskEnqueuer, agentrunaction.CancelServiceSessionRuns)
	// 文件地址与服务问题列表由多个业务域共用，先创建再注入引用它们的业务域。
	files := newFileOps(db, s3, serverfilecontent.NewLinks(s3))
	serviceIssues := newServiceIssueOps(db, files)
	ops := &directOperations{
		Guard:           dispatch.NewGuard(db),
		authOps:         newAuthOps(db, deployment.Deployment, certificates, taskEnqueuer, files, deployment.WorkspaceInitializer, deployment.PlatformInitializer, deployment.Seats, deployment.ModelSource),
		conversationOps: newConversationOps(db, agentScheduler, runCancellation, taskEnqueuer, translator, files),
		inboxOps:        newInboxOps(db, taskEnqueuer, files),
		channelOps: newChannelOps(db, taskEnqueuer, map[domain.ChannelType]channelaction.StatusUpdater{
			domain.ChannelTypeTelegram: telegramaction.NewUpdateChannelStatusAction(db, connectionRunner, telegramAPI, taskEnqueuer),
		}, files),
		telegramOps:        newTelegramOps(db, connectionRunner, telegramAPI),
		contactOps:         newContactOps(db, files),
		directoryOps:       newDirectoryOps(db, returner, runCancellation, taskEnqueuer, deployment.Seats, files),
		customerServiceOps: newCustomerServiceOps(db),
		aiPerformanceOps:   newAIPerformanceOps(db, files, serviceIssues),
		teamPerformanceOps: newTeamPerformanceOps(db, files, serviceIssues),
		serviceIssueOps:    serviceIssues,
		knowledgeGapOps:    newKnowledgeGapOps(db, taskEnqueuer),
		agentOps:           newAgentOps(db, taskEnqueuer, runCancellation, returner, serviceReplySuggestions, files),
		toolDecisionOps:    newToolDecisionOps(toolDecisions),
		agentEvaluationOps: newAgentEvaluationOps(db, taskEnqueuer),
		personalAgentOps:   newPersonalAgentOps(db, taskEnqueuer, files),
		knowledgeOps:       newKnowledgeOps(db, taskEnqueuer, documentQuery, knowledgeRetrieval, files),
		integrationOps:     newIntegrationOps(db, taskEnqueuer, connectionRunner, connectionClient, businessSystemTest, toolsScheduler),
		computerOps:        newComputerOps(db, toolDecisions),
		fileOps:            files,
		translationOps:     newTranslationOps(db, translator),
		webSearchOps:       newWebSearchOps(db, connectionRunner),
		webFetchOps:        newWebFetchOps(db, connectionRunner),
		mediaSettingsOps:   newMediaSettingsOps(db),
		invitationOps:      newInvitationOps(db, taskEnqueuer, deployment.Seats, deployment.Deployment.Mailer().Enabled, deployment.Deployment.PublicURL),
		platformOps:        newPlatformOps(db, taskEnqueuer, deployment.TaskMonitor, deployment.LicenseKeys, deployment.Control, deployment.Deployment, certificates, deployment.InstanceID, deployment.DataDirectory),
		seatOps:            newSeatOps(db, deployment.Seats),
		wechatOps:          newWechatOps(db, deployment.Wechat, deployment.Deployment.PublicURL),
		productDocsOps:     &productDocsOps{site: deployment.ProductDocs},
	}
	ops.conversationOps.runSnapshots = deployment.RunSnapshots
	return &Backend{db: db, ops: ops, modelSource: deployment.ModelSource}
}

// AuthorizeRunChannel 复核账号会话、工作区成员与运行所属会话的阅读资格。
func (b *Backend) AuthorizeRunChannel(ctx context.Context, accountID, sessionID, workspaceID, runID string) (bool, error) {
	account, err := authaction.ResolveAccountSession(ctx, b.db, accountID, sessionID)
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	identity, err := authaction.ResolveMember(ctx, b.db, account, workspaceID)
	if errors.Is(err, authaction.ErrMembershipNotFound) || errors.Is(err, authaction.ErrWorkspaceSuspended) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = b.ops.authorizeAgentRunStream.Execute(ctx, identity, runID)
	if errors.Is(err, processquery.ErrRunProcessUnavailable) {
		return false, nil
	}
	return err == nil, err
}
