/** 通讯录各分类列表共用的页头、工具栏和滚动加载列表骨架。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { ListToolbar } from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import type { ResourceState } from "@/components/resource-content"
import { ResourceListLayout } from "@/components/resource-list"
import type { PagedResourceMore } from "@/hooks/use-resource"
import { ContactScopeMobileSelect } from "@/features/contacts/contact-scope-mobile-select"
import type { ContactScope } from "@/features/contacts/contact-scope"

/** 渲染带窄屏范围切换和返回入口的页头、筛选工具栏和列表，给出 more 时滚动到末尾继续加载。 */
export function ContactListSection({
  title,
  description,
  scope,
  backTo,
  headerActions,
  toolbar,
  list,
  more,
  children,
}: {
  title: string
  description: string
  scope: ContactScope
  backTo?: string
  headerActions?: ReactNode
  toolbar: ReactNode
  list: ResourceState
  more?: PagedResourceMore
  children: ReactNode
}) {
  const { t } = useTranslation("contacts")

  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={title}
        description={description}
        beforeTitle={<ContactScopeMobileSelect scope={scope} />}
        backTo={backTo}
      >
        {headerActions}
      </PageHeader>

      <ListToolbar>{toolbar}</ListToolbar>

      <ResourceListLayout
        resources={list}
        errorMessage={t("list.loadError")}
        more={more}
      >
        {children}
      </ResourceListLayout>
    </section>
  )
}
