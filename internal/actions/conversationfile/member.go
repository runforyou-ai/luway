//go:build server

package conversationfile

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// MemberFiles 为成员提供会话文件区的列表、上传、保存附件、删除与下载，可阅读会话的成员即可使用该会话的文件区。
type MemberFiles struct {
	store *Store
}

// NewMemberFiles 创建成员侧的会话文件区操作。
func NewMemberFiles(store *Store) *MemberFiles {
	return &MemberFiles{store: store}
}

// area 校验成员可阅读会话并返回其文件区所属会话编号。
func (m *MemberFiles) area(ctx context.Context, identity *servermodels.Identity, conversationID string) (string, error) {
	if err := conversationaccess.RequireReadable(ctx, m.store.db, identity, conversationID); err != nil {
		return "", err
	}
	return AreaConversation(ctx, m.store.db, identity.Workspace.ID, conversationID)
}

// authorize 返回写入事务开始时的复核：锁定活跃用户，再锁定会话并确认成员仍可阅读会话。
func authorize(identity *servermodels.Identity, conversationID string) func(context.Context, bun.Tx) error {
	return func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := chatstate.LockConversation(ctx, tx, identity.Workspace.ID, conversationID); err != nil {
			return err
		}
		return conversationaccess.RequireReadable(ctx, tx, identity, conversationID)
	}
}

// subject 返回成员的聊天主体编号。
func (m *MemberFiles) subject(ctx context.Context, identity *servermodels.Identity) (string, error) {
	var subjectID string
	if err := m.store.db.NewSelect().Model((*servermodels.ChatSubject)(nil)).Column("id").
		Where("workspace_id = ? AND kind = ? AND source_id = ?", identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
		Scan(ctx, &subjectID); err != nil {
		return "", fmt.Errorf("load member chat subject for conversation file: %w", err)
	}
	return subjectID, nil
}

// List 返回会话文件区中的全部文件。
func (m *MemberFiles) List(ctx context.Context, identity *servermodels.Identity, conversationID string) ([]Entry, error) {
	area, err := m.area(ctx, identity, conversationID)
	if err != nil {
		return nil, err
	}
	return m.store.List(ctx, identity.Workspace.ID, area)
}

// AddUpload 把成员上传完成的文件以原文件名加入会话文件区，同名文件已存在时在文件名后追加序号。
func (m *MemberFiles) AddUpload(ctx context.Context, identity *servermodels.Identity, conversationID, fileID string) (Entry, error) {
	area, err := m.area(ctx, identity, conversationID)
	if err != nil {
		return Entry{}, err
	}
	file := &servermodels.File{}
	err = m.store.db.NewSelect().Model(file).
		Where("f.workspace_id = ? AND f.id = ? AND f.created_by_user_id = ? AND f.purpose = ?", identity.Workspace.ID, fileID, identity.User.ID, domain.FilePurposeConversationFile).
		Where("f.status = ? AND f.expires_at > now()", domain.FileStatusUploaded).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("load uploaded conversation file: %w", err)
	}
	subjectID, err := m.subject(ctx, identity)
	if err != nil {
		return Entry{}, err
	}
	// 内容摘要按上传后的实际内容流式计算。
	reader, err := m.store.opener.Open(ctx, file)
	if err != nil {
		return Entry{}, fmt.Errorf("open uploaded conversation file: %w", err)
	}
	digest := sha256.New()
	_, err = io.Copy(digest, reader)
	if err := errors.Join(err, reader.Close()); err != nil {
		return Entry{}, fmt.Errorf("hash uploaded conversation file: %w", err)
	}
	filePath, ok := domain.NormalizeConversationFilePath(file.OriginalName)
	if !ok {
		return Entry{}, ErrInvalidPath
	}
	entry, _, err := m.store.commit(ctx, WriteInput{
		WorkspaceID: identity.Workspace.ID, ConversationID: area, Path: filePath, Mode: WriteRenamed,
		SubjectID: subjectID, UserID: identity.User.ID, Authorize: authorize(identity, conversationID),
	}, file, hex.EncodeToString(digest.Sum(nil)))
	return entry, err
}

// SaveAttachment 把会话中一条附件消息的文件以附件名称保存到会话文件区，同名文件已存在时在文件名后追加序号。
func (m *MemberFiles) SaveAttachment(ctx context.Context, identity *servermodels.Identity, conversationID, messageID string) (Entry, error) {
	area, err := m.area(ctx, identity, conversationID)
	if err != nil {
		return Entry{}, err
	}
	subjectID, err := m.subject(ctx, identity)
	if err != nil {
		return Entry{}, err
	}
	return m.store.SaveAttachment(ctx, WriteInput{
		WorkspaceID: identity.Workspace.ID, ConversationID: area, Mode: WriteRenamed, SubjectID: subjectID, UserID: identity.User.ID,
		Authorize: authorize(identity, conversationID),
	}, conversationID, messageID)
}

// Delete 从会话文件区删除文件。
func (m *MemberFiles) Delete(ctx context.Context, identity *servermodels.Identity, conversationID, id string) error {
	area, err := m.area(ctx, identity, conversationID)
	if err != nil {
		return err
	}
	return m.store.Delete(ctx, identity.Workspace.ID, area, id, authorize(identity, conversationID))
}

// File 返回会话文件区中文件当前版本的内容记录，供签发下载地址。
func (m *MemberFiles) File(ctx context.Context, identity *servermodels.Identity, conversationID, id string) (Entry, *servermodels.File, error) {
	area, err := m.area(ctx, identity, conversationID)
	if err != nil {
		return Entry{}, nil, err
	}
	return m.store.Lookup(ctx, identity.Workspace.ID, area, id, "")
}
