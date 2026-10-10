//go:build server

package serverapp

import (
	"context"
	"errors"

	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	businesssystemaction "github.com/runforyou-ai/luway/internal/actions/businesssystem"
	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/actions/messagepartition"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/actions/ratelimit"
	serverlogaction "github.com/runforyou-ai/luway/internal/actions/serverlog"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	servicehandoffaction "github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/actions/usernotification"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/control"
	"github.com/runforyou-ai/luway/internal/integration/documentconvert"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

// serverTaskDeps 定义后台任务处理器共用的存储、模型运行时与外部服务。
type serverTaskDeps struct {
	db    *bun.DB
	tasks *servertask.Runtime
	// publicURL、fileS3、fileBackend 返回部署当前的部署地址、对象存储配置与新文件的存储类型。
	publicURL     func() string
	localFiles    *serverfilecontent.LocalStore
	fileS3        serverfilecontent.S3Settings
	fileBackend   func() domain.FileStorageBackend
	fileReader    *serverfilecontent.Reader
	emailSender   customernotify.Sender
	agentRuntime  *agentruntime.EinoRuntime
	modelInvoker  *modelcall.Invoker
	agentSchedule *agentrunaction.Scheduler
	agentRun      *agentrunaction.ExecuteAction
	// channelAdapters 提供渠道外发、入站媒体取回与头像读取能力。
	channelAdapters *channeladapter.Registry
	onlineLicense   *licenseaction.OnlineLicenseAction
	// certificates 检查并续期部署地址的证书。
	certificates *certificateaction.Certificates
	// controlClient 以服务器身份调用 control，离线推送经它发送。
	controlClient *control.Client
	// refreshWechatPlatformToken 在收到微信验证票据后刷新平台接口调用凭据。
	refreshWechatPlatformToken *wechataction.RefreshPlatformTokenAction
	// applyWechatAuthorization 处理公众号授权成功与授权更新通知。
	applyWechatAuthorization *wechataction.ApplyAuthorizationEventAction
	// replyWechatReleaseTest 经客服消息接口应答全网发布检测。
	replyWechatReleaseTest *wechataction.ReplyReleaseTestAction
}

// registerServerTasks 注册服务端全部后台任务处理器与定时计划。
func registerServerTasks(deps serverTaskDeps) error {
	registry, db := deps.tasks.Registry(), deps.db

	// 知识库文档处理、问答索引与业务系统工具目录更新在最终失败时写入失败状态。
	processDocument := knowledgeaction.NewProcessDocumentAction(db, documentconvert.NewConverter(), deps.modelInvoker, deps.fileReader, webfetch.NewClient(common.WebFetchUserAgent()))
	processQAEntry := knowledgeaction.NewProcessQAEntryAction(db, deps.modelInvoker)
	updateBusinessTools := businesssystemaction.NewUpdateToolsAction(db, businesssystemaction.NewDefaultConnector())
	if err := errors.Join(
		registry.RegisterJSONWithTerminalFailure(knowledgeaction.ProcessDocumentActionName, processDocument.Execute, processDocument.FinalizeFailure),
		registry.RegisterJSONWithTerminalFailure(knowledgeaction.ProcessQAEntryActionName, processQAEntry.Execute, processQAEntry.FinalizeFailure),
		registry.RegisterJSONWithTerminalFailure(businesssystemaction.RefreshToolsActionName, updateBusinessTools.Execute, updateBusinessTools.FinalizeFailure),
	); err != nil {
		return err
	}
	// 知识库专属向量索引每 10 分钟按知识库规模与向量维度对账一次。
	reconcileVectorIndexes := knowledgeaction.NewReconcileVectorIndexesAction(deps.db)
	if err := registry.RegisterJSON(knowledgeaction.ReconcileVectorIndexesActionName, reconcileVectorIndexes.Execute); err != nil {
		return err
	}
	vectorIndexes := servertask.MaintenanceSchedule(knowledgeaction.VectorIndexScheduleKey, knowledgeaction.ReconcileVectorIndexesActionName, "@every 10m")
	vectorIndexes.Payload = knowledgeaction.ReconcileVectorIndexesInput{}
	deps.tasks.RegisterSchedule(vectorIndexes)

	// 向转人工后离开的网站访客发送客服回复通知，每 30 秒扫描一次到达检查时间的客户会话；部署未配置邮件发送时扫描不投递。
	customerNotify := customernotify.NewWorker(db, deps.tasks, deps.emailSender, deps.publicURL)
	if err := errors.Join(
		registry.RegisterJSON(customernotify.ScanActionName, customerNotify.Scan),
		registry.RegisterJSONWithTerminalFailure(customernotify.NotifyActionName, customerNotify.Execute, customerNotify.FinalizeFailure),
	); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(customernotify.ScheduleKey, customernotify.ScanActionName, "@every 30s"))

	// 成员邀请邮件在发起或重新生成邀请的事务内投递，发信失败时按任务重试。
	if err := registry.RegisterJSON(invitationaction.SendEmailActionName, invitationaction.NewSendEmailAction(db, deps.emailSender, deps.publicURL).Execute); err != nil {
		return err
	}

	// Agent 运行执行、会话标题、AI 员工记忆、已批准工具调用的执行与过期、渠道会话的确认提醒、退回转人工与评测回放；电脑巡检每 15 秒结算派发到已撤销或离线电脑且未结束的调用。
	agentEvaluation := agentevaluationaction.NewWorker(db, deps.agentRun, deps.modelInvoker)
	agentChatTitle := agentrunaction.NewGenerateAgentChatTitleAction(db, deps.modelInvoker)
	agentMemory := agentrunaction.NewExtractAgentMemoryAction(db, deps.tasks, deps.agentRuntime, deps.modelInvoker)
	// 工具调用的处理与电脑巡检结算后的唤醒共用一份工具决定处理，运行所属会话的锁定与事件唤醒由 Agent 运行提供。
	toolDecisions := tooldecisionaction.New(db, deps.tasks, agentrunaction.NewRunScopes(deps.tasks))
	returnedHandoff := servicehandoffaction.NewReturnedHandoffAction(db, deps.tasks, deps.emailSender)
	if err := errors.Join(
		registry.RegisterJSONWithTerminalFailure(agentrunaction.RunActionName, deps.agentRun.Execute, deps.agentRun.FinalizeFailure),
		registry.RegisterJSON(agentrunaction.AgentChatTitleActionName, agentChatTitle.Execute),
		registry.RegisterJSON(agentrunaction.AgentMemoryActionName, agentMemory.Execute),
		registry.RegisterJSON(tooldecisionaction.ToolCallExecuteActionName, toolDecisions.ExecuteApproved),
		registry.RegisterJSON(tooldecisionaction.ToolCallExpireActionName, toolDecisions.Expire),
		registry.RegisterJSON(agentprocess.ToolCallResolveActionName, toolDecisions.Resolve),
		registry.RegisterJSON(tooldecisionaction.ToolDecisionReminderActionName, tooldecisionaction.NewReminderAction(db, deps.tasks, deps.publicURL).Execute),
		registry.RegisterJSONWithTerminalFailure(servicehandoffaction.ReturnedHandoffActionName, returnedHandoff.HandOffReturnedSession, returnedHandoff.FinalizeReturnedHandoffFailure),
		registry.RegisterJSON(computeraction.SweepActionName, computeraction.NewSweepAction(db, toolDecisions).Execute),
		registry.RegisterJSONWithTerminalFailure(agentevaluationaction.EvaluateActionName, agentEvaluation.Evaluate, agentEvaluation.FinalizeFailure),
	); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule("computer-sweep", computeraction.SweepActionName, "@every 15s"))

	// 过期文件每小时扫描一次，逐个删除。
	scanExpired := filemaintenance.NewScanExpiredAction(db, deps.tasks)
	deleteExpired := filemaintenance.NewDeleteExpiredAction(db, serverfilecontent.NewDeleter(deps.localFiles, deps.fileS3))
	if err := errors.Join(
		registry.RegisterJSON(filemaintenance.ScanExpiredActionName, scanExpired.Execute),
		registry.RegisterJSON(filemaintenance.DeleteExpiredActionName, deleteExpired.Execute),
	); err != nil {
		return err
	}
	// 消息分区每天提前创建之后几个月的分区。
	ensurePartitions := messagepartition.NewEnsureAction(db)
	if err := registry.RegisterJSON(messagepartition.EnsureActionName, ensurePartitions.Execute); err != nil {
		return err
	}
	partitions := servertask.MaintenanceSchedule(messagepartition.ScheduleKey, messagepartition.EnsureActionName, "@daily")
	partitions.Payload, partitions.MaxAttempts = messagepartition.EnsureInput{}, 5
	deps.tasks.RegisterSchedule(partitions)

	// 每 10 分钟结束开始超过一小时仍在进行中的模型调用，执行调用结束回调。
	sweepModelCalls := modelcall.NewSweepInterruptedAction(deps.modelInvoker)
	if err := registry.RegisterJSON(modelcall.SweepInterruptedActionName, sweepModelCalls.Execute); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(modelcall.SweepInterruptedScheduleKey, modelcall.SweepInterruptedActionName, "@every 10m"))

	// 运营数据每 10 分钟按平台统计时区重算昨天与今天的账号活跃明细和工作区按日指标。
	aggregateStats := platformaction.NewAggregateStatsAction(db)
	if err := registry.RegisterJSON(platformaction.AggregateStatsActionName, aggregateStats.Execute); err != nil {
		return err
	}
	stats := servertask.MaintenanceSchedule(platformaction.StatsScheduleKey, platformaction.AggregateStatsActionName, "@every 10m")
	stats.Payload = platformaction.AggregateStatsInput{}
	deps.tasks.RegisterSchedule(stats)

	// 服务端日志每小时提前创建日分区并删除超过保留期的分区。
	if err := registry.RegisterJSON(serverlogaction.MaintainServerLogsActionName, func(ctx context.Context, _ struct{}) error {
		return serverlogaction.MaintainServerLogPartitions(ctx, db)
	}); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(serverlogaction.MaintainServerLogsScheduleKey, serverlogaction.MaintainServerLogsActionName, "@hourly"))

	// 公开入口的限速记录每 10 分钟删除额度已全部恢复的部分。
	if err := registry.RegisterJSON(ratelimit.PruneActionName, func(ctx context.Context, _ struct{}) error {
		return ratelimit.Prune(ctx, db)
	}); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(ratelimit.PruneScheduleKey, ratelimit.PruneActionName, "@every 10m"))

	// 服务端每次启动和之后每天向 control 登记并拉取授权，失败时按退避重试。
	if err := registry.RegisterJSON(licenseaction.SyncLicenseActionName, deps.onlineLicense.SyncTask); err != nil {
		return err
	}
	licenseSync := servertask.MaintenanceSchedule(licenseaction.SyncLicenseScheduleKey, licenseaction.SyncLicenseActionName, "@every 24h")
	licenseSync.Payload, licenseSync.MaxAttempts = licenseaction.SyncLicenseInput{}, licenseaction.SyncLicenseEnqueueOptions.MaxAttempts
	licenseSync.StartImmediately = false
	deps.tasks.RegisterSchedule(licenseSync)
	if err := deps.tasks.Enqueue(context.Background(), licenseaction.SyncLicenseActionName, licenseaction.SyncLicenseInput{}, licenseaction.SyncLicenseEnqueueOptions); err != nil {
		return err
	}

	// 部署地址证书每 6 小时检查一次，缺少有效证书或临近到期时自动签发。
	if err := registry.RegisterJSON(certificateaction.RenewCertificateActionName, deps.certificates.Renew); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(certificateaction.RenewCertificateScheduleKey, certificateaction.RenewCertificateActionName, "@every 6h"))

	// 运行指标每分钟由部署中的一台服务器采集并向 control 上报一次。
	reportMetrics := licenseaction.NewReportMetricsAction(db, deps.controlClient)
	if err := registry.RegisterJSON(licenseaction.ReportMetricsActionName, reportMetrics.Execute); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(licenseaction.ReportMetricsScheduleKey, licenseaction.ReportMetricsActionName, "@every 1m"))

	// 微信平台接口调用凭据每 5 分钟用已保存的验证票据按剩余有效期续期，首次收到验证票据时另行投递获取。
	if err := registry.RegisterJSON(wechataction.RefreshPlatformTokenActionName, deps.refreshWechatPlatformToken.Execute); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(wechataction.RefreshPlatformTokenScheduleKey, wechataction.RefreshPlatformTokenActionName, "@every 5m"))
	// 公众号授权成功与授权更新通知换取授权信息，无法连接微信时按任务重试。
	if err := registry.RegisterJSON(wechataction.ApplyAuthorizationEventActionName, deps.applyWechatAuthorization.Execute); err != nil {
		return err
	}
	// 全网发布检测的客服消息检测在回调应答后经任务换取授权并回复。
	if err := registry.RegisterJSON(wechataction.ReplyReleaseTestActionName, deps.replyWechatReleaseTest.Execute); err != nil {
		return err
	}
	cleanup := servertask.MaintenanceSchedule(filemaintenance.CleanupScheduleKey, filemaintenance.ScanExpiredActionName, "@hourly")
	cleanup.Payload, cleanup.MaxAttempts = filemaintenance.ScanExpiredInput{}, 5
	deps.tasks.RegisterSchedule(cleanup)

	// 客服处理周期的自动分配与补分配，小结、质检、交接摘要、联系人资料抽取与待补知识起草，以及每 30 秒扫描一次的超时处理；AI 超时跟进经 Agent 调度器追加输入。
	serviceAssignment := serviceassignment.NewWorker(db, deps.tasks)
	serviceSummary := servicesummary.NewWorker(db, deps.tasks, deps.modelInvoker)
	serviceTimeout := servicetimeout.NewWorker(db, deps.tasks, deps.agentSchedule)
	if err := errors.Join(
		registry.RegisterJSON(serviceassignment.AssignActionName, serviceAssignment.Assign),
		registry.RegisterJSON(serviceassignment.BackfillActionName, serviceAssignment.Backfill),
		registry.RegisterJSONWithTerminalFailure(servicesummary.SummarizeActionName, serviceSummary.Summarize, serviceSummary.FinalizeSummarizeFailure),
		registry.RegisterJSON(servicesummary.ReviewActionName, serviceSummary.Review),
		registry.RegisterJSON(servicesummary.HandoffSummaryActionName, serviceSummary.HandoffSummary),
		registry.RegisterJSON(servicesummary.ExtractContactProfileActionName, serviceSummary.ExtractContactProfile),
		registry.RegisterJSONWithTerminalFailure(knowledgegap.DraftActionName, serviceSummary.DraftKnowledgeGap, serviceSummary.FinalizeKnowledgeGapDraftFailure),
		registry.RegisterJSON(servicetimeout.ScanActionName, serviceTimeout.Scan),
		registry.RegisterJSON(servicetimeout.ProcessActionName, serviceTimeout.Process),
	); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(servertask.MaintenanceSchedule(servicetimeout.ScheduleKey, servicetimeout.ScanActionName, "@every 30s"))

	// 新消息在已读确认窗口后、客服处理周期提醒立即为接收成员生成用户通知，并经 control 推送到接收账号已登录的移动设备。
	userNotifications := usernotification.NewDeliverer(db, deps.tasks)
	if err := errors.Join(
		registry.RegisterJSON(notificationtask.MessageActionName, userNotifications.DeliverMessage),
		registry.RegisterJSON(notificationtask.ServiceAttentionActionName, userNotifications.DeliverServiceAttention),
		registry.RegisterJSON(notificationtask.ToolDecisionActionName, userNotifications.DeliverToolDecision),
		registry.RegisterJSON(notificationtask.PushActionName, usernotification.NewPusher(db, deps.controlClient).Push),
	); err != nil {
		return err
	}

	// 渠道消息按渠道身份管道推进投递；渠道入站事件按平台事件编号排重后处理，渠道提示经任务发送；渠道身份头像与入站媒体按部署级存储配置导入，媒体取回最终失败时写入附件终态。
	deliveryWorker := deliveryaction.NewWorker(db, deps.channelAdapters, deps.fileReader, deps.tasks)
	fileWriter := serverfilecontent.NewWriter(deps.localFiles, deps.fileS3)
	retrieveChannelMedia := channelinboundaction.NewRetrieveMediaAction(db, deps.channelAdapters, fileWriter, deps.agentSchedule)
	receiveChannelEvent := channelinboundaction.NewReceiveEventAction(db, deps.channelAdapters, deps.agentSchedule, deps.fileBackend, deps.tasks, retrieveChannelMedia, deps.publicURL)
	refreshChannelAvatar := channelinboundaction.NewRefreshAvatarAction(db, deps.channelAdapters, fileaction.NewImportAction(db, deps.fileBackend, fileWriter))
	sendChannelNotice := channelinboundaction.NewSendNoticeAction(db, deps.channelAdapters)
	if err := errors.Join(
		registry.RegisterJSON(deliveryaction.AdvanceActionName, deliveryWorker.Advance),
		registry.RegisterJSON(channelinboundaction.ReceiveEventActionName, receiveChannelEvent.Execute),
		registry.RegisterJSONWithTerminalFailure(channelinboundaction.RetrieveMediaActionName, retrieveChannelMedia.Execute, retrieveChannelMedia.FinalizeFailure),
		registry.RegisterJSON(channelinboundaction.RefreshAvatarActionName, refreshChannelAvatar.Execute),
		registry.RegisterJSON(channelinboundaction.SendNoticeActionName, sendChannelNotice.Execute),
	); err != nil {
		return err
	}
	return nil
}
