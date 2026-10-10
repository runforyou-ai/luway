/** 设置中工作区成员的编辑页。 */
import { useTranslation } from "react-i18next"
import { useParams } from "react-router"

import { getUser, isNotFoundApiError, listRoleOptions, listTeams } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { useWorkspace } from "@/contexts/workspace-context"
import { useContactInvalidator } from "@/hooks/use-contact-invalidator"
import { MemberPersonalAgentsSection } from "@/components/member-personal-agents-section"
import { MemberForm } from "@/features/settings/members/member-form"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"

const listPath = "/settings/members"

/** 边改边存已有成员；返回时回到来源列表的筛选和页码。 */
export function MemberFormPage() {
  const { t } = useTranslation("contacts")
  const { t: tSettings } = useTranslation("settings")
  const { userId = "" } = useParams()
  const { identity } = useWorkspace()
  const invalidate = useResourceInvalidator()
  const invalidateContact = useContactInvalidator()
  const roles = useResource(resourceKeys.roleOptions(), () => listRoleOptions())
  const teams = useResource(resourceKeys.teams({ pageSize: 100 }), () =>
    listTeams({ pageSize: 100 }),
  )
  const detail = useResource(resourceKeys.user(userId), () => getUser(userId))
  const user = detail.data
  const { returnTo, leave } = useReturnTo(listPath, {
    notFound: isNotFoundApiError(detail.error),
    logFields: { user_id: userId },
  })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={user?.displayName ?? t("members.editTitle")}
        description={t("members.editDescription")}
        backTo={returnTo}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={[roles, teams, detail]}
          errorMessage={tSettings("members.detailLoadError")}
        >
          {user ? (
            <div className="flex flex-col gap-9">
              {/* 角色选项为当前成员可以分配的角色与该成员当前所属的角色。 */}
              <MemberForm
                key={user.id}
                user={user}
                roles={(roles.data?.roles ?? []).filter((role) => role.assignable || role.id === user.role.id)}
                teams={teams.data?.teams ?? []}
                onSaved={(saved) => {
                  void invalidateContact("user", saved.id)
                  if (saved.id === identity.user.id) {
                    void invalidate(resourceKeys.identity())
                  }
                }}
                onNotFound={() => {
                  void invalidateContact("user")
                  leave({ replace: true })
                }}
              />
              <MemberPersonalAgentsSection userId={user.id} />
            </div>
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}
