/** 移动端客户会话的转交底部面板。 */
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import type { CustomerSessionActions } from "@/features/inbox/customer-session-actions"
import { focusDialogContainer } from "@/lib/dialog-focus"

/** 按同事、团队和公共队列分组列出转交去向，选择后关闭面板并执行转交；转交进行中禁用全部去向，关闭后的焦点由 onCloseAutoFocus 处理。 */
export function MobileCustomerTransferSheet({
  actions,
  open,
  onOpenChange,
  onCloseAutoFocus,
}: {
  actions: CustomerSessionActions
  open: boolean
  onOpenChange: (open: boolean) => void
  onCloseAutoFocus: (event: Event) => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const busy = actions.operation !== ""
  const itemClassName =
    "min-h-11 w-full justify-start rounded-none px-4 text-base font-normal"

  /** 关闭面板后执行选中的转交。 */
  function select(transfer: () => Promise<void>) {
    onOpenChange(false)
    void transfer()
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        aria-describedby={undefined}
        className="max-h-[calc(100dvh-env(safe-area-inset-top)-1rem)] gap-0 rounded-t-2xl pb-[env(safe-area-inset-bottom)]"
        onOpenAutoFocus={focusDialogContainer}
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <SheetHeader className="flex-row items-center border-b">
          <SheetTitle className="flex-1">{t("conversationTransfer")}</SheetTitle>
          <SheetClose asChild>
            <Button variant="ghost" className="min-h-11">
              {t("common:actions.cancel")}
            </Button>
          </SheetClose>
        </SheetHeader>
        <div className="overflow-y-auto py-2">
          {actions.transferCandidates.length > 0 ? (
            <section className="border-b pb-2">
              <h3 className="px-4 pt-2 pb-1 text-xs text-muted-foreground">
                {t("conversationTransferCoworkers")}
              </h3>
              {actions.transferCandidates.map((assignee) => (
                <Button
                  key={assignee.identityId}
                  variant="ghost"
                  className={itemClassName}
                  disabled={busy}
                  onClick={() => select(() => actions.transferToMember(assignee))}
                >
                  <span className="min-w-0 truncate">{assignee.displayName}</span>
                </Button>
              ))}
            </section>
          ) : null}
          {actions.transferTeams.length > 0 ? (
            <section className="border-b py-2">
              <h3 className="px-4 pt-2 pb-1 text-xs text-muted-foreground">
                {t("conversationTransferTeams")}
              </h3>
              {actions.transferTeams.map((team) => (
                <Button
                  key={team.id}
                  variant="ghost"
                  className={itemClassName}
                  disabled={busy || !team.available}
                  onClick={() => select(() => actions.transferToTeam(team))}
                >
                  <span className="min-w-0 flex-1 truncate text-left">{team.name}</span>
                  {team.available ? null : (
                    <span className="text-xs text-muted-foreground">
                      {t("conversationTransferTeamUnavailable")}
                    </span>
                  )}
                </Button>
              ))}
            </section>
          ) : null}
          <div className="pt-2">
            <Button
              variant="ghost"
              className={itemClassName}
              disabled={busy}
              onClick={() => select(actions.transferToPublicQueue)}
            >
              {t("conversationTransferPublicQueue")}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
