/** 电脑调用。 */
import { OperationLevel, type ComputerRegistration, type ComputerRegistrationInput } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { invoke, requestMeta } from "@/api/client"
import { requestWorkspace } from "@/api/session-scope"

/** 读取当前成员已注册的个人电脑列表。 */
export const listComputers = ops.listComputers

/** 撤销当前成员的个人电脑或工作区电脑。 */
export const revokeComputer = ops.revokeComputer

/** 读取当前工作区的工作区电脑列表。 */
export const listWorkspaceComputers = ops.listWorkspaceComputers

/** 添加工作区电脑并返回电脑凭据。 */
export function createWorkspaceComputer(name: string) {
  return ops.createWorkspaceComputer({ name })
}

/** 为工作区电脑签发新凭据，旧凭据立即失效。 */
export const resetComputerCredential = ops.resetComputerCredential

/** 把这台电脑注册为当前成员在当前工作区的个人电脑，返回电脑凭据。 */
export const registerComputer = ops.registerComputer

/** 在指定工作区注册个人电脑；目标为当前工作区时走公开操作，否则覆盖请求工作区头。 */
export function registerComputerInWorkspace(workspaceID: string, input: ComputerRegistrationInput) {
  if (workspaceID === requestWorkspace()) {
    return registerComputer(input)
  }
  return invoke<ComputerRegistration>(
    { method: "POST", path: "/computers", body: input },
    { ...requestMeta(), workspaceId: workspaceID },
  )
}
/** 工作区电脑授权可选的最高级别：读取文件、修改文件与全部操作。 */
export const computerOperationLevels = [
  OperationLevel.L0,
  OperationLevel.L2,
  OperationLevel.L3,
] as const satisfies readonly OperationLevel[]
