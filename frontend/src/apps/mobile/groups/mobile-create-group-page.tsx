/** 移动端群图片、名称、描述、初始成员表单和创建后的聊天导航。 */
import { useController, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate } from "react-router"
import { z } from "zod"
import {
  createGroupConversationSchema,
  groupDescriptionMaxLength,
  groupTitleMaxLength,
} from "@/features/inbox/group/group-conversation-schema"

import { createGroupConversation, type MemberOption } from "@/api"
import { MobileGroupMemberPicker } from "@/apps/mobile/groups/mobile-group-member-picker"
import { useMobileNavigation } from "@/apps/mobile/shared/mobile-navigation"
import { MobilePageHeader } from "@/apps/mobile/shared/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/shared/mobile-workspace-layout"
import { ImagePicker } from "@/components/image-picker"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useGroupImageUpload } from "@/features/inbox/group/use-group-image-upload"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 创建群聊并替换表单路由，保留原消息列表的返回来源。 */
export function MobileCreateGroupPage() {
  const { t } = useTranslation("mobile")
  const { t: tInbox } = useTranslation("inbox")
  const { identity } = useMobileWorkspace()
  const { chatsURL } = useMobileNavigation()
  const location = useLocation()
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const reportError = useRequestErrorReporter()
  const image = useGroupImageUpload("移动端上传群图片")

  // 已选成员作为表单值保存，候选刷新后仍可移除。
  const schema = createGroupConversationSchema(tInbox, z.custom<MemberOption>())
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { title: "", description: "", members: [] },
  })
  const { field } = useController({
    control: form.control,
    name: "members",
  })
  const { dirty } = useFormLifetime(
    form.formState.isDirty || image.pending !== null,
  )

  const creation = useImmediateSave()
  const saving = creation.saving

  /** 上传所选群图片后提交群资料与初始成员，保存中拒绝重复提交，失败时保留表单，离开页面后停止创建并忽略返回结果。 */
  async function submit(values: z.infer<typeof schema>) {
    const request = creation.begin()
    if (request === null) return
    try {
      // 上传失败由图片 Hook 提示，保留候选供再次提交时重试。
      const imageFileId = await image.ensureUploaded()
      if (!creation.isCurrent(request) || imageFileId === null) return
      const conversation = await createGroupConversation({
        title: values.title,
        description: values.description,
        imageFileId,
        memberIdentityIds: values.members.map((member) => member.id),
      })
      if (!creation.isCurrent(request)) return
      void invalidate(resourceKeys.inbox())
      image.clear()
      dirty.current = false
      void navigate(`/chats/group/${conversation.id}`, {
        replace: true,
        state: {
          mobileBack: Boolean(
            (location.state as { mobileBack?: boolean } | null)?.mobileBack,
          ),
        },
      })
    } catch (error) {
      if (!creation.isCurrent(request)) return
      reportError(error, {
        log: "移动端创建群聊",
        fallback: tInbox("groupCreateError"),
        fields: ["title", "description", "imageFileId", "memberIdentityIds"],
      })
    } finally {
      creation.finish(request)
    }
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("group.create")} backTo={chatsURL} />
      <form
        className="app-form min-h-0 flex-1 space-y-9 overflow-y-auto overscroll-contain p-4"
        noValidate
        onSubmit={form.handleSubmit(submit)}
      >
        <FieldGroup>
          <Field>
            <span className="text-sm font-medium">{tInbox("groupImageLabel")}</span>
            <div className="flex items-center gap-3">
              <ImagePicker
                fallback="group"
                label={tInbox("groupImageChoose")}
                imageURL={image.pending?.previewURL}
                className="size-16"
                disabled={saving}
                loading={image.pending?.status === "uploading"}
                onSelect={image.select}
              />
              {image.pending ? (
                <Button
                  type="button"
                  variant="outline"
                  className="min-h-11"
                  disabled={saving}
                  onClick={() => image.clear()}
                >
                  {tInbox("groupImageDiscard")}
                </Button>
              ) : null}
            </div>
          </Field>
          <Field>
            <FieldLabel htmlFor="mobile-group-title">
              {tInbox("groupTitleLabel")}
            </FieldLabel>
            <Input
              {...form.register("title")}
              id="mobile-group-title"
              className="min-h-11 md:text-base"
              autoComplete="off"
              maxLength={groupTitleMaxLength}
              disabled={saving}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="mobile-group-description">
              {tInbox("groupDescriptionLabel")}
            </FieldLabel>
            <Textarea
              {...form.register("description")}
              id="mobile-group-description"
              rows={3}
              maxLength={groupDescriptionMaxLength}
              className="min-h-20 md:text-base"
              disabled={saving}
            />
          </Field>
          <MobileGroupMemberPicker
            currentIdentityID={identity.user.identityId}
            selected={field.value}
            onChange={field.onChange}
            onBlur={field.onBlur}
            inputRef={field.ref}
            disabled={saving}
          />
        </FieldGroup>
        <div>
          <Button
            type="submit"
            className="min-h-11 w-full"
            disabled={saving || field.value.length === 0}
          >
            {t("group.complete")}
          </Button>
        </div>
      </form>
    </section>
  )
}
