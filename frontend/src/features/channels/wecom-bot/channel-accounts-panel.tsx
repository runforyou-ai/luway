/** 服务员工渠道的「成员」页签：列出与机器人对话过的外部账号，管理员绑定、更换或解除成员。 */
import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  bindChannelAccount,
  listAllMemberOptions,
  listChannelAccounts,
  WorkspaceIdentityType,
  unbindChannelAccount,
  type ChannelAccount,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { FormActions } from "@/components/form/form-actions"
import { ResourceContent } from "@/components/resource-content"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

/** 外部账号的展示名称：平台给出的名称，没有时为平台编号。 */
function accountName(account: ChannelAccount) {
  return account.displayName ?? account.externalId
}

/** 列出渠道外部账号并承载绑定与解除绑定。 */
export function ChannelAccountsPanel({ channelID }: { channelID: string }) {
  const { t } = useTranslation(["channels", "common"])
  const { formatDateTime } = useDateTime()
  const accounts = useResource(resourceKeys.channelAccounts(channelID), (signal) =>
    listChannelAccounts(channelID, signal),
  )
  const [binding, setBinding] = useState<ChannelAccount | null>(null)
  const unbind = useConfirmedAction<ChannelAccount>({
    action: (account) => unbindChannelAccount(channelID, account.id),
    invalidateKeys: () => [resourceKeys.channelAccounts(channelID)],
    successMessage: () => t("accounts.unlinked"),
    errorMessage: () => t("accounts.unbindError"),
    logLabel: "解除渠道外部账号绑定",
  })

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("accounts.description")}</p>
      <ResourceContent resources={[accounts]} errorMessage={t("accounts.loadError")}>
        <ResourceTable
          columns={[
            {
              key: "account",
              header: t("accounts.member"),
              cellClassName: "min-w-0",
              cell: (account) => (
                <ResourceRowIdentity
                  avatar={
                    account.memberIdentityId
                      ? { imageURL: account.memberAvatarUrl, name: account.memberName }
                      : { fallback: "person" }
                  }
                  name={account.memberIdentityId ? account.memberName : t("accounts.unbound")}
                  secondary={accountName(account)}
                />
              ),
            },
            {
              key: "time",
              header: "",
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (account) =>
                account.lastSeenAt
                  ? t("accounts.lastSeen", { time: formatDateTime(account.lastSeenAt) })
                  : t("common:time.addedAt", { time: formatDateTime(account.createdAt) }),
            },
          ]}
          rows={accounts.data ?? []}
          rowKey={(account) => account.id}
          empty={t("accounts.empty")}
          rowActions={(account) => [
            {
              key: "bind",
              label: account.memberIdentityId ? t("accounts.rebind") : t("accounts.bind"),
              onSelect: () => setBinding(account),
            },
            ...(account.memberIdentityId
              ? [
                  {
                    key: "unbind",
                    label: t("accounts.unbind"),
                    destructive: true,
                    separatorBefore: true,
                    onSelect: () => unbind.select(account),
                  },
                ]
              : []),
          ]}
        />
      </ResourceContent>
      <BindAccountDialog channelID={channelID} account={binding} onClose={() => setBinding(null)} />
      <ConfirmationDialog
        {...unbind.dialog}
        destructive
        title={t("accounts.unbindTitle", { name: unbind.item ? accountName(unbind.item) : "" })}
        description={t("accounts.unbindDescription")}
      />
    </div>
  )
}

/** 选择成员并绑定外部账号的弹窗。 */
function BindAccountDialog({
  channelID,
  account,
  onClose,
}: {
  channelID: string
  account: ChannelAccount | null
  onClose: () => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const invalidate = useResourceInvalidator()
  const reportError = useRequestErrorReporter()
  const members = useResource(resourceKeys.memberOptions(), listAllMemberOptions, {
    enabled: account !== null,
  })
  const [memberID, setMemberID] = useState("")
  // 绑定成功后刷新外部账号列表并关闭弹窗。
  const binding = useMutation({
    mutationFn: ({ accountID, memberIdentityID }: { accountID: string; memberIdentityID: string }) =>
      bindChannelAccount(channelID, accountID, { memberIdentityId: memberIdentityID }),
    onSuccess: () => void invalidate(resourceKeys.channelAccounts(channelID)),
  })
  const saving = binding.isPending
  const users = (members.data ?? []).filter(
    (member) => member.type === WorkspaceIdentityType.User,
  )
  const selected = memberID || account?.memberIdentityId || ""

  /** 绑定所选成员，成功后提示并关闭弹窗。 */
  function save() {
    if (!account || !selected || saving) return
    binding.mutate(
      { accountID: account.id, memberIdentityID: selected },
      {
        onSuccess: () => {
          toast.success(t("accounts.linked"))
          setMemberID("")
          onClose()
        },
        onError: (error) =>
          reportError(error, { log: "绑定渠道外部账号", context: { channel_id: channelID }, fallback: t("accounts.bindError") }),
      },
    )
  }

  return (
    <Dialog
      open={account !== null}
      onOpenChange={(open) => {
        if (open) return
        setMemberID("")
        onClose()
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("accounts.bindTitle")}</DialogTitle>
          <DialogDescription>
            {t("accounts.bindDescription", { name: account ? accountName(account) : "" })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-9"
          onSubmit={(event) => {
            event.preventDefault()
            save()
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="channel-account-member" required>
                {t("accounts.member")}
              </FieldLabel>
              <NativeSelect
                id="channel-account-member"
                required
                value={selected}
                disabled={saving || !members.data}
                onChange={(event) => setMemberID(event.target.value)}
              >
                <option value="" disabled />
                {users.map((member) => (
                  <option key={member.id} value={member.id}>
                    {member.displayName}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          </FieldGroup>
          <FormActions saving={saving} disabled={!selected} onCancel={onClose} />
        </form>
      </DialogContent>
    </Dialog>
  )
}
