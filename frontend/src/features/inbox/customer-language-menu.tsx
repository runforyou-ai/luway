/** 客户会话的客户语言入口：切换整段显示原文，锁定或恢复自动识别的回复语言。 */
import { LanguagesIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { isApiError } from "@/api"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { apiErrorMessage } from "@/lib/form-errors"
import { languageDisplayName, translationLanguages } from "@/lib/languages"

import { useCustomerTranslation } from "./customer-translation"

/** 返回客户语言入口的展示状态与操作；客户语言已识别且与本人语言相同、且未锁定回复语言时返回 null。 */
export function useCustomerLanguage() {
  const { t, i18n } = useTranslation("inbox")
  const translation = useCustomerTranslation()
  if (!translation || !(translation.replyNeedsTranslation || translation.state.replyLanguageLocked)) return null
  const { state } = translation
  const locked = state.replyLanguageLocked
  const customerLanguage = state.customerLanguage
    ? languageDisplayName(state.customerLanguage, i18n.language)
    : t("translationCustomerLanguageUnknown")

  return {
    /** 客户语言显示名，未识别时为提示文案。 */
    customerLanguage,
    showOriginal: translation.showOriginal,
    setShowOriginal: translation.setShowOriginal,
    /** 已锁定的回复语言代码，自动识别时为空字符串。 */
    replyLanguage: locked ? state.customerLanguage : "",
    // 已锁定的语言不在常用列表中时补到列表首位。
    replyLanguageOptions: locked && !translationLanguages.includes(state.customerLanguage)
      ? [state.customerLanguage, ...translationLanguages]
      : translationLanguages,
    /** 语言代码的显示名。 */
    languageName: (language: string) => languageDisplayName(language, i18n.language),
    /** 保存回复语言，空字符串恢复自动识别；失败时提示。 */
    async selectReplyLanguage(language: string) {
      try {
        await translation.setReplyLanguage(language)
      } catch (error) {
        console.warn("修改回复语言失败", error)
        toast.error(isApiError(error) ? apiErrorMessage(error) : t("translationReplyLanguageError"))
      }
    },
  }
}

/** 会话头标题旁的客户语言标签，点击打开显示原文与回复语言菜单。 */
export function CustomerLanguageChip() {
  const { t } = useTranslation("inbox")
  const language = useCustomerLanguage()
  if (!language) return null
  const label = t("translationCustomerLanguage", { language: language.customerLanguage })
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-6 shrink-0 gap-1 px-1.5 text-xs font-normal text-muted-foreground"
              aria-label={label}
            >
              <LanguagesIcon className="size-3.5" />
              {language.customerLanguage}
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{label}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="start" className="min-w-52">
        <DropdownMenuCheckboxItem
          checked={language.showOriginal}
          onCheckedChange={language.setShowOriginal}
        >
          {t("translationShowAllOriginal")}
        </DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <span className="min-w-0 flex-1 truncate">
              {t("translationReplyLanguage", {
                language: language.replyLanguage
                  ? language.languageName(language.replyLanguage)
                  : t("translationReplyLanguageAuto"),
              })}
            </span>
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="max-h-80 min-w-48">
            <DropdownMenuCheckboxItem
              checked={!language.replyLanguage}
              onCheckedChange={() => void language.selectReplyLanguage("")}
            >
              {t("translationReplyLanguageAuto")}
            </DropdownMenuCheckboxItem>
            <DropdownMenuSeparator />
            {language.replyLanguageOptions.map((option) => (
              <DropdownMenuCheckboxItem
                key={option}
                checked={option === language.replyLanguage}
                onCheckedChange={() => void language.selectReplyLanguage(option)}
              >
                {language.languageName(option)}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
