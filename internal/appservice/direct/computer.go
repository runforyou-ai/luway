//go:build server

package direct

import (
	"context"
	"errors"

	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

var _ appservice.ComputerBackend = (*Backend)(nil)

// computerOps 持有电脑的 Action 和 Query。
type computerOps struct {
	db                      *bun.DB
	realtimePrefix          string
	registerComputer        *computeraction.RegisterComputerAction
	listComputers           *computeraction.ListComputersQuery
	revokeComputer          *computeraction.RevokeComputerAction
	createWorkspaceComputer *computeraction.CreateWorkspaceComputerAction
	resetComputerCredential *computeraction.ResetComputerCredentialAction
	computerCredential      *computeraction.AuthenticateComputerQuery
	touchComputer           *computeraction.TouchComputerAction
	reportCapabilities      *computeraction.ReportCapabilitiesAction
	computerOperations      *computeraction.OperationsAction
}

// newComputerOps 创建电脑的业务实现依赖。
func newComputerOps(db *bun.DB, settler computeraction.ToolCallSettler) *computerOps {
	return &computerOps{
		db:                      db,
		registerComputer:        computeraction.NewRegisterComputerAction(db),
		listComputers:           computeraction.NewListComputersQuery(db),
		revokeComputer:          computeraction.NewRevokeComputerAction(db, settler),
		createWorkspaceComputer: computeraction.NewCreateWorkspaceComputerAction(db),
		resetComputerCredential: computeraction.NewResetComputerCredentialAction(db, settler),
		computerCredential:      computeraction.NewAuthenticateComputerQuery(db),
		touchComputer:           computeraction.NewTouchComputerAction(db),
		reportCapabilities:      computeraction.NewReportCapabilitiesAction(db),
		computerOperations:      computeraction.NewOperationsAction(db, settler),
	}
}

// authenticateComputer 以 RequestMeta.Token 携带的电脑凭据认证电脑，返回电脑身份与记入电脑所属工作区日志作用域的 context；凭据无效时返回登录会话错误，执行器版本与服务端不一致时返回需要升级的会话错误。
func (o *computerOps) authenticateComputer(ctx context.Context, meta appservice.RequestMeta) (context.Context, computeraction.Identity, error) {
	if meta.ExecutorVersion != domain.ExecutorVersion {
		return ctx, computeraction.Identity{}, appservice.ExecutorUpgradeError(meta)
	}
	computer, err := o.computerCredential.Execute(ctx, meta.Token)
	if errors.Is(err, computeraction.ErrCredentialInvalid) {
		return ctx, computeraction.Identity{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorComputerCredentialInvalid)
	}
	if err != nil {
		return ctx, computeraction.Identity{}, appservice.FailedError(meta, i18n.ErrorComputerRequestFailed, err)
	}
	return logscope.WithWorkspace(ctx, computer.WorkspaceID), computer, nil
}

// RegisterComputer 把执行器所在电脑注册为当前成员的个人电脑，返回电脑凭据。
func (o *computerOps) RegisterComputer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ComputerRegistrationInput) (appservice.ComputerRegistration, error) {
	registration, err := o.registerComputer.Execute(ctx, identity, computeraction.RegisterInput{
		InstallID: input.InstallID, Name: input.Name,
	})
	if err != nil {
		return appservice.ComputerRegistration{}, computerError(meta, err, i18n.ErrorComputerRegisterFailed)
	}
	return appservice.ComputerRegistration{Computer: computerFromAction(registration.Record), Credential: registration.Credential}, nil
}

// ListComputers 返回当前成员未撤销的个人电脑。
func (o *computerOps) ListComputers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ComputerList, error) {
	return o.computerList(ctx, meta, identity, domain.ComputerKindPersonal)
}

// ListWorkspaceComputers 返回当前工作区未撤销的工作区电脑。
func (o *computerOps) ListWorkspaceComputers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ComputerList, error) {
	return o.computerList(ctx, meta, identity, domain.ComputerKindWorkspace)
}

// computerList 返回当前成员可见的指定类型电脑。
func (o *computerOps) computerList(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, kind domain.ComputerKind) (appservice.ComputerList, error) {
	records, err := o.listComputers.Execute(ctx, identity, kind)
	if err != nil {
		return appservice.ComputerList{}, computerError(meta, err, i18n.ErrorComputerListFailed)
	}
	computers := arr.Map(records, computerFromAction)
	return appservice.ComputerList{Computers: computers}, nil
}

// CreateWorkspaceComputer 添加工作区电脑并返回电脑凭据。
func (o *computerOps) CreateWorkspaceComputer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.WorkspaceComputerInput) (appservice.ComputerRegistration, error) {
	registration, err := o.createWorkspaceComputer.Execute(ctx, identity, input.Name)
	if err != nil {
		return appservice.ComputerRegistration{}, computerError(meta, err, i18n.ErrorComputerCreateFailed)
	}
	return appservice.ComputerRegistration{Computer: computerFromAction(registration.Record), Credential: registration.Credential}, nil
}

// ResetComputerCredential 为工作区电脑签发新凭据。
func (o *computerOps) ResetComputerCredential(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, computerID string) (appservice.ComputerRegistration, error) {
	registration, err := o.resetComputerCredential.Execute(ctx, identity, computerID)
	if err != nil {
		return appservice.ComputerRegistration{}, computerError(meta, err, i18n.ErrorComputerCredentialResetFailed)
	}
	return appservice.ComputerRegistration{Computer: computerFromAction(registration.Record), Credential: registration.Credential}, nil
}

// RevokeComputer 撤销当前成员的个人电脑或工作区电脑。
func (o *computerOps) RevokeComputer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, computerID string) error {
	if err := o.revokeComputer.Execute(ctx, identity, computerID); err != nil {
		return computerError(meta, err, i18n.ErrorComputerRevokeFailed)
	}
	return nil
}

// ReportComputerCapabilities 保存本电脑上报的执行能力。
func (o *computerOps) ReportComputerCapabilities(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, input appservice.ComputerCapabilitiesInput) error {
	if err := o.reportCapabilities.Execute(ctx, computer, computeraction.CapabilitiesInput{
		Platform: input.Platform, Capabilities: input.Capabilities, ExecutorVersion: input.ExecutorVersion, MaxConcurrency: input.MaxConcurrency,
	}); err != nil {
		return computerRequestError(meta, err)
	}
	return nil
}

// ClaimComputerOperations 领取派发给本电脑的待执行操作，并返回应当中止的操作。
func (o *computerOps) ClaimComputerOperations(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, input appservice.ComputerClaimInput) (appservice.ComputerOperationList, error) {
	claimed, err := o.computerOperations.Claim(ctx, computer, computeraction.ClaimInput{
		Limit: input.Limit, Running: input.Running, Sessions: input.Sessions, Permissions: input.Permissions,
	})
	if err != nil {
		return appservice.ComputerOperationList{}, computerRequestError(meta, err)
	}
	return appservice.ComputerOperationList{
		Operations: arr.Map(claimed.Operations, func(operation computeraction.Operation) appservice.ComputerOperationItem {
			return appservice.ComputerOperationItem{
				ID: operation.ID, Operation: operation.Operation, TimeoutSeconds: int(operation.Timeout.Seconds()), TraceID: operation.TraceID,
			}
		}),
		Abort: claimed.Abort, Released: claimed.Released,
		Permissions: arr.Map(claimed.Permissions, func(permission computeraction.PermissionResult) appservice.ComputerPermissionResult {
			return appservice.ComputerPermissionResult{ID: permission.ID, OptionID: permission.OptionID}
		}),
	}, nil
}

// CompleteComputerOperation 记录本电脑执行一次操作的结果。
func (o *computerOps) CompleteComputerOperation(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string, input appservice.ComputerOutcomeInput) error {
	if err := o.computerOperations.Complete(ctx, computer, operationID, input.Outcome); err != nil {
		return computerRequestError(meta, err)
	}
	return nil
}

// ReportComputerOperationUpdates 记录本电脑执行中的一次操作的过程更新。
func (o *computerOps) ReportComputerOperationUpdates(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string, input appservice.ComputerUpdatesInput) error {
	updates := arr.Map(input.Updates, func(update appservice.ComputerOperationUpdate) computeraction.UpdateInput {
		return computeraction.UpdateInput{Seq: update.Seq, Update: update.Update}
	})
	if err := o.computerOperations.ReportUpdates(ctx, computer, operationID, updates); err != nil {
		return computerRequestError(meta, err)
	}
	return nil
}

// RequestComputerPermission 记录本电脑上本机 Agent 请求的权限，交给这一轮的发起人确认。
func (o *computerOps) RequestComputerPermission(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string, input appservice.ComputerPermissionInput) error {
	if err := o.computerOperations.RequestPermission(ctx, computer, operationID, computeraction.PermissionInput{ID: input.ID, Permission: input.Permission}); err != nil {
		return computerRequestError(meta, err)
	}
	return nil
}

// computerErrors 是电脑管理操作的错误转换规则。
var computerErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(computeraction.ErrNotFound, dispatch.NotFound(i18n.ErrorComputerNotFound)),
})

// computerError 转换电脑管理操作错误；名称校验失败报在名称字段，执行器上报的安装标识校验失败按操作失败收敛。
func computerError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return computerErrors.Translate(meta, err, failureKey)
}

// computerRequestError 转换执行器请求的错误。
func computerRequestError(meta appservice.RequestMeta, err error) error {
	return appservice.FailedError(meta, i18n.ErrorComputerRequestFailed, err)
}

// computerFromAction 转换电脑输出。
func computerFromAction(input computeraction.Record) appservice.Computer {
	var platform *appservice.ComputerPlatform
	if input.Platform != nil {
		platform = new(*input.Platform)
	}
	return appservice.Computer{
		ID: input.ID, Kind: input.Kind, Name: input.Name, Platform: platform,
		Online: input.Online, AgentCount: input.AgentCount, LastSeenAt: input.LastSeenAt, CreatedAt: input.CreatedAt,
		LocalAgents: computerLocalAgents(input.LocalAgents),
	}
}

// computerLocalAgents 转换电脑上报的本机 Agent。
func computerLocalAgents(agents []domain.ComputerLocalAgent) []appservice.ComputerLocalAgent {
	return arr.Map(agents, func(agent domain.ComputerLocalAgent) appservice.ComputerLocalAgent {
		return appservice.ComputerLocalAgent{Name: agent.Name, Description: agent.Description}
	})
}
