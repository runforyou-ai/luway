/** 为长期挂载的页面指定浮层挂载容器。 */
import * as React from "react"

type PortalContainerState = {
  container: HTMLElement
  active: boolean
}

const PortalContainerContext = React.createContext<PortalContainerState | null>(
  null,
)

function PortalContainerProvider({
  container,
  active,
  children,
}: {
  container: HTMLElement
  active: boolean
  children: React.ReactNode
}) {
  return (
    <PortalContainerContext.Provider value={{ container, active }}>
      {children}
    </PortalContainerContext.Provider>
  )
}

function usePortalContainer() {
  return React.useContext(PortalContainerContext)
}

export { PortalContainerProvider, usePortalContainer }
