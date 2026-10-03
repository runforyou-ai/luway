//go:build server

package main

import (
	"context"
	"errors"

	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	mcpserveraction "github.com/runforyou-ai/luway/internal/actions/mcpserver"
	"github.com/runforyou-ai/luway/internal/actions/messagepartition"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/documentconvert"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

// serverTaskDeps 定义后台任务处理器共用的存储、模型运行时与外部服务。
type serverTaskDeps struct {
	db            *bun.DB
	maintenanceDB *bun.DB
	tasks         *servertask.Runtime
	publicURL     string
	localFiles    *serverfilecontent.LocalStore
	fileS3        serverfilecontent.S3Config
	fileReader    *serverfilecontent.Reader
	emailSender   customernotify.Sender
	agentRuntime  *agentruntime.EinoRuntime
	modelInvoker  *modelcall.Invoker
	agentSchedule *agentrunaction.Scheduler
	agentRun      *agentrunaction.ExecuteAction
	telegramAPI   *telegramintegration.Client
	onlineLicense *platformaction.OnlineLicenseAction
}

// registerServerTasks 注册服务端全部后台任务处理器与定时计划。
func registerServerTasks(deps serverTaskDeps) error {
	registry, db := deps.tasks.Registry(), deps.db

	// 知识库文档处理、问答索引与 MCP 工具目录更新在最终失败时写入失败状态。
	processDocument := knowledgeaction.NewProcessDocumentAction(db, documentconvert.NewConverter(), deps.modelInvoker, deps.fileReader, webfetch.NewClient(common.WebFetchUserAgent()))
	processQAEntry := knowledgeaction.NewProcessQAEntryAction(db, deps.modelInvoker)
	updateMCPTools := mcpserveraction.NewUpdateToolsAction(db, mcpintegration.NewClient())
	if err := errors.Join(
		registry.RegisterJSONWithTerminalFailure(knowledgeaction.ProcessDocumentActionName, processDocument.Execute, processDocument.FinalizeFailure),
		registry.RegisterJSONWithTerminalFailure(knowledgeaction.ProcessQAEntryActionName, processQAEntry.Execute, processQAEntry.FinalizeFailure),
		registry.RegisterJSONWithTerminalFailure(mcpserveraction.RefreshToolsActionName, updateMCPTools.Execute, updateMCPTools.FinalizeFailure),
	); err != nil {
		return err
	}
	// 知识库专属向量索引每 10 分钟按知识库规模与向量维度对账一次。
	reconcileVectorIndexes := knowledgeaction.NewReconcileVectorIndexesAction(deps.maintenanceDB)
	if err := registry.RegisterJSON(knowledgeaction.ReconcileVectorIndexesActionName, reconcileVectorIndexes.Execute); err != nil {
		return err
	}
	vectorIndexes := maintenanceSchedule(knowledgeaction.VectorIndexScheduleKey, knowledgeaction.ReconcileVectorIndexesActionName, "@every 10m")
	vectorIndexes.Payload = knowledgeaction.ReconcileVectorIndexesInput{}
	deps.tasks.RegisterSchedule(vectorIndexes)

	// 部署配置了 SMTP 时向转人工后离开的网站访客发送客服回复通知，每 30 秒扫描一次到达检查时间的客户会话。
	if deps.emailSender != nil {
		customerNotify := customernotify.NewWorker(db, deps.tasks, deps.emailSender, deps.publicURL)
		if err := errors.Join(
			registry.RegisterJSON(customernotify.ScanActionName, customerNotify.Scan),
			registry.RegisterJSONWithTerminalFailure(customernotify.NotifyActionName, customerNotify.Execute, customerNotify.FinalizeFailure),
		); err != nil {
			return err
		}
		deps.tasks.RegisterSchedule(maintenanceSchedule(customernotify.ScheduleKey, customernotify.ScanActionName, "@every 30s"))
	}

	// Agent 运行执行、会话标题、AI 员工记忆、退回转人工与评测回放；电脑巡检每 15 秒结算派发到已撤销或离线电脑且未结束的调用。
	agentEvaluation := agentevaluationaction.NewWorker(db, deps.agentRun, deps.modelInvoker)
	agentChatTitle := agentrunaction.NewGenerateAgentChatTitleAction(db, deps.agentRuntime, deps.modelInvoker)
	agentMemory := agentrunaction.NewExtractAgentMemoryAction(db, deps.tasks, deps.agentRuntime, deps.modelInvoker)
	if err := errors.Join(
		registry.RegisterJSONWithTerminalFailure(agentrunaction.RunActionName, deps.agentRun.Execute, deps.agentRun.FinalizeFailure),
		registry.RegisterJSON(agentrunaction.AgentChatTitleActionName, agentChatTitle.Execute),
		registry.RegisterJSON(agentrunaction.AgentMemoryActionName, agentMemory.Execute),
		registry.RegisterJSONWithTerminalFailure(agentrunaction.ReturnedHandoffActionName, deps.agentRun.HandOffReturnedSession, deps.agentRun.FinalizeReturnedHandoffFailure),
		registry.RegisterJSON(computeraction.SweepActionName, computeraction.NewSweepAction(db, deps.tasks).Execute),
		registry.RegisterJSONWithTerminalFailure(agentevaluationaction.EvaluateActionName, agentEvaluation.Evaluate, agentEvaluation.FinalizeFailure),
	); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(maintenanceSchedule("computer-sweep", computeraction.SweepActionName, "@every 15s"))

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
	partitions := maintenanceSchedule(messagepartition.ScheduleKey, messagepartition.EnsureActionName, "@daily")
	partitions.Payload, partitions.MaxAttempts = messagepartition.EnsureInput{}, 5
	deps.tasks.RegisterSchedule(partitions)

	// 每 10 分钟结束开始超过一小时仍在进行中的模型调用，结算或退回预占的积分。
	sweepModelCalls := modelcall.NewSweepInterruptedAction(db)
	if err := registry.RegisterJSON(modelcall.SweepInterruptedActionName, sweepModelCalls.Execute); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(maintenanceSchedule(modelcall.SweepInterruptedScheduleKey, modelcall.SweepInterruptedActionName, "@every 10m"))

	// 运营数据每 10 分钟按平台统计时区重算昨天与今天的账号活跃明细和工作区按日指标。
	aggregateStats := platformaction.NewAggregateStatsAction(db)
	if err := registry.RegisterJSON(platformaction.AggregateStatsActionName, aggregateStats.Execute); err != nil {
		return err
	}
	stats := maintenanceSchedule(platformaction.StatsScheduleKey, platformaction.AggregateStatsActionName, "@every 10m")
	stats.Payload = platformaction.AggregateStatsInput{}
	deps.tasks.RegisterSchedule(stats)

	// 服务端每次启动和之后每天向 control 登记并拉取授权，失败时按退避重试。
	if err := registry.RegisterJSON(platformaction.SyncLicenseActionName, deps.onlineLicense.SyncTask); err != nil {
		return err
	}
	licenseSync := maintenanceSchedule(platformaction.SyncLicenseScheduleKey, platformaction.SyncLicenseActionName, "@every 24h")
	licenseSync.Payload, licenseSync.MaxAttempts = platformaction.SyncLicenseInput{}, platformaction.SyncLicenseEnqueueOptions.MaxAttempts
	licenseSync.StartImmediately = false
	deps.tasks.RegisterSchedule(licenseSync)
	if _, err := deps.tasks.Enqueue(context.Background(), platformaction.SyncLicenseActionName, platformaction.SyncLicenseInput{}, platformaction.SyncLicenseEnqueueOptions); err != nil {
		return err
	}

	cleanup := maintenanceSchedule(filemaintenance.CleanupScheduleKey, filemaintenance.ScanExpiredActionName, "@hourly")
	cleanup.Payload, cleanup.MaxAttempts = filemaintenance.ScanExpiredInput{}, 5
	deps.tasks.RegisterSchedule(cleanup)

	// 客服处理周期的自动分配与补分配，小结、质检、交接摘要、联系人资料抽取与待补知识起草，以及每 30 秒扫描一次的超时处理；AI 超时跟进经 Agent 调度器追加输入。
	serviceAssignment := serviceassignment.NewWorker(db)
	serviceSummary := servicesummary.NewWorker(db, deps.tasks, deps.modelInvoker, deps.agentRuntime)
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
	deps.tasks.RegisterSchedule(maintenanceSchedule(servicetimeout.ScheduleKey, servicetimeout.ScanActionName, "@every 30s"))

	// 客户消息每五秒扫描一次待投递消息；Telegram 头像与入站媒体按部署级存储配置导入，媒体取回最终失败时写入附件终态。
	deliveryWorker := deliveryaction.NewWorker(db, deps.telegramAPI, deps.fileReader, deps.tasks)
	fileWriter := serverfilecontent.NewWriter(deps.localFiles, deps.fileS3)
	retrieveTelegramMedia := customerchataction.NewRetrieveTelegramMediaAction(db, deps.telegramAPI, fileWriter, deps.agentSchedule)
	refreshTelegramAvatar := channelaction.NewRefreshTelegramContactAvatarAction(db, deps.telegramAPI, fileaction.NewImportAction(db, deps.fileS3.Backend(), fileWriter))
	if err := errors.Join(
		registry.RegisterJSON(deliveryaction.SendActionName, deliveryWorker.Execute),
		registry.RegisterJSON(deliveryaction.ScanActionName, deliveryWorker.Scan),
		registry.RegisterJSONWithTerminalFailure(customerchataction.RetrieveTelegramMediaActionName, retrieveTelegramMedia.Execute, retrieveTelegramMedia.FinalizeFailure),
		registry.RegisterJSON(channelaction.RefreshTelegramContactAvatarActionName, refreshTelegramAvatar.Execute),
	); err != nil {
		return err
	}
	deps.tasks.RegisterSchedule(maintenanceSchedule("customer-delivery-scan", deliveryaction.ScanActionName, "@every 5s"))
	return nil
}

// maintenanceSchedule 返回维护队列中按 UTC 周期触发、启动时立即执行一次、失败不重试的定时计划。
func maintenanceSchedule(key, actionName, cron string) servertask.ScheduleDefinition {
	return servertask.ScheduleDefinition{
		Key: key, ActionName: actionName, Queue: "maintenance", Payload: struct{}{}, CronExpression: cron,
		Timezone: "UTC", Enabled: true, MaxAttempts: 1, StartImmediately: true,
	}
}
