/** 把应转人工未转的问题会话加入评测的弹窗：从客户消息中选定应当转人工的提问，默认最后一条。 */
import { useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  addServiceIssueToEvaluation,
  isApiError,
  MessageType,
  ServiceTranscriptSender,
  type ServiceTranscriptMessageData,
} from "@/api"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { cn } from "@/lib/utils"

/** 按 open 打开弹窗，messages 是周期内的对客沟通，只有客户发送的文字消息可选。 */
export function ServiceIssueEvaluationDialog({
  serviceSessionId,
  messages,
  open,
  onOpenChange,
}: {
  serviceSessionId: string
  messages: ServiceTranscriptMessageData[]
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation(["agents", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const questions = messages.filter(
    (message) =>
      message.sender === ServiceTranscriptSender.ServiceTranscriptSenderCustomer &&
      message.type === MessageType.MessageTypeText &&
      message.body.trim(),
  )
  const [selected, setSelected] = useState("")
  const [saving, setSaving] = useState(false)
  const questionId = questions.some((message) => message.id === selected) ? selected : (questions[questions.length - 1]?.id ?? "")

  /** 以选定的客户消息为提问加入评测。 */
  async function add() {
    setSaving(true)
    try {
      await addServiceIssueToEvaluation(serviceSessionId, questionId)
      toast.success(t("performance.issueSheet.evaluationAdded"))
      onOpenChange(false)
      void invalidate(resourceKeys.agentEvaluation())
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("问题会话加入评测失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("performance.issueSheet.evaluationError"))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("performance.issueSheet.addToEvaluation")}</DialogTitle>
          <DialogDescription>{t("performance.issueSheet.evaluationDescription")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-9">
          <fieldset className="max-h-72 space-y-1 overflow-y-auto rounded-lg border p-2 text-sm">
            <legend className="sr-only">{t("performance.issueSheet.evaluationQuestion")}</legend>
            {questions.map((message) => (
              <label
                key={message.id}
                className={cn("flex cursor-pointer items-start gap-2 rounded-md px-2 py-1.5", message.id === questionId && "bg-muted")}
              >
                <input
                  type="radio"
                  name="evaluation-question"
                  className="mt-1 size-4 shrink-0 accent-primary"
                  checked={message.id === questionId}
                  disabled={saving}
                  onChange={() => setSelected(message.id)}
                />
                <span className="min-w-0 break-words whitespace-pre-wrap">{message.body}</span>
              </label>
            ))}
          </fieldset>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="button" disabled={saving || !questionId} onClick={() => void add()}>
              {saving ? <LoaderCircleIcon className="animate-spin" /> : null}
              {t("common:actions.confirm")}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
