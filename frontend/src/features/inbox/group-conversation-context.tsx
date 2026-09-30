/** 群聊侧边面板中的资料编辑和成员管理交互。 */
import { useEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react"
import { MoreHorizontalIcon, PencilIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  addGroupConversationMembers,
  ConversationStatus,
  FilePurpose,
  getGroupConversation,
  GroupParticipantRole,
  isApiError,
  leaveGroupConversation,
  removeGroupConversationMember,
  transferGroupConversationOwner,
  updateGroupConversation,
  type GroupConversationData,
  type GroupConversationProfileInput,
  type MemberOption,
} from "@/api"
import { DetailEditActions } from "@/components/form/detail-edit-row"
import { ImagePicker } from "@/components/image-picker"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { GroupDissolveDialog } from "@/features/inbox/group-dissolve-dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { GroupParticipantList } from "@/features/inbox/group-participant-list"
import { GroupAvatar } from "@/features/inbox/group-avatar"
import { useGroupMute } from "@/features/inbox/use-group-mute"
import {
  SidePanelField,
  SidePanelTab,
  SidePanelTabsList,
} from "@/features/inbox/side-panel-layout"
import { useDateTime } from "@/hooks/use-date-time"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

import {
  createGroupProfileSchema,
  groupTitleMaxLength,
  groupDescriptionMaxLength,
} from "@/features/inbox/group-conversation-schema"
import { zodResolver } from "@/lib/zod-resolver"
import { useForm } from "react-hook-form"
import type { z } from "zod"

/** 群资料字段的编辑入口，宽屏下悬停字段行时显示。 */
function GroupFieldEditButton({ label, onEdit }: { label: string; onEdit: () => void }) {
  const { t } = useTranslation("common")
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      className="opacity-100 sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100"
      aria-label={t("actions.editField", { field: label })}
      title={t("actions.editField", { field: label })}
      onClick={onEdit}
    >
      <PencilIcon />
    </Button>
  )
}

/** 群图片与群主的更多操作；更换图片与名称、描述共用同一保存状态，保存期间不发起新的修改。 */
function GroupImageField({
  group,
  canManage,
  busy,
  saveState,
  moreTrigger,
  onUpdate,
  onDissolve,
}: {
  group: GroupConversationData
  canManage: boolean
  busy: boolean
  saveState: ReturnType<typeof useImmediateSave>
  moreTrigger: RefObject<HTMLButtonElement | null>
  onUpdate: (input: GroupConversationProfileInput) => Promise<void>
  onDissolve: () => void
}) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const image = usePendingImageUpload({
    purpose: FilePurpose.FilePurposeGroupImage,
    onError: (error) => {
      if (recoverSession(error, navigate)) return
      console.warn("上传群聊图片失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["imageFileId"])
          : t("groupImageUploadError"),
      )
    },
  })

  /** 上传并关联新选择的群图片。 */
  async function changeImage(file: File) {
    const request = saveState.begin()
    if (request === null) return
    image.select(file)
    try {
      const imageFileId = await image.ensureUploaded()
      if (!saveState.isCurrent(request)) return
      if (imageFileId === null) {
        image.clear()
        return
      }
      await onUpdate({
        title: group.title,
        description: group.description,
        imageFileId,
      })
      if (!saveState.isCurrent(request)) return
      image.clear()
    } catch (error) {
      if (!saveState.isCurrent(request)) return
      image.clear()
      if (recoverSession(error, navigate)) return
      console.warn("修改群聊图片失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["imageFileId"])
          : t("groupProfileSaveError"),
      )
    } finally {
      saveState.finish(request)
    }
  }

  return (
    <SidePanelField
      label={t("groupImageLabel")}
      action={
        canManage ? (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                ref={moreTrigger}
                variant="ghost"
                size="icon-sm"
                aria-label={t("groupMore")}
                disabled={busy}
              >
                <MoreHorizontalIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem destructive onSelect={onDissolve}>
                {t("groupDissolve")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : undefined
      }
    >
      {canManage ? (
        <ImagePicker
          fallback="group"
          label={t("groupImageChoose")}
          imageURL={image.pending?.previewURL || group.imageUrl}
          className="size-16 rounded-xl"
          disabled={busy}
          loading={saveState.saving && Boolean(image.pending)}
          onSelect={(file) => void changeImage(file)}
        />
      ) : (
        <GroupAvatar imageURL={group.imageUrl} className="size-16 rounded-xl" />
      )}
    </SidePanelField>
  )
}

/** 群名称与描述的逐字段编辑：同步服务端资料，校验后保存单个字段，Esc 放弃、名称按 Enter 提交；不能管理时停止编辑。 */
function useGroupProfileEditor(
  group: GroupConversationData,
  canManage: boolean,
  saveState: ReturnType<typeof useImmediateSave>,
  onUpdate: (input: GroupConversationProfileInput) => Promise<void>,
) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const [editing, setEditing] = useState<"title" | "description" | null>(null)
  // 权限变化或群聊解散后停止资料编辑。
  if (!canManage && editing !== null) setEditing(null)
  const schema = createGroupProfileSchema(t)
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { title: group.title, description: group.description },
  })

  useEffect(() => {
    form.reset({ title: group.title, description: group.description })
  }, [form, group.description, group.title])

  /** 放弃尚未提交的群资料字段。 */
  function cancelEdit() {
    form.reset({ title: group.title, description: group.description })
    setEditing(null)
  }

  /** 校验并保存指定群资料字段，其他资料使用当前服务端值。 */
  async function saveProfileField(field: "title" | "description") {
    // 原生校验需要可用的输入控件，校验通过后再进入保存状态。
    if (!(await form.trigger(field))) return
    const value = form.getValues(field).trim()
    if (value === group[field]) {
      cancelEdit()
      return
    }
    const request = saveState.begin()
    if (request === null) return
    try {
      await onUpdate({
        title: group.title,
        description: group.description,
        [field]: value,
        imageFileId: null,
      })
      if (!saveState.isCurrent(request)) return
      form.setValue(field, value)
      setEditing(null)
    } catch (error) {
      if (!saveState.isCurrent(request) || recoverSession(error, navigate)) return
      console.warn("修改群聊资料失败", { group_id: group.id, field, error })
      cancelEdit()
      toast.error(
        isApiError(error) ? apiErrorMessage(error, [field]) : t("groupProfileSaveError"),
      )
    } finally {
      saveState.finish(request)
    }
  }

  /** 处理群名称编辑快捷键。 */
  function handleTitleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Escape") {
      event.preventDefault()
      event.stopPropagation()
      cancelEdit()
      return
    }
    if (event.key === "Enter") {
      event.preventDefault()
      event.currentTarget.blur()
    }
  }

  /** 处理群描述编辑快捷键。 */
  function handleDescriptionKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Escape") return
    event.preventDefault()
    event.stopPropagation()
    cancelEdit()
  }

  return {
    form,
    editing,
    setEditing,
    cancelEdit,
    saveProfileField,
    handleTitleKeyDown,
    handleDescriptionKeyDown,
  }
}

/** 展示群资料并允许群主修改图片、名称和描述，成员可设置本人的消息免打扰。 */
function GroupConversationProfile({
  group,
  createdAt,
  canManage,
  onUpdate,
}: {
  group: GroupConversationData
  createdAt: string | null
  canManage: boolean
  onUpdate: (input: GroupConversationProfileInput) => Promise<void>
}) {
  const { t } = useTranslation("inbox")
  const { formatFullDateTime } = useDateTime()
  const [dissolveOpen, setDissolveOpen] = useState(false)
  const moreTrigger = useRef<HTMLButtonElement>(null)
  const saveState = useImmediateSave()
  const mute = useGroupMute(group)
  const {
    form,
    editing,
    setEditing,
    cancelEdit,
    saveProfileField,
    handleTitleKeyDown,
    handleDescriptionKeyDown,
  } = useGroupProfileEditor(group, canManage, saveState, onUpdate)
  const owner = group.participants.find(
    (participant) => participant.role === GroupParticipantRole.GroupParticipantRoleOwner,
  )

  // 权限变化或群聊解散后关闭解散确认。
  if (!canManage && dissolveOpen) setDissolveOpen(false)

  const profileBusy = saveState.saving || editing !== null

  return (
    <dl className="space-y-1 text-sm">
      <GroupImageField
        group={group}
        canManage={canManage}
        busy={profileBusy}
        saveState={saveState}
        moreTrigger={moreTrigger}
        onUpdate={onUpdate}
        onDissolve={() => setDissolveOpen(true)}
      />
      <GroupDissolveDialog
        group={group}
        open={dissolveOpen}
        onOpenChange={setDissolveOpen}
        trigger={moreTrigger.current}
      />
      <SidePanelField
        label={t("groupTitleLabel")}
        action={
          canManage && !profileBusy ? (
            <GroupFieldEditButton
              label={t("groupTitleLabel")}
              onEdit={() => {
                form.setValue("title", group.title)
                setEditing("title")
              }}
            />
          ) : undefined
        }
      >
        {editing === "title" ? (
          <Input
            autoFocus
            {...form.register("title")}
            maxLength={groupTitleMaxLength}
            disabled={saveState.saving}
            aria-label={t("groupTitleLabel")}
            onBlur={(event) => {
              void form.register("title").onBlur(event)
              void saveProfileField("title")
            }}
            onKeyDown={handleTitleKeyDown}
          />
        ) : (
          <span className="min-w-0 break-words">
            {group.title || t("groupFieldEmpty")}
          </span>
        )}
      </SidePanelField>
      <SidePanelField
        label={t("groupDescriptionLabel")}
        action={
          canManage && !profileBusy ? (
            <GroupFieldEditButton
              label={t("groupDescriptionLabel")}
              onEdit={() => {
                form.setValue("description", group.description)
                setEditing("description")
              }}
            />
          ) : undefined
        }
      >
        {editing === "description" ? (
          <div className="min-w-0 flex-1">
            <Textarea
              autoFocus
              {...form.register("description")}
              rows={4}
              maxLength={groupDescriptionMaxLength}
              disabled={saveState.saving}
              aria-label={t("groupDescriptionLabel")}
              className="min-h-24 resize-y"
              onKeyDown={handleDescriptionKeyDown}
            />
            <DetailEditActions
              saving={saveState.saving}
              onSave={() => void saveProfileField("description")}
              onCancel={cancelEdit}
            />
          </div>
        ) : (
          <span className="min-w-0 break-words">
            {group.description || t("groupFieldEmpty")}
          </span>
        )}
      </SidePanelField>
      <SidePanelField label={t("groupMute")}>
        <div className="flex min-h-7 items-center">
          <Switch
            checked={mute.muted}
            disabled={mute.saving}
            aria-label={t("groupMute")}
            onCheckedChange={(muted) => void mute.change(muted)}
          />
        </div>
      </SidePanelField>
      <SidePanelField label={t("groupOwner")}>
        <span className="min-w-0 truncate">{owner?.displayName ?? "—"}</span>
      </SidePanelField>
      <SidePanelField label={t("groupCreatedAt")}>
        {createdAt ? formatFullDateTime(createdAt) : "—"}
      </SidePanelField>
    </dl>
  )
}

/** 展示群资料读取状态。 */
function GroupResourceState({
  loading,
  failed,
  onRetry,
}: {
  loading: boolean
  failed: boolean
  onRetry: () => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  if (loading) {
    return (
      <LoadingIndicator className="min-h-48 justify-center">
        {t("groupDetailsLoading")}
      </LoadingIndicator>
    )
  }
  if (!failed) return null
  return (
    <div className="flex min-h-48 flex-col items-center justify-center p-6 text-center">
      <p className="text-sm text-muted-foreground">{t("groupDetailsLoadError")}</p>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="mt-3"
        onClick={onRetry}
      >
        {t("common:actions.retry")}
      </Button>
    </div>
  )
}

/** 在现有侧边面板框架内协调群资料和成员管理状态。 */
export function GroupConversationContext({
  conversationID,
  currentIdentityID,
  onLeft,
}: {
  conversationID: string
  currentIdentityID: string
  onLeft: () => void
}) {
  const { t } = useTranslation("inbox")
  const resource = useResource(resourceKeys.groupConversation(conversationID), () =>
    getGroupConversation(conversationID),
  )
  const invalidate = useResourceInvalidator()
  const group = resource.data
  const currentParticipant = group?.participants.find(
    (participant) => participant.identityId === currentIdentityID,
  )
  const dissolved = group?.status === ConversationStatus.ConversationStatusArchived
  const canManage =
    !dissolved &&
    currentParticipant?.role === GroupParticipantRole.GroupParticipantRoleOwner

  /** 刷新群资料、消息与收件箱，统一采用查询结果。 */
  async function refreshGroup() {
    await Promise.all([
      invalidate(resourceKeys.groupConversation(conversationID), { exact: true }),
      invalidate(resourceKeys.conversationMessages(conversationID)),
      invalidate(resourceKeys.inbox()),
    ])
  }

  /** 修改群资料后刷新服务端事实。 */
  async function updateGroup(input: GroupConversationProfileInput) {
    await updateGroupConversation(conversationID, input)
    await refreshGroup()
  }

  /** 将选中的有效成员加入群聊。 */
  async function addMembers(members: MemberOption[]) {
    await addGroupConversationMembers(conversationID, {
      memberIdentityIds: members.map((member) => member.id),
    })
    await refreshGroup()
  }

  /** 将群主转让给指定成员。 */
  async function transferOwner(identityID: string) {
    await transferGroupConversationOwner(conversationID, {
      ownerIdentityId: identityID,
    })
    await refreshGroup()
  }

  /** 将指定成员移出群聊。 */
  async function removeMember(identityID: string) {
    await removeGroupConversationMember(conversationID, {
      memberIdentityId: identityID,
    })
    await refreshGroup()
  }

  /** 普通成员退出后返回消息列表。 */
  async function leaveGroup() {
    await leaveGroupConversation(conversationID)
    onLeft()
    await Promise.all([
      invalidate(resourceKeys.groupConversation(conversationID), {
        exact: true,
        refetchType: "none",
      }),
      invalidate(resourceKeys.conversationMessages(conversationID), {
        refetchType: "none",
      }),
      invalidate(resourceKeys.inbox()),
    ])
  }

  const failed = Boolean(resource.error) || (!resource.loading && !group)

  return (
    <Tabs key={conversationID} defaultValue="profile" className="min-h-0 flex-1">
      <SidePanelTabsList aria-label={t("contextTabsLabel")}>
        <SidePanelTab value="profile">
          {t("contextGroupProfileTab")}
        </SidePanelTab>
        <SidePanelTab value="members">
          {t("contextGroupMembersTab")}
        </SidePanelTab>
      </SidePanelTabsList>

      <TabsContent
        value="profile"
        className="mt-0 min-h-0 flex-1 overflow-y-auto overscroll-contain p-3"
      >
        {group ? (
          <GroupConversationProfile
            group={group}
            createdAt={group.createdAt}
            canManage={canManage}
            onUpdate={updateGroup}
          />
        ) : (
          <GroupResourceState
            loading={resource.loading}
            failed={failed}
            onRetry={() => void resource.refresh()}
          />
        )}
      </TabsContent>

      <TabsContent value="members" className="mt-0 min-h-0 flex-1 overflow-hidden">
        {group ? (
          <GroupParticipantList
            participants={group.participants}
            currentIdentityID={currentIdentityID}
            canManage={canManage}
            readOnly={dissolved}
            onAdd={addMembers}
            onTransferOwner={transferOwner}
            onRemove={removeMember}
            onLeave={leaveGroup}
          />
        ) : (
          <GroupResourceState
            loading={resource.loading}
            failed={failed}
            onRetry={() => void resource.refresh()}
          />
        )}
      </TabsContent>
    </Tabs>
  )
}
