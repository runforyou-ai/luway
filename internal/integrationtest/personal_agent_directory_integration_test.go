//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestPersonalAgentDirectory 验证 AI 员工目录只列出服务型 AI 员工与本人负责的个人 AI 员工，个人 AI 员工不进入成员候选、写回复候选和服务型 AI 员工的管理操作，服务型 AI 员工的负责人不作为个人 AI 员工负责人展示。
func TestPersonalAgentDirectory(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	member := newChatLockUser(t, db, identity)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}}
	service, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "目录服务员工", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, Execution: execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentaction.NewUpdateAgentAction(db, testServiceSessionReturner(db)).Execute(ctx, identity, service.ID, agentaction.UpdateInput{
		DisplayName: service.DisplayName, ServiceAudiences: service.ServiceAudiences, ResponsibleUserID: member.User.ID, WorkStatus: domain.WorkStatusWorking,
	}); err != nil {
		t.Fatal(err)
	}
	createPersonal := func(owner *servermodels.Identity, name string) *agentaction.PersonalAgent {
		t.Helper()
		device, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, owner, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: name + "的电脑", Platform: domain.DevicePlatformMacOS})
		if err != nil {
			t.Fatal(err)
		}
		created, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, owner, device.ID, agentaction.PersonalAgentInput{DisplayName: name, Execution: execution})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	own := createPersonal(member, "目录本人员工")
	other := createPersonal(identity, "目录他人员工")

	t.Run("目录列出服务型与本人负责的个人 AI 员工", func(t *testing.T) {
		output, err := agentaction.NewListAgentsQuery(db).Execute(ctx, member, agentaction.ListInput{Query: "目录", Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(output.Agents))
		for _, item := range output.Agents {
			ids = append(ids, item.ID)
			switch item.ID {
			case own.ID:
				if !item.Personal() || common.StringValue(item.DeviceID) != own.DeviceID || item.Presence(own.CreatedAt) != domain.PersonalAgentPresenceOffline {
					t.Fatalf("own personal item=%+v", item)
				}
			case service.ID:
				if item.Personal() || item.DeviceID != nil {
					t.Fatalf("service item=%+v", item)
				}
			}
		}
		if !slices.Contains(ids, service.ID) || !slices.Contains(ids, own.ID) || slices.Contains(ids, other.ID) || output.Page.Total != 2 {
			t.Fatalf("member directory=%v total=%d", ids, output.Page.Total)
		}
	})

	t.Run("个人 AI 员工不进入成员候选与写回复候选", func(t *testing.T) {
		options, err := memberaction.NewListOptionsQuery(db).Execute(ctx, member, memberaction.ListOptionsInput{Query: "目录", Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(options.Members) != 1 || options.Members[0].ID != service.IdentityID {
			t.Fatalf("member options=%+v", options.Members)
		}
		replyAgents, err := agentrunaction.NewListServiceReplyAgentsQuery(db).Execute(ctx, member)
		if err != nil {
			t.Fatal(err)
		}
		for _, agent := range replyAgents {
			if agent.IdentityID == own.IdentityID || agent.IdentityID == other.IdentityID {
				t.Fatalf("reply agents include personal=%+v", replyAgents)
			}
		}
	})

	t.Run("服务型 AI 员工的管理操作不作用于个人 AI 员工", func(t *testing.T) {
		if _, err := agentaction.NewGetAgentQuery(db).Execute(ctx, member, own.ID); !errors.Is(err, agentaction.ErrNotFound) {
			t.Fatalf("get personal as service=%v", err)
		}
		if _, err := agentaction.NewUpdateAgentAction(db, testServiceSessionReturner(db)).Execute(ctx, member, own.ID, agentaction.UpdateInput{
			DisplayName: own.DisplayName, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking,
		}); !errors.Is(err, agentaction.ErrNotFound) {
			t.Fatalf("update personal as service=%v", err)
		}
		if _, _, err := agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, member, other.ID); !errors.Is(err, agentaction.ErrPersonalAgentNotFound) {
			t.Fatalf("get other's personal=%v", err)
		}
		if _, _, err := agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, member, service.ID); !errors.Is(err, agentaction.ErrPersonalAgentNotFound) {
			t.Fatalf("get service as personal=%v", err)
		}
	})

	t.Run("服务型 AI 员工的负责人不作为个人 AI 员工负责人展示", func(t *testing.T) {
		group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
			Title: "目录群", MemberIdentityIDs: []string{member.OrganizationIdentity.ID, service.IdentityID},
		})
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, participant := range loaded.Participants {
			if participant.PersonalResponsibleName != nil || participant.PersonalResponsibleIdentityID != nil {
				t.Fatalf("participant responsible=%+v", participant)
			}
		}
	})
}
