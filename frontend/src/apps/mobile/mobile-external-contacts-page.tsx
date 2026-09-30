/** 移动端外部联系人列表、阶段与标签筛选，以及含客户资料的详情。 */
import { useState } from "react"
import { ChevronRightIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useLocation, useNavigate, useParams } from "react-router"

import {
  ContactStage,
  getContact,
  isNotFoundApiError,
  listContactTags,
  listContacts,
} from "@/api"
import { editableContactFields } from "@/apps/mobile/mobile-external-contact-editor"
import { MobileFilterSheet } from "@/apps/mobile/mobile-filter-sheet"
import { useMobileNavigation } from "@/apps/mobile/mobile-navigation"
import {
  MobilePageHeader,
  MobilePageState,
  MobileProfileField,
  MobileProfileSection,
  MobileScrollArea,
  MobileSearchBar,
} from "@/apps/mobile/mobile-page"
import { MobilePagedList } from "@/apps/mobile/mobile-paged-list"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { ContactProfileEditor } from "@/components/contact-profile-editor"
import { contactStageKey, contactStageOptions } from "@/features/contacts/external/contact-labels"
import { contactValuesFromDetail } from "@/features/contacts/external/contact-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useContactName } from "@/hooks/use-contact-name"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource } from "@/hooks/use-resource"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 联系人阶段筛选的可选项，空值表示全部阶段。 */
const contactStageFilters = [
  { value: "", label: "filters.allStages" },
  ...contactStageOptions,
] as const

/** 防抖同步搜索条件，按阶段筛选并展示现有外部联系人。 */
export function MobileExternalContactsPage() {
  const { t } = useTranslation(["contacts", "mobile"])
  const navigate = useNavigate()
  const location = useLocation()
  const { listPageCounts, scrollPositions } = useMobileNavigation()
  // 检索词变化时重置目标查询的加载进度和滚动位置。
  const {
    searchParams,
    setParameters,
    query: queryText,
    search,
    setSearch,
  } = useListSearchParams({
    onQueryChange: (query) => {
      const storageKey = `external:${stage ?? ""}:${tagId}:${query}`
      listPageCounts.delete(storageKey)
      scrollPositions.delete(storageKey)
    },
  })
  const stage = optionalWailsEnum(ContactStage, searchParams.get("stage"))
  const tagId = searchParams.get("tagId") ?? ""
  const [draftStage, setDraftStage] = useState<ContactStage | "">("")
  const [draftTagId, setDraftTagId] = useState("")
  const stageFilter = contactStageFilters.find(
    (item) => item.value === (stage ?? ""),
  )
  const tagsResource = useResource(resourceKeys.contactTags(), () =>
    listContactTags(),
  )
  const tags = tagsResource.data?.tags ?? []
  const tagFilter = tags.find((tag) => tag.id === tagId)

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("scopes.external")}
        backTo="/contacts"
        actions={
          <Button
            variant="ghost"
            size="icon-lg"
            className="-mr-2"
            aria-label={t("detail.createTitle")}
            title={t("detail.createTitle")}
            onClick={() =>
              navigate("/contacts/external/new", { state: { mobileBack: true } })
            }
          >
            <PlusIcon />
          </Button>
        }
      />
      <MobileSearchBar
        label={t("search.external")}
        value={search}
        onChange={setSearch}
      />
      <MobileFilterSheet
        summary={[
          stage && stageFilter ? t(stageFilter.label) : "",
          tagFilter?.name ?? "",
        ]
          .filter(Boolean)
          .join(" · ")}
        onOpen={() => {
          setDraftStage(stage ?? "")
          setDraftTagId(tagId)
        }}
        onReset={() => {
          setDraftStage("")
          setDraftTagId("")
        }}
        onApply={() => {
          // 切换筛选时重置目标查询的加载进度和滚动位置。
          const storageKey = `external:${draftStage}:${draftTagId}:${queryText.trim()}`
          listPageCounts.delete(storageKey)
          scrollPositions.delete(storageKey)
          setParameters(
            { stage: draftStage || null, tagId: draftTagId || null },
            true,
            location.state,
          )
        }}
      >
        <div
          role="group"
          aria-label={t("filters.stage")}
          className="grid grid-cols-2 gap-2"
        >
          {contactStageFilters.map((item) => (
            <Button
              key={item.value}
              variant={draftStage === item.value ? "default" : "outline"}
              className="min-h-11"
              aria-pressed={draftStage === item.value}
              onClick={() => setDraftStage(item.value)}
            >
              {t(item.label)}
            </Button>
          ))}
        </div>
        {tags.length > 0 ? (
          <div
            role="group"
            aria-label={t("filters.tag")}
            className="mt-4 grid grid-cols-2 gap-2"
          >
            {tags.map((tag) => (
              <Button
                key={tag.id}
                variant={draftTagId === tag.id ? "default" : "outline"}
                className="min-h-11 min-w-0"
                aria-pressed={draftTagId === tag.id}
                onClick={() =>
                  setDraftTagId(draftTagId === tag.id ? "" : tag.id)
                }
              >
                <span className="truncate">{tag.name}</span>
              </Button>
            ))}
          </div>
        ) : null}
      </MobileFilterSheet>
      <MobileExternalContactList
        key={`external:${stage ?? ""}:${tagId}:${queryText.trim()}`}
        stage={stage}
        tagId={tagId}
        queryText={queryText.trim()}
        searching={search !== queryText}
      />
    </section>
  )
}

/** 逐页读取联系人，行内展示阶段、主要联系方式、来源渠道和标签。 */
function MobileExternalContactList({
  stage,
  tagId,
  queryText,
  searching,
}: {
  stage: ContactStage | undefined
  tagId: string
  queryText: string
  searching: boolean
}) {
  const { t } = useTranslation(["contacts", "mobile"])
  const contactName = useContactName()
  return (
    <MobilePagedList
      storageKey={`external:${stage ?? ""}:${tagId}:${queryText}`}
      searching={searching}
      labels={{
        loadError: t("mobile:external.loadError"),
        empty: t("mobile:external.empty"),
        allLoaded: t("mobile:external.allLoaded"),
      }}
      source={(page) => {
        const query = { query: queryText, stage, tagId, page, pageSize: 50 }
        return {
          key: resourceKeys.contacts(query),
          load: (signal) => listContacts(query, signal),
        }
      }}
      select={(data) => ({ items: data.contacts, page: data.page })}
    >
      {(contacts) => (
        <ul className="divide-y border-b">
          {contacts.map((contact) => {
            const stageKey = contactStageKey(contact.stage)
            return (
              <li key={contact.id}>
                <Link
                  to={`/contacts/external/${contact.id}`}
                  state={{ mobileBack: true }}
                  className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                >
                  <ProfileAvatar
                    name={contact.displayName}
                    imageURL={contact.avatarUrl}
                    seed={contact.number}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-[15px] font-medium">
                        {contactName(contact.displayName, contact.number)}
                      </span>
                      {stageKey ? (
                        <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs font-medium">
                          {t(stageKey)}
                        </span>
                      ) : null}
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {[
                        // 名称取自邮箱时次要信息改用电话。
                        (contact.primaryEmail === contact.displayName ? null : contact.primaryEmail) || contact.primaryPhone,
                        contact.sourceChannelName,
                        ...contact.tags.slice(0, 2).map((tag) => tag.name),
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </span>
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </Link>
              </li>
            )
          })}
        </ul>
      )}
    </MobilePagedList>
  )
}

/** 展示联系人的资料与关联渠道，资料项点进后逐项编辑。 */
export function MobileExternalContactPage() {
  const { t } = useTranslation(["contacts", "mobile", "common"])
  const contactName = useContactName()
  const { contactID = "" } = useParams()
  const navigate = useNavigate()
  const { formatDateTime } = useDateTime()
  const {
    data: detail,
    loading,
    error,
    refresh,
  } = useResource(
    resourceKeys.contact(contactID),
    () => getContact(contactID),
    { staleTime: 0, refetchOnWindowFocus: true },
  )
  const empty = t("detail.empty")
  const values = detail ? contactValuesFromDetail(detail) : null
  const stageKey = detail ? contactStageKey(detail.contact.stage) : null

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("mobile:external.detail")}
        backTo="/contacts/external"
        actions={
          error && detail ? (
            <Button
              variant="ghost"
              className="min-h-11"
              onClick={() => void refresh()}
            >
              {t("mobile:refreshFailed")}
            </Button>
          ) : undefined
        }
      />
      <MobileScrollArea
        storageKey={`external:${contactID}`}
        ready={Boolean(detail)}
        className="px-4 py-6"
      >
        {loading && !detail ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : null}
        {error && !detail ? (
          <MobilePageState
            title={t(
              isNotFoundApiError(error)
                ? "mobile:external.notFound"
                : "mobile:external.loadError",
            )}
            onRetry={() => void refresh()}
          />
        ) : null}
        {detail && values ? (
          <div>
            <div className="flex items-center gap-3 pb-6">
              <ProfileAvatar
                name={detail.name}
                imageURL={detail.avatarUrl}
                seed={detail.contact.number}
                className="size-14"
              />
              <div className="min-w-0 space-y-2">
                <h2 className="break-words text-lg font-semibold">
                  {contactName(detail.name, detail.contact.number)}
                </h2>
                {stageKey ? (
                  <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-xs font-medium">
                    {t(stageKey)}
                  </span>
                ) : null}
              </div>
            </div>
            <div className="divide-y border-y">
              {editableContactFields.map(({ field, label }) => (
                <button
                    key={field}
                    type="button"
                    className="flex w-full items-center gap-3 py-4 text-left outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                    onClick={() =>
                      navigate(`/contacts/external/${contactID}/edit/${field}`, {
                        state: { mobileBack: true },
                      })
                    }
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block text-xs text-muted-foreground">
                        {t(label)}
                      </span>
                      <span className="block mt-1 break-words whitespace-pre-wrap text-sm">
                        {field === "stage"
                          ? stageKey
                            ? t(stageKey)
                            : empty
                          : values[field] || empty}
                      </span>
                    </span>
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </button>
              ))}
              <div className="py-4">
                <span className="block text-xs text-muted-foreground">
                  {t("detail.sourceChannel")}
                </span>
                <span className="block mt-1 break-words text-sm">
                  {detail.sourceChannel.name}
                </span>
              </div>
              <div className="py-4">
                <span className="block text-xs text-muted-foreground">
                  {t("common:time.addedAtColumn")}
                </span>
                <span className="block mt-1 text-sm">
                  {formatDateTime(detail.contact.createdAt)}
                </span>
              </div>
              <div className="py-4">
                <span className="block text-xs text-muted-foreground">
                  {t("detail.linkedChannels")}
                </span>
                <span className="block mt-1 space-y-2 text-sm">
                  {detail.channelIdentities.length
                    ? detail.channelIdentities.map((identity) => (
                        <span
                          key={`${identity.channelId}:${identity.externalId}`}
                          className="block"
                        >
                          <span className="block break-words">
                            {identity.channelName}
                          </span>
                          <span className="block break-all text-xs text-muted-foreground">
                            {identity.displayName || identity.externalId}
                          </span>
                        </span>
                      ))
                    : empty}
                </span>
              </div>
            </div>
            <div className="mt-6">
              <MobileProfileSection title={t("profile.title")}>
                <ContactProfileEditor contact={detail} row={MobileProfileField} />
              </MobileProfileSection>
            </div>
          </div>
        ) : null}
      </MobileScrollArea>
    </section>
  )
}
