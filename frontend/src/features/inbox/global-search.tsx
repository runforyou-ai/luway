/** 挂载全局搜索模态并提供打开入口。 */
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"

import type { Identity } from "@/api"
import { GlobalSearchContext } from "@/contexts/global-search-context"
import { InboxSearchDialog } from "@/features/inbox/inbox-search-dialog"

/** 提供全局搜索入口，并在打开时挂载搜索模态。 */
export function GlobalSearchProvider({
  identity,
  children,
}: {
  identity: Identity
  children: ReactNode
}) {
  // 每次打开重新计数，模态按该计数重建，检索词和结果不跨次残留。
  const [state, setState] = useState({ open: false, conversationId: "", session: 0 })

  const open = useCallback((conversationId = "") => {
    setState((current) =>
      current.open
        ? current
        : { open: true, conversationId, session: current.session + 1 },
    )
  }, [])

  useEffect(() => {
    // Ctrl/⌘ K 在工作台任意位置打开全局搜索。
    function handleShortcut(event: KeyboardEvent) {
      if (!(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey) return
      if (event.key.toLowerCase() !== "k") return
      event.preventDefault()
      open()
    }
    window.addEventListener("keydown", handleShortcut)
    return () => window.removeEventListener("keydown", handleShortcut)
  }, [open])

  const value = useMemo(() => ({ open }), [open])

  return (
    <GlobalSearchContext.Provider value={value}>
      {children}
      {state.session > 0 ? (
        <InboxSearchDialog
          key={state.session}
          identity={identity}
          conversationId={state.conversationId}
          open={state.open}
          onOpenChange={(next) => {
            if (!next) setState((current) => ({ ...current, open: false }))
          }}
        />
      ) : null}
    </GlobalSearchContext.Provider>
  )
}
