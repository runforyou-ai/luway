//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

var _ appservice.ComputerBackend = (*Backend)(nil)

// computerOps 持有电脑的 Action 和 Query。
type computerOps struct {
	registerComputer   *computeraction.RegisterComputerAction
	listComputers      *computeraction.ListComputersQuery
	revokeComputer     *computeraction.RevokeComputerAction
	computerCredential *computeraction.AuthenticateComputerQuery
	touchComputer      *computeraction.TouchComputerAction
	reportCapabilities *computeraction.ReportCapabilitiesAction
	computerOperations *computeraction.OperationsAction
}

// newComputerOps 创建电脑的业务实现依赖。
func newComputerOps(db *bun.DB, enqueuer servertask.TxEnqueuer) computerOps {
	return computerOps{
		registerComputer:   computeraction.NewRegisterComputerAction(db),
		listComputers:      computeraction.NewListComputersQuery(db),
		revokeComputer:     computeraction.NewRevokeComputerAction(db, enqueuer),
		computerCredential: computeraction.NewAuthenticateComputerQuery(db),
		touchComputer:      computeraction.NewTouchComputerAction(db),
		reportCapabilities: computeraction.NewReportCapabilitiesAction(db),
		computerOperations: computeraction.NewOperationsAction(db, enqueuer),
	}
}

// ComputerSession 定义实时网关持有的已认证电脑。
type ComputerSession struct {
	OrganizationID string
	ComputerID     string
}

// AuthenticateComputer 校验执行器事件流请求携带的电脑凭据并返回电脑会话。
func (b *Backend) AuthenticateComputer(ctx context.Context, meta appservice.RequestMeta) (ComputerSession, error) {
	computer, err := b.ops.authenticateComputer(ctx, meta)
	if err != nil {
		return ComputerSession{}, err
	}
	return ComputerSession{OrganizationID: computer.OrganizationID, ComputerID: computer.ComputerID}, nil
}

// TouchComputer 记录电脑在线，电脑已撤销时返回电脑凭据失效错误。
func (b *Backend) TouchComputer(ctx context.Context, session ComputerSession) error {
	err := b.ops.touchComputer.Execute(ctx, computeraction.Identity{OrganizationID: session.OrganizationID, ComputerID: session.ComputerID})
	if errors.Is(err, computeraction.ErrCredentialInvalid) {
		return appservice.SessionError(appservice.RequestMeta{}, appservice.SessionStateLogin, i18n.ErrorComputerCredentialInvalid)
	}
	return err
}

// authenticateComputer 以 RequestMeta.Token 携带的电脑凭据认证电脑，凭据无效时返回登录会话错误。
func (o *directOperations) authenticateComputer(ctx context.Context, meta appservice.RequestMeta) (computeraction.Identity, error) {
	computer, err := o.computerCredential.Execute(ctx, meta.Token)
	if errors.Is(err, computeraction.ErrCredentialInvalid) {
		return computeraction.Identity{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorComputerCredentialInvalid)
	}
	if err != nil {
		if ctx.Err() != nil {
			return computeraction.Identity{}, ctx.Err()
		}
		slog.Warn("电脑认证失败", "error", err)
		return computeraction.Identity{}, appservice.FailedError(meta, i18n.ErrorComputerRequestFailed)
	}
	return computer, nil
}

// RegisterComputer 把执行器所在电脑注册为当前成员的个人电脑，返回电脑凭据。
func (o *directOperations) RegisterComputer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ComputerRegistrationInput) (appservice.ComputerRegistration, error) {
	registration, err := o.registerComputer.Execute(ctx, identity, computeraction.RegisterInput{
		InstallID: input.InstallID, Name: input.Name, Platform: domain.ComputerPlatform(input.Platform),
	})
	if err != nil {
		return appservice.ComputerRegistration{}, o.computerError(ctx, meta, err, i18n.ErrorComputerRegisterFailed, identity.Organization.ID)
	}
	return appservice.ComputerRegistration{Computer: computerFromAction(registration.Record), Credential: registration.Credential}, nil
}

// ListComputers 返回当前成员未撤销的电脑。
func (o *directOperations) ListComputers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ComputerList, error) {
	records, err := o.listComputers.Execute(ctx, identity)
	if err != nil {
		return appservice.ComputerList{}, o.computerError(ctx, meta, err, i18n.ErrorComputerListFailed, identity.Organization.ID)
	}
	computers := make([]appservice.Computer, 0, len(records))
	for _, record := range records {
		computers = append(computers, computerFromAction(record))
	}
	return appservice.ComputerList{Computers: computers}, nil
}

// RevokeComputer 撤销当前成员的电脑。
func (o *directOperations) RevokeComputer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, computerID string) error {
	if err := o.revokeComputer.Execute(ctx, identity, computerID); err != nil {
		return o.computerError(ctx, meta, err, i18n.ErrorComputerRevokeFailed, identity.Organization.ID, "computer_id", computerID)
	}
	return nil
}

// ReportComputerCapabilities 保存本电脑上报的执行能力。
func (o *directOperations) ReportComputerCapabilities(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, input appservice.ComputerCapabilitiesInput) error {
	if err := o.reportCapabilities.Execute(ctx, computer, computeraction.CapabilitiesInput{
		Capabilities: input.Capabilities, ExecutorVersion: input.ExecutorVersion, MaxConcurrency: input.MaxConcurrency,
	}); err != nil {
		return o.computerRequestError(ctx, meta, err, computer)
	}
	return nil
}

// ClaimComputerOperations 领取派发给本电脑的待执行操作，并返回应当中止的操作。
func (o *directOperations) ClaimComputerOperations(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, input appservice.ComputerClaimInput) (appservice.ComputerOperationList, error) {
	claimed, err := o.computerOperations.Claim(ctx, computer, input.Limit, input.Running)
	if err != nil {
		return appservice.ComputerOperationList{}, o.computerRequestError(ctx, meta, err, computer)
	}
	output := appservice.ComputerOperationList{Operations: make([]appservice.ComputerOperationItem, 0, len(claimed.Operations)), Abort: claimed.Abort}
	for _, operation := range claimed.Operations {
		output.Operations = append(output.Operations, appservice.ComputerOperationItem{ID: operation.ID, Operation: operation.Operation})
	}
	return output, nil
}

// CompleteComputerOperation 记录本电脑执行一次操作的结果。
func (o *directOperations) CompleteComputerOperation(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string, input appservice.ComputerOutcomeInput) error {
	if err := o.computerOperations.Complete(ctx, computer, operationID, input.Outcome); err != nil {
		return o.computerRequestError(ctx, meta, err, computer)
	}
	return nil
}

// computerError 转换电脑管理操作错误。电脑注册信息由执行器上报，校验失败按注册失败收敛并记录字段原因码。
func (o *directOperations) computerError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID string, attributes ...any) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, computeraction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorComputerNotFound)
	}
	logAttributes := []any{"organization_id", organizationID, "failure", failureKey}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		logAttributes = append(logAttributes, "fields", validationError.Fields)
	} else {
		logAttributes = append(logAttributes, "error", err)
	}
	slog.Warn("电脑操作失败", append(logAttributes, attributes...)...)
	return appservice.FailedError(meta, failureKey)
}

// computerRequestError 转换执行器请求的错误。
func (o *directOperations) computerRequestError(ctx context.Context, meta appservice.RequestMeta, err error, computer computeraction.Identity) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	slog.Warn("执行器请求失败", "organization_id", computer.OrganizationID, "computer_id", computer.ComputerID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorComputerRequestFailed)
}

// computerFromAction 转换电脑输出。
func computerFromAction(input computeraction.Record) appservice.Computer {
	return appservice.Computer{
		ID: input.ID, Name: input.Name, Platform: appservice.ComputerPlatform(input.Platform), Online: input.Online,
		LastSeenAt: input.LastSeenAt, CreatedAt: input.CreatedAt,
	}
}
