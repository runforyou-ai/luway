/** 公开构建的前端入口。 */
import { bootstrap } from "@/bootstrap"
import "@/index.css"

void bootstrap({}).catch((error: unknown) => {
  console.error("应用初始化失败", error)
})
