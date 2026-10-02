/** 联系人档案：阶段、标签与企业自定义字段的展示和即时编辑。 */
import {
  useEffect,
  useRef,
  useState,
  type ComponentType,
  type ReactNode,
  type RefObject,
} from "react"
import { BadgeCheckIcon, PlusIcon, SparklesIcon, XIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  ContactFieldType,
  ContactProfileSource,
  addContactTag,
  isApiError,
  listContactFields,
  listContactTags,
  removeContactTag,
  setContactFieldValue,
  type ContactDetail,
  type ContactFieldData,
  type ContactProfileSourceSession,
} from "@/api"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 档案行组件，需放在 dl 内。 */
type ContactProfileRow = ComponentType<{
  label: string
  children: ReactNode
}>

/** 联系人详情侧栏中的档案行，需放在 dl 内。 */
export function ContactProfileGridRow({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="grid grid-cols-[6rem_minmax(0,1fr)] items-start gap-3">
      <dt className="flex min-h-7 min-w-0 items-center text-sm text-muted-foreground">
        <span className="min-w-0 truncate" title={label}>
          {label}
        </span>
      </dt>
      <dd className="flex min-h-7 min-w-0 items-center text-sm">{children}</dd>
    </div>
  )
}

/** 渲染标签与有值的字段，修改即时保存，AI 写入的项带来源标记，网站同步的项只读；「添加资料」列出尚未填写的字段。 */
export function ContactProfileEditor({
  contact,
  row: Row,
  showStage = false,
}: {
  contact: ContactDetail
  row: ContactProfileRow
  showStage?: boolean
}) {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const fields = useResource(resourceKeys.contactFields(), () =>
    listContactFields(),
  )
  const tags = useResource(resourceKeys.contactTags(), () => listContactTags())
  const [addingFieldID, setAddingFieldID] = useState<string | null>(null)
  // 新添加字段的输入框，在「添加资料」菜单关闭后聚焦。
  const addingInput = useRef<HTMLInputElement | HTMLSelectElement | null>(null)
  const focusAdding = useRef(false)
  const contactId = contact.contact.id
  const values = new Map(
    contact.profile.fields.map((value) => [value.fieldId, value]),
  )
  const assigned = new Set(contact.profile.tags.map((tag) => tag.id))
  const signedTags = new Set(
    contact.profile.tags
      .filter(
        (tag) =>
          tag.source === ContactProfileSource.ContactProfileSourceSignedIdentity,
      )
      .map((tag) => tag.id),
  )
  const definitions = fields.data?.fields ?? []
  const shownFields = definitions.filter(
    (field) => values.has(field.id) || field.id === addingFieldID,
  )
  const unfilledFields = definitions.filter(
    (field) => !values.has(field.id) && field.id !== addingFieldID,
  )

  /** 执行一次档案修改，成功后刷新联系人详情与列表，失败时提示；返回是否成功。 */
  async function mutate(action: () => Promise<void>) {
    try {
      await action()
      await Promise.all([
        invalidate(resourceKeys.contact(contactId)),
        invalidate(resourceKeys.contacts()),
      ])
      return true
    } catch (error) {
      if (!recoverSession(error, navigate)) {
        console.warn("保存客户资料失败", error)
        toast.error(
          isApiError(error)
            ? apiErrorMessage(error, ["value"])
            : t("profile.saveError"),
        )
      }
      return false
    }
  }

  const stage =
    showStage && contact.contact.stage ? (
      <Row label={t("profile.stage")}>
        <span className="min-w-0 truncate">
          {t(`stages.${contact.contact.stage}`)}
        </span>
      </Row>
    ) : null
  if (fields.error || tags.error) {
    return (
      <>
        {stage}
        <div className="py-1 text-xs leading-5 text-muted-foreground">
          {t("profile.loadError")}
        </div>
      </>
    )
  }

  return (
    <>
      {stage}
      <Row label={t("profile.tags")}>
        <div className="flex min-w-0 flex-wrap items-center gap-1 py-0.5">
          {contact.profile.tags.map((tag) => (
            <span
              key={tag.id}
              className="inline-flex h-6 max-w-full items-center gap-0.5 rounded-md bg-muted pr-0.5 pl-2 text-xs"
            >
              {tag.source === ContactProfileSource.ContactProfileSourceAI &&
              tag.sourceSession ? (
                <AISourceButton
                  session={tag.sourceSession}
                  label={t("profile.aiTagged")}
                />
              ) : null}
              {signedTags.has(tag.id) ? (
                <SignedIdentitySourceMark label={t("profile.signedIdentitySynced")} />
              ) : null}
              <span className="min-w-0 truncate">{tag.name}</span>
              {signedTags.has(tag.id) ? (
                <span className="w-1.5 shrink-0" />
              ) : (
                <button
                  type="button"
                  className="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground"
                  aria-label={t("profile.removeTag", { name: tag.name })}
                  title={t("profile.removeTag", { name: tag.name })}
                  onClick={() =>
                    void mutate(() => removeContactTag(contactId, tag.id))
                  }
                >
                  <XIcon className="size-3" />
                </button>
              )}
            </span>
          ))}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={t("profile.editTags")}
                title={t("profile.editTags")}
              >
                <PlusIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="max-h-72 min-w-44">
              {tags.data?.tags.length === 0 ? (
                <DropdownMenuItem disabled>{t("profile.noTags")}</DropdownMenuItem>
              ) : (
                (tags.data?.tags ?? []).map((tag) => (
                  <DropdownMenuCheckboxItem
                    key={tag.id}
                    checked={assigned.has(tag.id)}
                    disabled={signedTags.has(tag.id)}
                    onSelect={(event) => event.preventDefault()}
                    onCheckedChange={(checked) =>
                      void mutate(() =>
                        checked
                          ? addContactTag(contactId, tag.id)
                          : removeContactTag(contactId, tag.id),
                      )
                    }
                  >
                    {tag.name}
                  </DropdownMenuCheckboxItem>
                ))
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </Row>
      {shownFields.map((field) => {
        const stored = values.get(field.id)
        const synced =
          stored?.source === ContactProfileSource.ContactProfileSourceSignedIdentity
        return (
          <Row key={field.id} label={field.name}>
            <div className="flex min-w-0 flex-1 items-center gap-1">
              <ContactFieldValue
                field={field}
                value={stored?.value ?? ""}
                readOnly={synced}
                adding={field.id === addingFieldID}
                inputRef={field.id === addingFieldID ? addingInput : undefined}
                onSave={(value) =>
                  mutate(() =>
                    setContactFieldValue(contactId, field.id, { value }),
                  )
                }
                onDone={() => setAddingFieldID(null)}
              />
              {stored?.source === ContactProfileSource.ContactProfileSourceAI &&
              stored.sourceSession ? (
                <AISourceButton
                  session={stored.sourceSession}
                  label={t("profile.aiFilled")}
                />
              ) : null}
              {synced ? (
                <SignedIdentitySourceMark label={t("profile.signedIdentitySynced")} />
              ) : null}
            </div>
          </Row>
        )
      })}
      {unfilledFields.length > 0 ? (
        <div className="py-1">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="xs" className="-ml-2 text-muted-foreground">
                <PlusIcon />
                {t("profile.addField")}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="start"
              className="max-h-72 min-w-44"
              onCloseAutoFocus={(event) => {
                // 选中字段后焦点交给新输入框，不回到菜单按钮。
                if (!focusAdding.current) return
                focusAdding.current = false
                event.preventDefault()
                // 等新输入框挂载后再聚焦。
                requestAnimationFrame(() => addingInput.current?.focus())
              }}
            >
              {unfilledFields.map((field) => (
                <DropdownMenuItem
                  key={field.id}
                  onSelect={() => {
                    focusAdding.current = true
                    setAddingFieldID(field.id)
                  }}
                >
                  {field.name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      ) : null}
    </>
  )
}

/** AI 来源标记：悬停说明由 AI 根据对话写入，点击打开依据的客服周期所在会话并定位到周期开头。 */
function AISourceButton({
  session,
  label,
}: {
  session: ContactProfileSourceSession
  label: string
}) {
  const navigate = useNavigate()

  return (
    <button
      type="button"
      className="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground"
      aria-label={label}
      title={label}
      onClick={() => {
        // 移动端进入客户会话页并定位消息，Web 与桌面端在收件箱按查询参数打开会话。
        if (resolveAppPlatform() === "mobile") {
          void navigate(`/inbox/customer/${session.conversationId}`, {
            state: {
              mobileBack: true,
              locateMessage: { messageId: session.openingMessageId, nonce: Date.now() },
            },
          })
          return
        }
        void navigate(
          `/inbox?${new URLSearchParams({ conversation: session.conversationId, message: session.openingMessageId }).toString()}`,
        )
      }}
    >
      <SparklesIcon className="size-3" />
    </button>
  )
}

/** 签名身份来源标记：悬停说明由客户登录身份同步。 */
function SignedIdentitySourceMark({ label }: { label: string }) {
  return (
    <span
      role="img"
      className="inline-flex size-5 shrink-0 items-center justify-center text-muted-foreground"
      aria-label={label}
      title={label}
    >
      <BadgeCheckIcon className="size-3" />
    </span>
  )
}

/** 字段值：只读时显示文本（单选显示选项名称）；单选始终是原生下拉，选中即保存；其余类型平时显示文本，点击后原位编辑，回车或失焦保存，Esc 取消，保存失败时保留输入继续编辑。 */
function ContactFieldValue({
  field,
  value,
  readOnly,
  adding,
  inputRef,
  onSave,
  onDone,
}: {
  field: ContactFieldData
  value: string
  readOnly: boolean
  adding: boolean
  inputRef?: RefObject<HTMLInputElement | HTMLSelectElement | null>
  onSave: (value: string) => Promise<boolean>
  onDone: () => void
}) {
  const { t } = useTranslation("contacts")
  const [editing, setEditing] = useState(adding)
  const [draft, setDraft] = useState(value)
  const [pending, setPending] = useState<string | null>(null)
  // 标记本次编辑尚未结束，编辑控件卸载时的失焦不再重复保存或取消。
  const active = useRef(adding)
  const localInput = useRef<HTMLInputElement | null>(null)
  // 点击进入编辑或保存失败后重新聚焦输入框；新添加的字段由菜单关闭后聚焦。
  const focusOnEdit = useRef(false)
  const shown = pending ?? value

  useEffect(() => {
    if (editing && focusOnEdit.current) {
      focusOnEdit.current = false
      localInput.current?.focus()
    }
  }, [editing])

  /** 保存取值，保存期间显示新值；成功返回 true。 */
  async function save(next: string) {
    setPending(next)
    const saved = await onSave(next)
    setPending(null)
    return saved
  }

  /** 结束编辑，取值变化时保存；保存失败时回到编辑状态。 */
  async function commit(next: string) {
    if (!active.current) return
    active.current = false
    setEditing(false)
    const trimmed = next.trim()
    if (trimmed !== value && !(await save(trimmed))) {
      active.current = true
      focusOnEdit.current = true
      setEditing(true)
      return
    }
    onDone()
  }

  /** 放弃本次编辑。 */
  function cancel() {
    if (!active.current) return
    active.current = false
    setEditing(false)
    setDraft(value)
    onDone()
  }

  if (readOnly) {
    return (
      <span className="min-h-7 min-w-0 flex-1 py-1 break-words">
        {field.type === ContactFieldType.ContactFieldTypeSelect
          ? field.options.find((option) => option.id === value)?.name
          : value}
      </span>
    )
  }
  if (field.type === ContactFieldType.ContactFieldTypeSelect) {
    return (
      <select
        ref={inputRef as RefObject<HTMLSelectElement | null> | undefined}
        aria-label={field.name}
        className="-mx-1.5 h-7 w-full min-w-0 cursor-pointer appearance-none truncate rounded-md bg-transparent px-1.5 text-sm outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
        value={shown}
        onChange={(event) => {
          // 保存成功后结束添加，失败时保留该行以便重新选择。
          void save(event.target.value).then((saved) => saved && onDone())
        }}
      >
        <option value="">{t("profile.emptyOption")}</option>
        {field.options.map((option) => (
          <option key={option.id} value={option.id}>
            {option.name}
          </option>
        ))}
      </select>
    )
  }
  if (editing) {
    return (
      <Input
        ref={(element) => {
          localInput.current = element
          if (inputRef) inputRef.current = element
        }}
        aria-label={field.name}
        className="h-7 text-sm"
        type={field.type === ContactFieldType.ContactFieldTypeDate ? "date" : "text"}
        inputMode={
          field.type === ContactFieldType.ContactFieldTypeNumber
            ? "decimal"
            : undefined
        }
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => void commit(draft)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault()
            event.currentTarget.blur()
          } else if (event.key === "Escape") {
            cancel()
          }
        }}
      />
    )
  }
  return (
    <button
      type="button"
      className="-mx-1.5 min-h-7 w-full min-w-0 rounded-md px-1.5 text-left break-words hover:bg-muted"
      title={t("profile.editField", { name: field.name })}
      onClick={() => {
        active.current = true
        focusOnEdit.current = true
        setDraft(value)
        setEditing(true)
      }}
    >
      {shown}
    </button>
  )
}
