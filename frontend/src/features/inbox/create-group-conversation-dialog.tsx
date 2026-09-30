/** 企业内部群聊创建表单。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useController, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  createGroupConversation,
  FilePurpose,
  isApiError,
  isGroupInboxConversation,
  type GroupInboxConversationData,
} from "@/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  createGroupConversationSchema,
  groupTitleMaxLength,
  groupDescriptionMaxLength,
  groupAdditionalMemberMaxCount,
} from "@/features/inbox/group-conversation-schema"
import { ImagePicker } from "@/components/image-picker"
import { GroupMemberPicker } from "@/features/inbox/group-member-picker"
import { listChatTargets } from "@/features/inbox/list-all-member-options"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 选择初始成员并创建企业内部群聊。 */
export function CreateGroupConversationDialog({
  open,
  currentIdentityID,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  currentIdentityID: string
  onOpenChange: (open: boolean) => void
  onCreated: (conversation: GroupInboxConversationData) => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [query, setQuery] = useState("")
  const createRequestID = useRef(0)
  const image = usePendingImageUpload({
    purpose: FilePurpose.FilePurposeGroupImage,
    onError: (error) => {
      console.warn("上传群聊图片失败", error)
      if (!recoverSession(error, navigate)) toast.error(t("groupImageUploadError"))
    },
  })
  const pendingImage = image.pending
  const schema = useMemo(() => createGroupConversationSchema(t, z.string()), [t])
  type GroupConversationValues = z.infer<typeof schema>
  const form = useForm<GroupConversationValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { title: "", description: "", members: [] },
  })

  useEffect(() => () => {
    createRequestID.current += 1
  }, [])

  const { field: memberIdentityIDsField } = useController({
    control: form.control,
    name: "members",
  })
  const selectedIdentityIDs = memberIdentityIDsField.value
  const { data, loading, error, refresh } = useResource(
    resourceKeys.chatTargets(),
    listChatTargets,
    { enabled: open, staleTime: 0 },
  )
  const candidates = (data ?? []).filter((member) => member.id !== currentIdentityID)

  /** 创建群聊并关闭表单。 */
  async function create(values: GroupConversationValues) {
    const requestID = ++createRequestID.current
    const imageFileId = await image.ensureUploaded()
    if (imageFileId === null || requestID !== createRequestID.current) return
    try {
      const conversation = await createGroupConversation({
        title: values.title.trim(),
        description: values.description.trim(),
        imageFileId,
        memberIdentityIds: values.members,
      })
      // 关闭表单或离开页面后丢弃迟到结果。
      if (requestID !== createRequestID.current) {
        void invalidate(resourceKeys.inbox())
        return
      }
      if (!isGroupInboxConversation(conversation)) {
        throw new Error("企业群聊响应结构无效")
      }
      onCreated(conversation)
      changeOpen(false)
    } catch (createError) {
      if (requestID !== createRequestID.current) return
      if (recoverSession(createError, navigate)) return
      console.warn("创建企业内部群聊失败", { error: createError })
      toast.error(
        isApiError(createError)
          ? apiErrorMessage(createError, [
              "title",
              "description",
              "imageFileId",
              "memberIdentityIds",
            ])
          : t("groupCreateError"),
      )
    }
  }

  /** 关闭时清空尚未提交的群聊表单。 */
  function changeOpen(nextOpen: boolean) {
    if (!nextOpen) {
      createRequestID.current += 1
      form.reset()
      setQuery("")
      image.clear()
    }
    onOpenChange(nextOpen)
  }

  const { isSubmitting } = form.formState

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent
        className="grid-rows-[auto_minmax(0,1fr)] overflow-hidden sm:max-w-2xl"
        onOpenAutoFocus={(event) => {
          // 打开时聚焦必填的群名称，跳过前面的群头像按钮。
          event.preventDefault()
          form.setFocus("title")
        }}
      >
        <DialogHeader>
          <DialogTitle>{t("groupCreateTitle")}</DialogTitle>
          <DialogDescription>{t("groupCreateDescription")}</DialogDescription>
        </DialogHeader>
        <form
          className="grid min-h-0 grid-rows-[minmax(0,1fr)_auto] gap-9 overflow-hidden"
          onSubmit={form.handleSubmit(create)}
          noValidate
        >
          <div className="grid min-h-0 gap-5 overflow-y-auto pr-1">
            <div className="space-y-1.5">
              <span className="block text-sm font-medium">{t("groupImageLabel")}</span>
              <div className="flex items-center gap-3">
                <ImagePicker
                  fallback="group"
                  label={t("groupImageChoose")}
                  imageURL={pendingImage?.previewURL}
                  disabled={form.formState.isSubmitting}
                  loading={pendingImage?.status === "uploading"}
                  onSelect={image.select}
                />
                {pendingImage ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={form.formState.isSubmitting}
                    onClick={() => image.clear()}
                  >
                    {t("groupImageDiscard")}
                  </Button>
                ) : null}
              </div>
            </div>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="group-title">
                {t("groupTitleLabel")}
              </FieldLabel>
              <Input
                {...form.register("title")}
                id="group-title"
                autoComplete="off"
                maxLength={groupTitleMaxLength}
              />
            </div>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="group-description">
                {t("groupDescriptionLabel")}
              </FieldLabel>
              <Textarea
                {...form.register("description")}
                id="group-description"
                rows={3}
                maxLength={groupDescriptionMaxLength}
                className="min-h-20 resize-y"
              />
            </div>
            <GroupMemberPicker
              label={t("groupMembersLabel")}
              emptyMessage={t("groupMembersEmpty")}
              members={candidates}
              selected={selectedIdentityIDs}
              onChange={memberIdentityIDsField.onChange}
              query={query}
              onQueryChange={setQuery}
              selectionLimit={groupAdditionalMemberMaxCount}
              disabled={isSubmitting}
              required
              showCount
              inputRef={memberIdentityIDsField.ref}
              name={memberIdentityIDsField.name}
              onBlur={memberIdentityIDsField.onBlur}
              loading={loading}
              error={Boolean(error)}
              onRetry={() => void refresh()}
            />
          </div>
          <div className="flex shrink-0 justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={isSubmitting}
              onClick={() => changeOpen(false)}
            >
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
              {t("common:actions.create")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
