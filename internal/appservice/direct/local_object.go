//go:build server

package direct

import (
	"context"
	"errors"
	"strings"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

var (
	// ErrLocalObjectUnauthorized 表示本地对象请求的凭据缺失或无效。
	ErrLocalObjectUnauthorized = errors.New("local object unauthorized")
	// ErrLocalObjectNotFound 表示本地对象不存在或不属于请求者。
	ErrLocalObjectNotFound = errors.New("local object not found")
	// ErrLocalObjectConflict 表示本地对象不处于可写入内容的状态。
	ErrLocalObjectConflict = errors.New("local object conflict")
	// ErrLocalObjectInvalid 表示本地对象写入参数不合法。
	ErrLocalObjectInvalid = errors.New("local object invalid")
)

// LocalObjectCredentials 定义本地对象请求携带的凭据：成员登录令牌、网站登录用户签名身份或匿名访客令牌，按签名身份、访客令牌、成员令牌的顺序取第一个非空值。
type LocalObjectCredentials struct {
	Bearer        string
	VisitorToken  string
	CustomerToken string
}

// LocalObjectUpload 定义通过授权的本地对象写入；PartNumber 大于 0 表示写入分片，ExpectedSize 为本次请求应写入的字节数。
type LocalObjectUpload struct {
	FileID       string
	PartNumber   int32
	ExpectedSize int64
}

// LocalObjectAuthorizer 为服务端本地对象的直传和知识文档预览完成认证与归属校验，对象所属工作区取自对象键。
type LocalObjectAuthorizer struct {
	resolveIdentity *authaction.ResolveIdentityQuery
	computer        *computeraction.AuthenticateComputerQuery
	verifyCustomer  *customerchataction.VerifyWebsiteCustomerQuery
	getFile         *fileaction.GetQuery
}

// NewLocalObjectAuthorizer 创建本地对象授权器。
func NewLocalObjectAuthorizer(db *bun.DB) *LocalObjectAuthorizer {
	return &LocalObjectAuthorizer{
		resolveIdentity: authaction.NewResolveIdentityQuery(db),
		computer:        computeraction.NewAuthenticateComputerQuery(db),
		verifyCustomer:  customerchataction.NewVerifyWebsiteCustomerQuery(db),
		getFile:         fileaction.NewGetQuery(db),
	}
}

// AuthorizeUpload 按凭据确定上传者并校验文件归属与待写入状态：网站登录用户按对象键所属工作区验签，匿名访客按访客令牌，电脑按电脑凭据，成员按登录令牌；partNumber 为分片文件的序号参数。
func (a *LocalObjectAuthorizer) AuthorizeUpload(ctx context.Context, credentials LocalObjectCredentials, storageKey, partNumber string) (LocalObjectUpload, error) {
	workspaceID := storageKeyWorkspaceID(storageKey)
	var upload fileaction.LocalUpload
	var err error
	switch {
	case credentials.CustomerToken != "":
		verified, verifyErr := a.verifyCustomer.ExecuteForWorkspace(ctx, workspaceID, credentials.CustomerToken)
		if errors.Is(verifyErr, customerchataction.ErrCustomerIdentityInvalid) {
			return LocalObjectUpload{}, ErrLocalObjectUnauthorized
		}
		if verifyErr != nil {
			return LocalObjectUpload{}, verifyErr
		}
		upload, err = a.getFile.AuthorizeVisitorLocalUpload(ctx, workspaceID, customeridentity.CustomerExternalID(verified.Customer.UserID), storageKey, partNumber)
	case credentials.VisitorToken != "":
		if !customeridentity.ValidAnonymousToken(credentials.VisitorToken) {
			return LocalObjectUpload{}, ErrLocalObjectUnauthorized
		}
		upload, err = a.getFile.AuthorizeVisitorLocalUpload(ctx, "", customeridentity.AnonymousExternalID(credentials.VisitorToken), storageKey, partNumber)
	default:
		// 电脑凭据只能写入该电脑同步会话共享文件时创建的内容文件。
		computer, computerErr := a.computer.Execute(ctx, credentials.Bearer)
		if computerErr == nil && computer.WorkspaceID == workspaceID {
			upload, err = a.getFile.AuthorizeComputerLocalUpload(ctx, workspaceID, computer.ComputerID, storageKey)
			break
		}
		if computerErr != nil && !errors.Is(computerErr, computeraction.ErrCredentialInvalid) {
			return LocalObjectUpload{}, computerErr
		}
		identity, identityErr := a.resolveIdentity.Execute(ctx, workspaceID, credentials.Bearer)
		if identityErr != nil {
			return LocalObjectUpload{}, localObjectIdentityError(identityErr)
		}
		upload, err = a.getFile.AuthorizeMemberLocalUpload(ctx, identity, storageKey, partNumber)
	}
	switch {
	case err == nil:
		return LocalObjectUpload{FileID: upload.FileID, PartNumber: upload.PartNumber, ExpectedSize: upload.ExpectedSize}, nil
	case errors.Is(err, fileaction.ErrFileNotFound):
		return LocalObjectUpload{}, ErrLocalObjectNotFound
	case errors.Is(err, fileaction.ErrUploadNotPending):
		return LocalObjectUpload{}, ErrLocalObjectConflict
	case errors.Is(err, fileaction.ErrUploadPartInvalid):
		return LocalObjectUpload{}, ErrLocalObjectInvalid
	default:
		return LocalObjectUpload{}, err
	}
}

// AuthorizeKnowledgePreview 校验成员可读取仍在使用的知识文档原件，返回原始文件名。
func (a *LocalObjectAuthorizer) AuthorizeKnowledgePreview(ctx context.Context, bearer, storageKey string) (string, error) {
	identity, err := a.resolveIdentity.Execute(ctx, storageKeyWorkspaceID(storageKey), bearer)
	if err != nil {
		return "", localObjectIdentityError(err)
	}
	record, err := a.getFile.ExecuteByStorageKey(ctx, identity, storageKey)
	if errors.Is(err, fileaction.ErrFileNotFound) || (err == nil && (record.Status != string(domain.FileStatusActive) || record.Purpose != string(domain.FilePurposeKnowledgeDocument))) {
		return "", ErrLocalObjectNotFound
	}
	if err != nil {
		return "", err
	}
	return record.OriginalName, nil
}

// localObjectIdentityError 把成员身份解析错误转换为本地对象错误，令牌无效、不是该工作区成员或工作区已暂停时视为未认证。
func localObjectIdentityError(err error) error {
	if errors.Is(err, authaction.ErrIdentityNotFound) || errors.Is(err, authaction.ErrMembershipNotFound) || errors.Is(err, authaction.ErrWorkspaceSuspended) {
		return ErrLocalObjectUnauthorized
	}
	return err
}

// storageKeyWorkspaceID 返回规范对象键中的工作区编号，对象键格式为 workspaces/<工作区编号>/<类别>/<文件名>。
func storageKeyWorkspaceID(storageKey string) string {
	return strings.Split(storageKey, "/")[1]
}
