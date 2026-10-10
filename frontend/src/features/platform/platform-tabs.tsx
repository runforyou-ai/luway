/** 平台设置中带页签页面的外壳：页头与页签常驻并与地址同步，各页签内容经 PlatformTabsActions 把操作放进页头右侧。 */
import { createContext, useContext, useEffect, useState, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { useSearchParams } from "react-router"

import { PageHeader } from "@/components/page-header"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

/** 页头右侧操作区的挂载节点。 */
const PlatformTabsActionsContext = createContext<HTMLElement | null>(null)

/** 渲染页头与页签，按地址中的页签渲染当前页签内容，缺省或无效页签写回首个页签；切换页签时只保留页签参数与 sharedParameters 列出的各页签共用参数。 */
export function PlatformTabsPage<T extends string>({
  title,
  description,
  tabs,
  sharedParameters = [],
  children,
}: {
  title: string
  description: string
  tabs: readonly { value: T; label: string }[]
  sharedParameters?: readonly string[]
  children: (tab: T) => ReactNode
}) {
  const [searchParams, setSearchParams] = useSearchParams()
  const [actions, setActions] = useState<HTMLElement | null>(null)
  const tab = tabs.find((item) => item.value === searchParams.get("tab"))?.value ?? tabs[0]?.value

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (!tab || searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams, tab])

  if (!tab) return null
  return (
    <PlatformTabsActionsContext.Provider value={actions}>
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <PageHeader title={title} description={description}>
          <span ref={setActions} className="contents" />
        </PageHeader>
        <div className="app-page-gutter shrink-0">
          <Tabs
            value={tab}
            onValueChange={(value) => {
              const next = new URLSearchParams({ tab: value })
              for (const name of sharedParameters) {
                const shared = searchParams.get(name)
                if (shared) next.set(name, shared)
              }
              setSearchParams(next, { replace: true })
            }}
          >
            <TabsList>
              {tabs.map((item) => (
                <TabsTrigger key={item.value} value={item.value}>
                  {item.label}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        </div>
        {children(tab)}
      </div>
    </PlatformTabsActionsContext.Provider>
  )
}

/** 把当前页签的操作渲染到页头右侧操作区。 */
export function PlatformTabsActions({ children }: { children: ReactNode }) {
  const target = useContext(PlatformTabsActionsContext)
  return target ? createPortal(children, target) : null
}
