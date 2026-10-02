/** 移动端群主添加任意成员、其他成员添加本人负责的个人 AI 员工，保留选择并返回原群详情。 */
import { groupMemberMaxCount } from "@/features/inbox/group-conversation-schema"
import { useEffect } from "react"
import { useController, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useOutletContext } from "react-router"
import { z } from "zod"

import { addGroupConversationMembers, type MemberOption } from "@/api"
import { type MobileGroupDetailsContext } from "@/apps/mobile/mobile-group-context"
import { MobileGroupMemberPicker } from "@/apps/mobile/mobile-group-member-picker"
import { useMobileBack } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader } from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { Button } from "@/components/ui/button"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { zodResolver } from "@/lib/zod-resolver"

/** 按当前群成员和剩余名额选择成员，成功后刷新群聊事实。 */
export function MobileAddGroupMembersPage() {
  const { t } = useTranslation("mobile")
  const { t: tInbox } = useTranslation("inbox")
  const { group, canManage, archived, busy, onSave } =
    useOutletContext<MobileGroupDetailsContext>()
  const { identity } = useMobileWorkspace()
  const close = useMobileBack(`/chats/group/${group.id}/details`)
  const existingIDs = group.participants.map((member) => member.identityId)
  const remaining = Math.max(0, groupMemberMaxCount - existingIDs.length)
  const schema = z.object({
    members: z
      .array(z.custom<MemberOption>())
      .min(1, tInbox("groupMembersRequired"))
      .max(remaining, tInbox("groupMemberLimitReached")),
  })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { members: [] },
  })
  const { field } = useController({ control: form.control, name: "members" })
  const { mounted, dirty } = useFormLifetime(form.formState.isDirty)
  useEffect(() => {
    // 已从其他端入群的成员退出候选和勾选，其余选择继续保留。
    const selected = field.value.filter(
      (member) =>
        !group.participants.some((participant) => participant.identityId === member.id),
    )
    if (selected.length !== field.value.length) field.onChange(selected)
  }, [field.value, field.onChange, group.participants])
  const notice = archived
    ? t("group.addArchived")
    : remaining === 0
      ? tInbox("groupMemberLimitReached")
      : canManage
        ? null
        : tInbox("groupAddPersonalAgentsDescription")

  /** 批量添加所选成员，离开页面后忽略迟到的返回导航。 */
  async function addMembers(values: z.infer<typeof schema>) {
    if (archived) return
    const success = await onSave(() =>
      addGroupConversationMembers(group.id, {
        memberIdentityIds: values.members.map((member) => member.id),
      }),
    )
    if (!success || !mounted.current) return
    // 同步清空选择，返回前的重新渲染保持无未保存内容。
    form.reset()
    dirty.current = false
    close()
  }

  return (
    <section className="flex h-full min-h-0 flex-col bg-background">
      <MobilePageHeader
        title={t("group.addMembers")}
        backTo={`/chats/group/${group.id}/details`}
        backDisabled={busy}
      />
      <form
        className="min-h-0 flex-1 space-y-9 overflow-y-auto p-4"
        noValidate
        onSubmit={form.handleSubmit(addMembers)}
      >
        <div className="space-y-3">
          {notice ? (
            <p className="text-sm text-muted-foreground" role="status">
              {notice}
            </p>
          ) : null}
          <MobileGroupMemberPicker
            label={t("group.selectMembers")}
            showSelectionSummary={false}
            currentIdentityID={identity.user.identityId}
            excludedIdentityIDs={existingIDs}
            ownPersonalAgentsOnly={!canManage}
            selectionLimit={remaining}
            selected={field.value}
            onChange={field.onChange}
            onBlur={field.onBlur}
            inputRef={field.ref}
            disabled={busy || archived}
          />
        </div>
        <div>
          <Button
            type="submit"
            className="min-h-11 w-full"
            disabled={
              busy || archived ||
              !field.value.length || field.value.length > remaining
            }
          >
            {t("group.complete")}
          </Button>
        </div>
      </form>
    </section>
  )
}
