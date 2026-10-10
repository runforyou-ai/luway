/** 移动端群成员预览下方的群资料、聊天记录搜索和个人、群主管理入口。 */
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { GroupParticipantRole, type GroupConversation } from "@/api"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { GroupAvatar } from "@/features/inbox/group/group-avatar"
import { useDateTime } from "@/hooks/use-date-time"

/** 紧凑展示群资料，资料操作、免打扰与归档操作保持统一表单行布局。 */
export function MobileGroupInfo({
  group,
  isOwner,
  archived,
  busy,
  muted,
  muteBusy,
  chatArchived,
  archiveBusy,
  onEdit,
  onTransfer,
  onSearch,
  onFiles,
  onLeave,
  onMute,
  onArchive,
}: {
  group: GroupConversation
  isOwner: boolean
  archived: boolean
  busy: boolean
  muted: boolean
  muteBusy: boolean
  chatArchived: boolean
  archiveBusy: boolean
  onEdit: (field: "image" | "title" | "description") => void
  onTransfer: () => void
  onSearch: () => void
  onFiles: () => void
  onLeave: (trigger: HTMLElement | null) => void
  onMute: (muted: boolean) => void
  onArchive: () => void
}) {
  const { t } = useTranslation("inbox")
  const { t: tm } = useTranslation("mobile")
  const { formatFullDateTime } = useDateTime()
  const owner = group.participants.find(
    (member) => member.role === GroupParticipantRole.Owner,
  )
  const ownerContent = (
    <>
      <span className="w-20 shrink-0 text-muted-foreground">
        {t("groupOwner")}
      </span>
      <span className="min-w-0 flex-1 break-words text-right">
        {owner?.displayName ?? "—"}
      </span>
    </>
  )
  return (
    <div className="px-4 pb-4">
      <div className="divide-y text-sm">
        {(["image", "title", "description"] as const).map((field) => {
          const label = t(
            field === "image"
              ? "groupImageLabel"
              : field === "title"
                ? "groupTitleLabel"
                : "groupDescriptionLabel",
          )
          const content = (
            <>
              <span className="w-20 shrink-0 text-muted-foreground">
                {label}
              </span>
              <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-right">
                {field === "image" ? (
                  <GroupAvatar
                    imageURL={group.imageUrl}
                    className="ml-auto size-12 rounded-xl"
                  />
                ) : (
                  group[field] || t("groupFieldEmpty")
                )}
              </span>
              <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
            </>
          )
          return (
            <button
              key={field}
              type="button"
              disabled={busy}
              onClick={() => onEdit(field)}
              className="flex min-h-14 w-full items-center gap-3 py-3 text-left outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
            >
              {content}
            </button>
          )
        })}
        {isOwner && !archived ? (
          <button
            type="button"
            disabled={busy}
            onClick={onTransfer}
            className="flex min-h-14 w-full items-center gap-3 py-3 text-left outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
          >
            {ownerContent}
            <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
          </button>
        ) : (
          <div className="flex min-h-14 items-center gap-3 py-3">
            {ownerContent}
          </div>
        )}
        <div className="flex min-h-14 items-center gap-3 py-3">
          <span className="w-20 shrink-0 text-muted-foreground">
            {t("groupCreatedAt")}
          </span>
          <span className="min-w-0 flex-1 text-right">
            {formatFullDateTime(group.createdAt)}
          </span>
        </div>
      </div>
      {archived ? (
        <p className="py-3 text-sm text-muted-foreground" role="status">
          {tm("group.archived")}
        </p>
      ) : null}
      <button
        type="button"
        onClick={onSearch}
        className="flex min-h-14 w-full items-center justify-between gap-3 border-t text-left text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
      >
        {t("searchCurrentConversation")}
        <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
      </button>
      <button
        type="button"
        onClick={onFiles}
        className="flex min-h-14 w-full items-center justify-between gap-3 border-t text-left text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
      >
        {t("contextFilesTab")}
        <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
      </button>
      <div className="flex min-h-14 items-center justify-between gap-3 border-t text-sm">
        <label
          className="flex min-h-14 flex-1 items-center"
          htmlFor="mobile-group-muted"
        >
          {t("groupMute")}
        </label>
        <Switch
          id="mobile-group-muted"
          className="relative h-7 w-12 border-0 px-0.5 disabled:opacity-100 after:absolute after:inset-x-0 after:-inset-y-2 after:content-[''] [&_[data-slot=switch-thumb]]:size-6 [&_[data-slot=switch-thumb][data-state=checked]]:translate-x-5"
          checked={muted}
          disabled={muteBusy}
          onCheckedChange={onMute}
        />
      </div>
      <button
        type="button"
        disabled={archiveBusy}
        onClick={onArchive}
        className="flex min-h-14 w-full items-center border-t text-left text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
      >
        {t(chatArchived ? "conversationUnarchive" : "conversationArchive")}
      </button>
      <Button
        type="button"
        variant="destructive"
        className="mt-4 min-h-11 w-full"
        disabled={archived || busy}
        onClick={(event) => onLeave(event.currentTarget)}
      >
        {t(isOwner ? "groupDissolve" : "groupLeave")}
      </Button>
    </div>
  )
}
