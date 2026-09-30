/** 移动端企业成员只读资料和发消息入口。 */
import { useTranslation } from "react-i18next"
import { Link, useParams } from "react-router"

import { getUser, isNotFoundApiError, UserStatus } from "@/api"
import {
  MobilePageHeader,
  MobilePageState,
  MobileScrollArea,
} from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { WorkStatusBadge } from "@/components/work-status"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 展示姓名、邮箱、团队和工作状态，只允许向其他活跃成员发消息。 */
export function MobileEmployeeProfilePage() {
  const { t } = useTranslation(["mobile", "common", "contacts"])
  const { userID = "" } = useParams()
  const { identity } = useMobileWorkspace()
  const {
    data: user,
    loading,
    error,
    refresh,
  } = useResource(resourceKeys.user(userID), () => getUser(userID), {
    staleTime: 0,
  })

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("contacts.profile")}
        backTo="/contacts/employees"
        actions={
          error && user ? (
            <Button
              variant="ghost"
              className="min-h-11"
              onClick={() => void refresh()}
            >
              {t("refreshFailed")}
            </Button>
          ) : undefined
        }
      />
      <MobileScrollArea
        storageKey={`employee:${userID}`}
        ready={Boolean(user)}
        className="px-4 py-6"
      >
        {loading && !user ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : null}
        {error && !user ? (
          <MobilePageState
            title={t(
              isNotFoundApiError(error)
                ? "contacts.notFound"
                : "contacts.profileError",
            )}
            onRetry={() => void refresh()}
          />
        ) : null}
        {user ? (
          <div className="space-y-9">
            <div>
              <div className="flex items-center gap-3 pb-6">
                <ProfileAvatar
                  name={user.displayName}
                  imageURL={user.avatarUrl}
                  className="size-14"
                />
                <div className="min-w-0 space-y-2">
                  <h2 className="break-words text-lg font-semibold">
                    {user.displayName}
                  </h2>
                  {user.status === UserStatus.UserStatusActive ? (
                    <WorkStatusBadge status={user.workStatus} />
                  ) : (
                    <p className="text-sm text-muted-foreground">
                      {t("contacts.inactive")}
                    </p>
                  )}
                </div>
              </div>
              <dl className="divide-y border-y">
                <div className="py-4">
                  <dt className="text-xs text-muted-foreground">
                    {t("me.email")}
                  </dt>
                  <dd className="mt-1 break-all text-sm">{user.email}</dd>
                </div>
                <div className="py-4">
                  <dt className="text-xs text-muted-foreground">
                    {t("contacts:columns.teams")}
                  </dt>
                  <dd className="mt-1 break-words text-sm">
                    {user.teams.length
                      ? user.teams.map((team) => (
                          <span key={team.id} className="block">
                            {team.name}
                          </span>
                        ))
                      : t("contacts.noTeams")}
                  </dd>
                </div>
              </dl>
            </div>
            {user.status === UserStatus.UserStatusActive &&
            user.identityId !== identity.user.identityId ? (
              <Button asChild className="min-h-11 w-full">
                <Link
                  to={`/contacts/employees/${user.id}/chat`}
                  state={{ mobileBack: true }}
                >
                  {t("contacts.sendMessage")}
                </Link>
              </Button>
            ) : null}
          </div>
        ) : null}
      </MobileScrollArea>
    </section>
  )
}
