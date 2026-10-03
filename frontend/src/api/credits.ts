/** 积分调用：工作区余额与流水，平台管理员的每日赠送设置、工作区余额、流水与手动调整。 */
import {
  AdjustPlatformWorkspaceCredits,
  GetCreditBalance,
  GetPlatformWorkspaceCredits,
  ListCreditEntries,
  ListPlatformWorkspaceCreditEntries,
  UpdatePlatformDailyCreditGrant,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  CreditEntryKind,
  type CreditEntry,
  type CreditEntryList,
  type CreditEntryListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import type { AIModelUsageId } from "@/api/ai-providers"
import { bind } from "@/api/client"
import type { AIModelCallStatusId } from "@/api/platform-models"

/** 积分流水公共字段。 */
type CreditEntryBase = Omit<CreditEntry, "kind" | "modelUsage" | "callStatus">

/** 积分流水：模型调用带用途与调用状态，其余事件没有模型字段。 */
export type CreditEntryData =
  | (CreditEntryBase & {
      kind: CreditEntryKind.CreditEntryKindModelCall
      modelUsage: AIModelUsageId
      callStatus: AIModelCallStatusId
    })
  | (CreditEntryBase & {
      kind: Exclude<CreditEntryKind, CreditEntryKind.$zero | CreditEntryKind.CreditEntryKindModelCall>
    })

/** 积分流水分页结果。 */
export type CreditEntryListData = { entries: CreditEntryData[]; page: CreditEntryList["page"] }

const listCreditEntriesBound = bind(ListCreditEntries)
const listPlatformWorkspaceCreditEntriesBound = bind(ListPlatformWorkspaceCreditEntries)

/** 读取当前工作区的可用积分与今天的每日赠送。 */
export const getCreditBalance = bind(GetCreditBalance)

/** 读取当前工作区的积分流水。 */
export function listCreditEntries(query: Partial<CreditEntryListInput>, signal?: AbortSignal) {
  return listCreditEntriesBound({ page: query.page ?? 1, pageSize: query.pageSize ?? 50 }, signal) as Promise<CreditEntryListData>
}

/** 修改每个工作区每天赠送的积分。 */
export const updatePlatformDailyCreditGrant = bind(UpdatePlatformDailyCreditGrant)

/** 读取工作区的可用积分与今天的每日赠送。 */
export const getPlatformWorkspaceCredits = bind(GetPlatformWorkspaceCredits)

/** 读取工作区的积分流水。 */
export function listPlatformWorkspaceCreditEntries(workspaceId: string, query: Partial<CreditEntryListInput>, signal?: AbortSignal) {
  return listPlatformWorkspaceCreditEntriesBound(
    workspaceId,
    { page: query.page ?? 1, pageSize: query.pageSize ?? 50 },
    signal,
  ) as Promise<CreditEntryListData>
}

/** 手动增加或扣减工作区积分，扣减最多扣到余额为 0。 */
export const adjustPlatformWorkspaceCredits = bind(AdjustPlatformWorkspaceCredits)
