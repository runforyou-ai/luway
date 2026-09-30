/** 移动端客户会话标题：联系人名称下方显示正在输入或客户语言，需要翻译时点击打开语言设置底部面板。 */
import { useState } from "react"
import { LanguagesIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { Switch } from "@/components/ui/switch"
import { useCustomerLanguage } from "@/features/inbox/customer-language-menu"
import { focusDialogContainer } from "@/lib/dialog-focus"

/** 展示联系人名称和副标题；正在输入优先于客户语言，客户语言存在时整个标题区打开显示原文与回复语言设置，修改立即生效；正在输入期间为读屏保留客户语言说明。 */
export function MobileCustomerConversationTitle({
  name,
  activityLabel,
}: {
  name: string
  activityLabel: string | null
}) {
  const { t } = useTranslation(["inbox", "common"])
  const language = useCustomerLanguage()
  const [open, setOpen] = useState(false)
  if (!language) {
    return (
      <span className="flex w-full min-w-0 flex-col">
        <span className="block w-full truncate text-center leading-6">{name}</span>
        {activityLabel ? (
          <span className="block w-full truncate text-center text-xs leading-4 font-normal text-muted-foreground">
            {activityLabel}
          </span>
        ) : null}
      </span>
    )
  }
  const label = t("translationCustomerLanguage", { language: language.customerLanguage })

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button
          variant="ghost"
          className="flex h-11 w-full min-w-0 flex-col gap-0 px-2 py-0 text-base font-semibold tracking-tight hover:bg-transparent"
        >
          <span className="block w-full truncate text-center leading-6">{name}</span>
          <span className="flex w-full min-w-0 items-center justify-center gap-1 text-xs leading-4 font-normal text-muted-foreground">
            {activityLabel ? (
              <>
                <span className="min-w-0 truncate">{activityLabel}</span>
                <span className="sr-only">{label}</span>
              </>
            ) : (
              <>
                <LanguagesIcon className="size-3" />
                <span className="min-w-0 truncate">{language.customerLanguage}</span>
              </>
            )}
          </span>
        </Button>
      </SheetTrigger>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        aria-describedby={undefined}
        className="max-h-[calc(100dvh-env(safe-area-inset-top)-1rem)] gap-0 rounded-t-2xl pb-[env(safe-area-inset-bottom)]"
        onOpenAutoFocus={focusDialogContainer}
      >
        <SheetHeader className="flex-row items-center border-b">
          <SheetTitle className="min-w-0 flex-1 truncate">{label}</SheetTitle>
          <SheetClose asChild>
            <Button variant="ghost" className="min-h-11">
              {t("common:actions.close")}
            </Button>
          </SheetClose>
        </SheetHeader>
        <div className="space-y-4 overflow-y-auto p-4">
          <div className="flex min-h-11 items-center justify-between gap-3 text-sm">
            <label className="flex min-h-11 flex-1 items-center" htmlFor="mobile-customer-show-original">
              {t("translationShowAllOriginal")}
            </label>
            <Switch
              id="mobile-customer-show-original"
              className="relative h-7 w-12 border-0 px-0.5 after:absolute after:inset-x-0 after:-inset-y-2 after:content-[''] [&_[data-slot=switch-thumb]]:size-6 [&_[data-slot=switch-thumb][data-state=checked]]:translate-x-5"
              checked={language.showOriginal}
              onCheckedChange={language.setShowOriginal}
            />
          </div>
          <div className="space-y-2">
            <label className="block text-sm font-medium" htmlFor="mobile-customer-reply-language">
              {t("translationReplyLanguageLabel")}
            </label>
            <select
              id="mobile-customer-reply-language"
              className="h-11 w-full rounded-md border bg-background px-3 text-sm"
              value={language.replyLanguage}
              onChange={(event) => void language.selectReplyLanguage(event.target.value)}
            >
              <option value="">{t("translationReplyLanguageAuto")}</option>
              {language.replyLanguageOptions.map((option) => (
                <option key={option} value={option}>
                  {language.languageName(option)}
                </option>
              ))}
            </select>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
