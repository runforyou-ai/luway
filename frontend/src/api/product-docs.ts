/** 产品文档正文读取。 */
import { GetProductDocPage } from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"

/** 读取当前部署可见的产品文档页面正文。 */
export const getProductDocPage = bind(GetProductDocPage)
