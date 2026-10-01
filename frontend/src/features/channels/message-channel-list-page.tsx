/** 渠道列表页：列出已接入的渠道，添加时选择平台。 */
import { useMemo, useState } from "react"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate, useSearchParams } from "react-router"

import {
  activateMessageChannel,
  deactivateMessageChannel,
  listMessageChannels,
  type MessageChannelSummary,
} from "@/api"
import {
  ListToolbar,
  ListToolbarFilter,
  ListToolbarReset,
} from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import { MessageChannelTypeDialog } from "@/features/channels/message-channel-type-dialog"
import { messageChannelTypeDefinition } from "@/lib/message-channel-types"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useResource } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"
import { useReturnLink } from "@/hooks/use-return-to"

type ChannelEnabledStatus = "enabled" | "disabled"

/** 加载并管理已接入的渠道列表。 */
export function MessageChannelListPage() {
  const { t } = useTranslation(["channels", "common"])
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const [choosingType, setChoosingType] = useState(false)
  const [searchParams, setSearchParams] = useSearchParams()
  const returnLink = useReturnLink()
  // 状态筛选记在 URL 中，从编辑页返回时保留。
  const enabledStatus: ChannelEnabledStatus =
    searchParams.get("status") === "disabled" ? "disabled" : "enabled"
  const setEnabledStatus = (value: ChannelEnabledStatus) =>
    setSearchParams(value === "enabled" ? {} : { status: value }, {
      replace: true,
    })
  const resource = useResource(resourceKeys.messageChannels(), () => listMessageChannels())
  const { data } = resource
  const channels = useMemo(() => data ?? [], [data])

  const filteredChannels = useMemo(
    () =>
      channels.filter(
        (channel) => channel.enabled === (enabledStatus === "enabled"),
      ),
    [channels, enabledStatus],
  )

  const statusChange = useConfirmedAction<MessageChannelSummary>({
    action: (channel) =>
      channel.enabled
        ? deactivateMessageChannel(channel.id)
        : activateMessageChannel(channel.id),
    invalidateKeys: () => [
      resourceKeys.messageChannels(),
      resourceKeys.channelOptions(),
    ],
    logLabel: "切换消息渠道状态",
    errorMessage: () => t("list.statusUpdateError"),
  })

  return (
    <div className="flex min-h-0 w-full flex-1 flex-col overflow-hidden">
      <PageHeader title={t("list.title")} description={t("list.description")}>
        <Button
          variant="subtle"
          size="icon-sm"
          aria-label={t("list.create")}
          title={t("list.create")}
          onClick={() => setChoosingType(true)}
        >
          <PlusIcon />
        </Button>
      </PageHeader>

      <ListToolbar>
        <ListToolbarFilter
          label={t("filters.status")}
          value={enabledStatus}
          options={[
            { value: "enabled", label: t("statuses.enabled") },
            { value: "disabled", label: t("statuses.disabled") },
          ]}
          onValueChange={(value) =>
            setEnabledStatus(value as ChannelEnabledStatus)
          }
        />
        {enabledStatus !== "enabled" ? (
          <ListToolbarReset onClick={() => setEnabledStatus("enabled")}>
            {t("common:actions.clearFilters")}
          </ListToolbarReset>
        ) : null}
      </ListToolbar>

      <ResourceListLayout
        resources={resource}
        errorMessage={t("list.loadError")}
      >
        <ResourceTable
          columns={[
            {
              key: "name",
              header: t("list.columns.name"),
              cellClassName: "min-w-44 font-medium",
              cell: (channel) => {
                const definition = messageChannelTypeDefinition(channel.type)
                return (
                  <div className="flex items-center gap-2.5">
                    <span
                      aria-hidden="true"
                      className={cn(
                        "flex size-7 shrink-0 items-center justify-center rounded-lg",
                        definition?.softClassName ?? "bg-muted text-muted-foreground",
                      )}
                    >
                      {definition ? <definition.icon className="size-4" /> : null}
                    </span>
                    <span className="truncate">
                      {channel.name}
                      {definition ? (
                        <span className="font-normal text-muted-foreground">
                          {" · "}
                          {t(`types.${definition.translationKey}`)}
                        </span>
                      ) : null}
                    </span>
                  </div>
                )
              },
            },
            {
              key: "time",
              header: t("common:time.addedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (channel) =>
                t("common:time.addedAt", { time: formatDateTime(channel.createdAt) }),
            },
          ]}
          rows={filteredChannels}
          rowKey={(channel) => channel.id}
          empty={
            channels.length === 0
              ? t("list.emptyTitle")
              : t("list.emptyFiltered")
          }
          onRowActivate={(channel) =>
            navigate(returnLink(`/channels/${channel.type}/${channel.id}`))
          }
          rowActions={(channel) => [
            {
              key: "status",
              label: channel.enabled ? t("list.deactivate") : t("list.activate"),
              disabled:
                statusChange.pending && statusChange.item?.id === channel.id,
              destructive: channel.enabled,
              separatorBefore: channel.enabled,
              onSelect: () => statusChange.select(channel),
            },
          ]}
        />
      </ResourceListLayout>

      <MessageChannelTypeDialog open={choosingType} onOpenChange={setChoosingType} />
      <ConfirmationDialog
        {...statusChange.dialog}
        title={
          statusChange.item
            ? t(
                statusChange.item.enabled
                  ? "deactivation.title"
                  : "activation.title",
                { name: statusChange.item.name },
              )
            : ""
        }
        description={
          statusChange.item
            ? t(
                statusChange.item.enabled
                  ? "deactivation.description"
                  : "activation.description",
              )
            : ""
        }
        destructive={statusChange.item?.enabled ?? true}
      />
    </div>
  )
}
