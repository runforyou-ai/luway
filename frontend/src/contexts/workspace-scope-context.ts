/** 提供当前工作区、账号可进入的全部工作区及账号能否再创建工作区。 */
import { createContext, createElement, useContext, type ReactNode } from "react"

import type { Workspace } from "@/api"

type WorkspaceScope = {
  current: Workspace
  workspaces: Workspace[]
  canCreate: boolean
}

const WorkspaceScopeContext = createContext<WorkspaceScope | null>(null)

/** 向工作区内的页面提供当前工作区与工作区列表。 */
export function WorkspaceScopeProvider({ value, children }: { value: WorkspaceScope; children: ReactNode }) {
  return createElement(WorkspaceScopeContext.Provider, { value }, children)
}

/** 返回当前工作区、账号可进入的全部工作区及账号能否再创建工作区。 */
export function useWorkspaceScope() {
  const context = useContext(WorkspaceScopeContext)
  if (!context) {
    throw new Error("工作区上下文不可用")
  }
  return context
}
