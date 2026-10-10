/** 移动端群头像、名称和描述的独立编辑页。 */
import { useMutation } from "@tanstack/react-query"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Navigate, useNavigate, useOutletContext, useParams } from "react-router"
import { toast } from "sonner"
import { z } from "zod"
import { ConversationStatus, FilePurpose, updateGroupConversation } from "@/api"
import type { MobileGroupDetailsContext } from "@/apps/mobile/groups/mobile-group-context"
import { useMobileBack } from "@/apps/mobile/shared/mobile-navigation"
import { MobilePageHeader } from "@/apps/mobile/shared/mobile-page"
import { ImagePicker } from "@/components/image-picker"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  groupProfileSchema,
  groupTitleMaxLength,
  groupDescriptionMaxLength,
} from "@/features/inbox/group/group-conversation-schema"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 按字段隔离表单，拒绝无效字段和失去群主资格的编辑入口。 */
export function MobileGroupProfileEditor() {
  const { t } = useTranslation("mobile")
  const context = useOutletContext<MobileGroupDetailsContext>()
  const { field } = useParams()
  if (field !== "image" && field !== "title" && field !== "description")
    return <Navigate to={`/chats/group/${context.group.id}/details`} replace />
  if (!context.canManage)
    return (
      <section className="flex h-full min-h-0 flex-col bg-background">
        <MobilePageHeader
          title={t("group.profile")}
          backTo={`/chats/group/${context.group.id}/details`}
        />
        <p className="p-4 text-sm text-muted-foreground" role="status">
          {t(
            context.group.status === ConversationStatus.Archived
              ? "group.editArchived"
              : "group.editOwnerOnly",
          )}
        </p>
      </section>
    )
  return <MobileGroupFieldEditor key={field} field={field} {...context} />
}

/** 只编辑选中的资料项，其他字段采用当前服务端资料。 */
function MobileGroupFieldEditor({
  group,
  field,
  busy,
  onSave,
}: MobileGroupDetailsContext & { field: "image" | "title" | "description" }) {
  const { t } = useTranslation("inbox")
  const { t: tm } = useTranslation("mobile")
  const navigate = useNavigate()
  const close = useMobileBack(`/chats/group/${group.id}/details`)
  const image = usePendingImageUpload({
    purpose: FilePurpose.GroupImage,
    onError: (error) => {
      if (recoverSession(error, navigate)) return
      console.warn("移动端上传群图片失败", error)
      toast.error(t("groupImageUploadError"))
    },
  })
  const label = t(
    field === "image"
      ? "groupImageLabel"
      : field === "title"
        ? "groupTitleLabel"
        : "groupDescriptionLabel",
  )
  const schema = z.object({
    value:
      field === "title" ? groupProfileSchema.shape.title : groupProfileSchema.shape.description,
  })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { value: field === "image" ? "" : group[field] },
  })
  const { mounted, dirty } = useFormLifetime(
    form.formState.isDirty || image.pending !== null,
  )

  const update = useMutation({
    mutationFn: async (values: z.infer<typeof schema>) => {
      // 上传失败由图片 Hook 提示，保留候选供再次保存时重试；离开编辑页后停止保存。
      const imageFileID =
        field === "image" ? await image.ensureUploaded() : null
      if (!mounted.current || (field === "image" && !imageFileID)) return false
      return onSave(() =>
        updateGroupConversation(group.id, {
          title: field === "title" ? values.value : group.title,
          description: field === "description" ? values.value : group.description,
          imageFileId: field === "image" ? imageFileID : null,
        }),
      )
    },
  })

  /** 保存当前字段，成功后返回群详情。 */
  function save(values: z.infer<typeof schema>) {
    update.mutate(values, {
      onSuccess: (success) => {
        if (!success) return
        // 同步重置表单和图片候选，返回前的重新渲染保持无未保存内容。
        form.reset(values)
        image.clear()
        dirty.current = false
        close()
      },
    })
  }

  const uploading = image.pending?.status === "uploading"
  const disabled = busy || update.isPending || uploading
  return (
    <section className="flex h-full min-h-0 flex-col bg-background">
      <MobilePageHeader
        title={label}
        backTo={`/chats/group/${group.id}/details`}
        backDisabled={busy}
      />
      <form
        className="app-form min-h-0 flex-1 space-y-9 overflow-y-auto p-4"
        noValidate
        onSubmit={form.handleSubmit(save)}
      >
        <Field>
          {field === "image" ? (
            <div className="flex flex-col items-center gap-2 text-center">
              <ImagePicker
                fallback="group"
                label={t("groupImageChoose")}
                imageURL={image.pending?.previewURL || group.imageUrl}
                className="size-24"
                disabled={disabled}
                loading={uploading}
                onSelect={image.select}
              />
              <p className="text-xs text-muted-foreground">{tm("group.changeImage")}</p>
            </div>
          ) : (
            <>
              <FieldLabel htmlFor="mobile-edit-group-value">
                {label}
              </FieldLabel>
              {field === "title" ? (
                <Input
                  {...form.register("value")}
                  id="mobile-edit-group-value"
                  maxLength={groupTitleMaxLength}
                  disabled={disabled}
                  className="min-h-11 md:text-base"
                />
              ) : (
                <Textarea
                  {...form.register("value")}
                  id="mobile-edit-group-value"
                  maxLength={groupDescriptionMaxLength}
                  rows={6}
                  disabled={disabled}
                  className="md:text-base"
                />
              )}
            </>
          )}
        </Field>
        <div className="w-full">
          <Button
            type="submit"
            className="min-h-11 w-full"
            disabled={disabled || (field === "image" && !image.pending)}
          >
            {tm("group.complete")}
          </Button>
        </div>
      </form>
    </section>
  )
}
