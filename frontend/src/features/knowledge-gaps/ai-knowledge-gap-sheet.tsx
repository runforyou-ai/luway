/** 待补知识处理侧栏：展示来源对话，把 AI 起草的问答编辑后加入知识库，或忽略该条目。 */
import { useTranslation } from "react-i18next"

import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { useUnsavedChangesContext } from "@/contexts/unsaved-changes-context"

import {
  KnowledgeGapContent,
  useKnowledgeGapResources,
  useKnowledgeGapSummary,
} from "./knowledge-gap-detail"

/** 按条目编号打开侧栏；gapId 为空时关闭，处理完成后由 onHandled 决定下一条。 */
export function AIKnowledgeGapSheet({
  gapId,
  onClose,
  onHandled,
}: {
  gapId: string
  onClose: () => void
  onHandled: () => void
}) {
  const { t } = useTranslation("agents")
  const unsavedChanges = useUnsavedChangesContext()
  const resources = useKnowledgeGapResources(gapId)
  const summary = useKnowledgeGapSummary(resources.data)

  return (
    <Sheet
      open={Boolean(gapId)}
      onOpenChange={async (open) => {
        // 关闭侧栏会丢弃表单，有未保存内容时先确认。
        if (open || (unsavedChanges && !(await unsavedChanges.confirmDiscard()))) return
        onClose()
      }}
    >
      <SheetContent className="w-full gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle>{t("performance.gapSheet.title")}</SheetTitle>
          <SheetDescription>{summary || null}</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="p-6">
            <KnowledgeGapContent resources={resources} onHandled={onHandled} />
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}
