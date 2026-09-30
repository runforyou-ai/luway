/** 收件箱当前页签的筛选浮层。 */
import type { ReactNode } from "react"
import { FilterIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  ServiceQueueFilter,
  InboxAssigneeFilter,
  InboxPendingKind,
  InboxScope,
  OrganizationIdentityType,
  ServiceAudience,
  ServiceSessionStatus,
  ServiceSource,
  type InboxAssignee,
  type InboxChannel,
  type InboxQuery,
  type ServiceQueueTeam,
} from "@/api"
import { IconTooltip } from "@/components/icon-tooltip"
import { Button } from "@/components/ui/button"
import { NativeSelect } from "@/components/ui/native-select"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  inboxAssigneeFromParam,
  inboxAssigneeParam,
  inboxPendingKindOptions,
  inboxQueueFromParam,
  inboxQueueParam,
  serviceAudienceOptions,
  type NormalizedInboxQuery,
} from "@/features/inbox/inbox-query"

/** 判断当前筛选是否偏离默认值。 */
function inboxFilterApplied(query: NormalizedInboxQuery) {
  return (
    query.pendingKind !== InboxPendingKind.$zero ||
    query.channelId !== "" ||
    query.source !== ServiceSource.$zero ||
    query.audience !== ServiceAudience.$zero ||
    query.serviceStatus === ServiceSessionStatus.ServiceSessionStatusClosed ||
    inboxAssigneeParam(query) !== ""
  )
}

/** 浮层中带标签的一行下拉选择。 */
function FilterField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="grid gap-1.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </label>
  )
}

/** 按当前页签提供类型、队列、状态、负责人、来源与服务对象筛选；尚未接入的服务对象只展示不可选。 */
export function InboxFilter({
  query,
  channels,
  queueTeams,
  assignees,
  currentIdentityId,
  onChange,
}: {
  query: NormalizedInboxQuery
  channels: InboxChannel[]
  queueTeams: ServiceQueueTeam[]
  assignees: InboxAssignee[]
  currentIdentityId: string
  onChange: (changes: Partial<InboxQuery>) => void
}) {
  const { t } = useTranslation("inbox")
  const applied = inboxFilterApplied(query)
  const pending = query.scope === InboxScope.InboxScopePending
  const assignee = inboxAssigneeParam(query)
  const coworkers = assignees.filter((item) => item.identityId !== currentIdentityId)

  return (
    <Popover>
      {/* 触发按钮宽度固定，已设置条件用角标表示。 */}
      <IconTooltip label={t("filterLabel")}>
        <PopoverTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            className="relative shrink-0 text-muted-foreground"
            aria-label={applied ? t("filterApplied") : t("filterLabel")}
          >
            <FilterIcon />
            {applied ? (
              <span
                aria-hidden="true"
                className="absolute top-1 right-1 size-1.5 rounded-full bg-primary"
              />
            ) : null}
          </Button>
        </PopoverTrigger>
      </IconTooltip>
      {/* 浮层紧贴触发按钮向右展开，改条件时会话列表大部分保持可见。 */}
      <PopoverContent side="right" align="start" className="grid gap-3">
        {pending ? (
          <>
            <FilterField label={t("filterPendingKind")}>
              <NativeSelect
                value={query.pendingKind}
                onChange={(event) =>
                  onChange({ pendingKind: event.target.value as InboxPendingKind })
                }
              >
                <option value="">{t("filterAll")}</option>
                {inboxPendingKindOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {t(option.label)}
                  </option>
                ))}
              </NativeSelect>
            </FilterField>
            {query.pendingKind === InboxPendingKind.InboxPendingKindQueue ? (
              <FilterField label={t("filterQueue")}>
                <NativeSelect
                  value={inboxQueueParam(query)}
                  onChange={(event) => onChange(inboxQueueFromParam(event.target.value))}
                >
                  <option value="">{t("queueFilterAllQueues")}</option>
                  <option value={ServiceQueueFilter.ServiceQueueFilterPublic}>{t("queueFilterPublicQueue")}</option>
                  {queueTeams.filter((team) => team.mine).map((team) => (
                    <option key={team.id} value={team.id}>
                      {team.name}
                    </option>
                  ))}
                </NativeSelect>
              </FilterField>
            ) : null}
          </>
        ) : (
          <>
            <FilterField label={t("filterServiceStatus")}>
              <NativeSelect
                value={query.serviceStatus}
                onChange={(event) =>
                  onChange({ serviceStatus: event.target.value as ServiceSessionStatus })
                }
              >
                <option value={ServiceSessionStatus.ServiceSessionStatusOpen}>
                  {t("filterServiceStatusOpen")}
                </option>
                <option value={ServiceSessionStatus.ServiceSessionStatusClosed}>
                  {t("filterServiceStatusClosed")}
                </option>
              </NativeSelect>
            </FilterField>
            <FilterField label={t("filterAssignee")}>
              <NativeSelect
                value={assignee}
                onChange={(event) => onChange(inboxAssigneeFromParam(event.target.value))}
              >
                <option value="">{t("filterAll")}</option>
                <option value={currentIdentityId}>{t("filterAssigneeMe")}</option>
                <option value={InboxAssigneeFilter.InboxAssigneeFilterUnassigned}>{t("filterAssigneeUnassigned")}</option>
                {coworkers.map((item) => (
                  <option key={item.identityId} value={item.identityId}>
                    {item.type === OrganizationIdentityType.OrganizationIdentityTypeAgent
                      ? t("filterAssigneeAgent", { name: item.displayName })
                      : item.displayName}
                  </option>
                ))}
              </NativeSelect>
            </FilterField>
          </>
        )}
        <FilterField label={t("filterSource")}>
          <NativeSelect
            value={query.source || query.channelId}
            onChange={(event) =>
              // 单聊按来源筛选，其余选项按渠道筛选。
              onChange(
                event.target.value === ServiceSource.ServiceSourceDirect
                  ? { channelId: "", source: ServiceSource.ServiceSourceDirect }
                  : { channelId: event.target.value, source: ServiceSource.$zero },
              )
            }
          >
            <option value="">{t("filterAll")}</option>
            <option value={ServiceSource.ServiceSourceDirect}>{t("filterSourceDirect")}</option>
            {channels.map((channel) => (
              <option key={channel.id} value={channel.id}>
                {channel.enabled
                  ? channel.name
                  : `${channel.name}（${t("filterChannelDisabled")}）`}
              </option>
            ))}
          </NativeSelect>
        </FilterField>
        <FilterField label={t("filterAudience")}>
          <NativeSelect
            value={query.audience}
            onChange={(event) => onChange({ audience: event.target.value as ServiceAudience })}
          >
            <option value="">{t("filterAll")}</option>
            {serviceAudienceOptions.map((option) => (
              <option key={option.value} value={option.value} disabled={!option.available}>
                {t(option.label)}
              </option>
            ))}
          </NativeSelect>
        </FilterField>
        {applied ? (
          <Button
            variant="outline"
            size="sm"
            className="justify-self-start"
            onClick={() =>
              onChange({
                pendingKind: InboxPendingKind.$zero,
                ...inboxQueueFromParam(""),
                channelId: "",
                source: ServiceSource.$zero,
                audience: ServiceAudience.$zero,
                serviceStatus: ServiceSessionStatus.ServiceSessionStatusOpen,
                ...inboxAssigneeFromParam(""),
              })
            }
          >
            {t("filterReset")}
          </Button>
        ) : null}
      </PopoverContent>
    </Popover>
  )
}
