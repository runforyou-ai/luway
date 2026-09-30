/** 联网搜索设置调用。 */
import {
  GetWebSearchSettings,
  TestWebSearchService,
  UpdateWebSearchSettings,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import { WebSearchProvider } from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"

export type WebSearchProviderId = Exclude<WebSearchProvider, WebSearchProvider.$zero>

/** 读取当前企业的联网搜索设置。 */
export const getWebSearchSettings = bind(GetWebSearchSettings)

/** 修改当前企业的联网搜索设置，搜索服务为空表示关闭联网搜索。 */
export const updateWebSearchSettings = bind(UpdateWebSearchSettings)

/** 用草稿配置执行一次搜索，验证搜索服务可用。 */
export const testWebSearchService = bind(TestWebSearchService)
