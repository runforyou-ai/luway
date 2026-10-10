/** 单聊对象选择器，支持按姓名筛选。 */
import { useRef, useState } from "react"
import { SearchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { WorkspaceIdentityType, type MemberOption } from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { DirectConversationDraftAvatar } from "@/features/inbox/conversation/direct-conversation-draft-header"
import { filterChatTargets, listChatTargets, orderChatTargets } from "@/features/inbox/shared/list-all-member-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 选择一位同事或活跃 AI 员工开始单聊，同事在前，可按姓名筛选；每次打开时清空检索词。 */
export function ConversationTargetPickerDialog({
  open,
  currentIdentityId,
  onOpenChange,
  onSelected,
}: {
  open: boolean
  currentIdentityId: string
  onOpenChange: (open: boolean) => void
  onSelected: (member: MemberOption) => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const { data, loading, error, refresh } = useResource(
    resourceKeys.chatTargets(),
    listChatTargets,
    { enabled: open, staleTime: 0 },
  )
  const searchRef = useRef<HTMLInputElement>(null)
  const [search, setSearch] = useState("")
  const candidates = filterChatTargets(orderChatTargets(data ?? [], currentIdentityId), search)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="grid max-h-[min(42rem,calc(100svh-2rem))] grid-rows-[auto_auto_minmax(0,1fr)] overflow-hidden outline-none"
        onOpenAutoFocus={(event) => {
          // 每次打开清空检索词并聚焦检索框。
          event.preventDefault()
          setSearch("")
          searchRef.current?.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>{t("newDirectConversation")}</DialogTitle>
          <DialogDescription>
            {t("chatPickerDescription")}
          </DialogDescription>
        </DialogHeader>
        <div className="relative">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={searchRef}
            type="search"
            value={search}
            autoComplete="off"
            aria-label={t("chatPickerSearch")}
            className="pl-9"
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        <ScrollArea className="min-h-64 rounded-md border">
          {loading ? (
            <LoadingIndicator className="min-h-64 justify-center">
              {t("chatPickerLoading")}
            </LoadingIndicator>
          ) : error ? (
            <div className="flex min-h-64 flex-col items-center justify-center p-6 text-center">
              <p className="text-sm text-muted-foreground">
                {t("chatPickerLoadError")}
              </p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={() => void refresh()}
              >
                {t("common:actions.retry")}
              </Button>
            </div>
          ) : candidates.length === 0 ? (
            <p className="px-6 py-12 text-center text-sm text-muted-foreground">
              {t(search.trim() ? "membersNoMatches" : "chatPickerEmpty")}
            </p>
          ) : (
            <div className="grid p-1.5">
              {candidates.map((member) => (
                <button
                  key={member.id}
                  type="button"
                  className="flex items-center gap-3 rounded-md px-3 py-2.5 text-left transition-colors hover:bg-muted"
                  onClick={() => {
                    onSelected(member)
                    onOpenChange(false)
                  }}
                >
                  <DirectConversationDraftAvatar
                    member={member}
                    className="size-9"
                  />
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">
                    {member.displayName}
                  </span>
                  {member.type === WorkspaceIdentityType.Agent ? (
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {t(member.personal ? "chatPickerPersonalAgent" : "chatPickerAgent")}
                    </span>
                  ) : null}
                </button>
              ))}
            </div>
          )}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}
