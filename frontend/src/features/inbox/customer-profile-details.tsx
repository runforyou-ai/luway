/** 客户会话侧栏按「客户资料」「本次访问」分组展示客户身份、客户档案与当前周期访客上下文。 */
import { useEffect, useRef } from "react"
import { useTranslation } from "react-i18next"

import { getContact, getRequesterProfile, isNotFoundApiError } from "@/api"
import { ContactProfileEditor } from "@/components/contact-profile-editor"
import {
  SidePanelField,
  SidePanelSection,
  type ProfileField,
  type ProfileSection,
} from "@/features/inbox/side-panel-layout"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { openExternalURL } from "@/platform/external-navigation"

/** 展示客户身份、访客上下文与客户档案，两者随会话内容变化重读，加载完成后分组展示，只展示有值的字段；身份验证状态只在网站渠道展示；分组与字段行默认使用侧栏样式。 */
export function CustomerProfileDetails({
  conversationID,
  website,
  field: Field = SidePanelField,
  section: Section = SidePanelSection,
}: {
  conversationID: string
  website: boolean
  field?: ProfileField
  section?: ProfileSection
}) {
  const { t } = useTranslation(["inbox", "contacts"])
  const profile = useResource(
    resourceKeys.requesterProfile(conversationID),
    () => getRequesterProfile(conversationID),
  )
  const data = profile.data?.customer
  const contactId = data?.contactId ?? ""
  const contact = useResource(
    resourceKeys.contact(contactId),
    () => getContact(contactId),
    { enabled: Boolean(contactId) },
  )
  const invalidate = useResourceInvalidator()
  const profileUpdatedAt = useRef(profile.dataUpdatedAt)
  // 发起人资料随会话参与方变化重读后，同步重读该发起人的联系人；首次读取由联系人查询自行完成。
  useEffect(() => {
    const previous = profileUpdatedAt.current
    profileUpdatedAt.current = profile.dataUpdatedAt
    if (previous === 0 || previous === profile.dataUpdatedAt || !contactId) return
    void invalidate(resourceKeys.contact(contactId))
  }, [profile.dataUpdatedAt, contactId, invalidate])
  if (profile.error) {
    return (
      <p className="mt-5 text-xs leading-5 text-muted-foreground">
        {t("contextProfileLoadError")}
      </p>
    )
  }
  if (!data || (!contact.data && !contact.error)) return null
  // 联系人已移入回收站时不展示档案，包括其他页面缓存的旧档案；其他读取失败时在档案位置提示。
  const contactRemoved = isNotFoundApiError(contact.error)
  const contactData = contactRemoved ? undefined : contact.data
  const contactFailed = Boolean(contact.error && !contactRemoved)
  const visit = data.visit
  // 设备类型、浏览器与操作系统合为一行。
  const deviceTypes: Record<string, string> = {
    desktop: t("contextDevice_desktop"),
    mobile: t("contextDevice_mobile"),
    tablet: t("contextDevice_tablet"),
  }
  const device = visit
    ? [deviceTypes[visit.deviceType] ?? "", visit.browser, visit.os]
        .filter(Boolean)
        .join(" · ")
    : ""
  const hasCustomer = Boolean(
    website ||
      data.externalUserId ||
      data.email ||
      contactData ||
      contactFailed,
  )
  const hasVisit = Boolean(
    visit &&
    (visit.pageUrl ||
      visit.referrerUrl ||
      device ||
      visit.language ||
      visit.timeZone ||
      visit.country),
  )

  return (
    <>
      {hasCustomer ? (
        <Section title={t("contacts:profile.title")}>
          {website ? (
            <Field label={t("contextVerification")}>
              {data.identityVerified
                ? t("contextVerified")
                : t("contextUnverified")}
            </Field>
          ) : null}
          {data.externalUserId ? (
            <Field label={t("contextExternalUserId")}>
              <span className="min-w-0 truncate" title={data.externalUserId}>
                {data.externalUserId}
              </span>
            </Field>
          ) : null}
          {data.email ? (
            <Field label={t("contextEmail")}>
              <span className="min-w-0 truncate" title={data.email}>
                {data.email}
              </span>
            </Field>
          ) : null}
          {contactData ? (
            <ContactProfileEditor
              contact={contactData}
              row={Field}
              showStage
            />
          ) : null}
          {contactFailed ? (
            <div className="py-1 text-xs leading-5 text-muted-foreground">
              {t("contacts:profile.loadError")}
            </div>
          ) : null}
        </Section>
      ) : null}
      {hasVisit ? (
        <Section title={t("contextVisitGroup")}>
          {visit?.pageUrl ? (
            <Field label={t("contextCurrentPage")}>
              <button
                type="button"
                className="min-w-0 truncate text-left hover:underline"
                title={visit.pageUrl}
                onClick={() => void openExternalURL(visit.pageUrl)}
              >
                {visit.pageTitle || visit.pageUrl}
              </button>
            </Field>
          ) : null}
          {visit?.referrerUrl ? (
            <Field label={t("contextReferrer")}>
              <button
                type="button"
                className="min-w-0 truncate text-left hover:underline"
                title={visit.referrerUrl}
                onClick={() => void openExternalURL(visit.referrerUrl)}
              >
                {visit.referrerUrl}
              </button>
            </Field>
          ) : null}
          {device ? (
            <Field label={t("contextDevice")}>
              <span className="min-w-0 truncate" title={device}>
                {device}
              </span>
            </Field>
          ) : null}
          {visit?.language ? (
            <Field label={t("contextLanguage")}>{visit.language}</Field>
          ) : null}
          {visit?.timeZone ? (
            <Field label={t("contextTimeZone")}>{visit.timeZone}</Field>
          ) : null}
          {visit?.country ? (
            <Field label={t("contextCountry")}>{visit.country}</Field>
          ) : null}
        </Section>
      ) : null}
    </>
  )
}
