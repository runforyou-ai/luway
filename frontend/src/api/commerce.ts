/** 商业服务调用：平台与商业服务的配对状态、配对、解除配对与立即同步。 */
import {
  GetCommercePairing,
  PairCommerce,
  SyncCommerce,
  UnpairCommerce,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"

/** 读取平台与商业服务的配对状态。 */
export const getCommercePairing = bind(GetCommercePairing)

/** 用商业服务生成的配对码完成配对，替换现有配对。 */
export const pairCommerce = bind(PairCommerce)

/** 解除与商业服务的配对。 */
export const unpairCommerce = bind(UnpairCommerce)

/** 立即读取商业服务的变更。 */
export const syncCommerce = bind(SyncCommerce)
