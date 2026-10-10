//go:build server

package conversationfile

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RunAuthor 是运行写入会话共享文件区时的文件区与署名：Area 是文件区所属会话，SubjectID 是作为修改人的 AI 员工聊天主体，UserID 是内容文件的上传用户。
type RunAuthor struct {
	Area      string
	SubjectID string
	UserID    string
}

// LoadRunAuthor 按运行所属会话确定共享文件区，以 AI 员工的聊天主体为修改人，以配置版本的创建人为内容文件的上传用户。
func LoadRunAuthor(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (RunAuthor, error) {
	area, err := AreaConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return RunAuthor{}, err
	}
	author := RunAuthor{Area: area}
	if err := db.NewSelect().Model((*servermodels.ChatSubject)(nil)).Column("id").
		Where("workspace_id = ? AND kind = ? AND source_id = ?", run.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, run.AgentIdentityID).
		Scan(ctx, &author.SubjectID); err != nil {
		return RunAuthor{}, fmt.Errorf("load agent chat subject for shared files: %w", err)
	}
	if err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Column("created_by_user_id").
		Where("workspace_id = ? AND id = ?", run.WorkspaceID, run.AgentRevisionID).
		Scan(ctx, &author.UserID); err != nil {
		return RunAuthor{}, fmt.Errorf("load agent revision creator for shared files: %w", err)
	}
	return author, nil
}
