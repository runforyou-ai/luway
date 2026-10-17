/** 平台后台表面：主题作用域；行操作据此在 Web 总后台改用「⋯」而非自定义右键菜单。 */
import { createContext, useContext, useEffect, type ReactNode } from "react"

const AdminSurfaceContext = createContext(false)

/** 是否在平台后台登录页或后台壳内。 */
export function useAdminSurface() {
  return useContext(AdminSurfaceContext)
}

/** 后台页面挂载时在根元素标记 data-admin，卸载后清除。 */
function useAdminTheme() {
  useEffect(() => {
    const root = document.documentElement
    root.setAttribute("data-admin", "")
    return () => root.removeAttribute("data-admin")
  }, [])
}

/** 包裹后台表面：启用主题，并让子树识别为后台。 */
export function AdminSurface({ children }: { children: ReactNode }) {
  useAdminTheme()
  return <AdminSurfaceContext.Provider value={true}>{children}</AdminSurfaceContext.Provider>
}
