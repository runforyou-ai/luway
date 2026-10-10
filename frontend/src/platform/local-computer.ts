/** 桌面端本机电脑：以登录会话为账号所在的工作区注册这台电脑，把电脑凭据交给本机执行器，并读取本机在当前工作区的电脑与运行环境。 */
import { invoke, requestMeta, serverURL } from "@/api/client"
import type { ComputerRegistration } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { currentSessionGeneration, requestWorkspace } from "@/api/session-scope"
import { resolveDesktopOS } from "@/platform/app-platform"
import {
  attachComputer,
  currentNativeComputer,
  detachComputers,
  getComputerIdentity,
  listAttachedComputers,
  type ComputerAccount,
  type LocalComputer,
} from "@/platform/native"

// 本登录会话代次中已注册的工作区；凭据失效后被删除的注册在下一次登录会话中重新注册。
let registered = { generation: -1, workspaces: new Set<string>() }

// 注册调用串行进行，同一工作区不会并发注册。
let queue: Promise<unknown> = Promise.resolve()

/** 返回本登录会话代次中已注册的工作区集合，代次变化时清空。 */
function registeredWorkspaces() {
  const generation = currentSessionGeneration()
  if (registered.generation !== generation) registered = { generation, workspaces: new Set() }
  return registered.workspaces
}

/** 在指定工作区注册这台电脑并把电脑凭据交给本机执行器；本机已有注册或本登录会话已注册过时跳过。 */
async function registerWorkspace(account: ComputerAccount, workspaceID: string, attached: Set<string>) {
  const workspaces = registeredWorkspaces()
  if (attached.has(workspaceID) || workspaces.has(workspaceID)) return
  const identity = await getComputerIdentity()
  if (!identity.installId) return
  const registration = await invoke<ComputerRegistration>(
    { method: "POST", path: "/computers", body: { installId: identity.installId, name: identity.name } },
    { ...requestMeta(), workspaceId: workspaceID },
  )
  await attachComputer({
    serverUrl: account.serverUrl,
    accountId: account.accountId,
    workspaceId: workspaceID,
    computerId: registration.computer.id,
    credential: registration.credential,
  })
  workspaces.add(workspaceID)
  console.info("电脑已注册", { workspaceID, computerID: registration.computer.id })
}

/** 返回当前登录账号在当前服务器上的本机电脑分组。 */
async function currentAccount(): Promise<ComputerAccount> {
  const account = await ops.loadAccount()
  return { serverUrl: serverURL(), accountId: account.id }
}

/** 为账号所在的每个工作区注册这台电脑，并删除账号已不属于的工作区的注册；非桌面端不处理。 */
export function syncComputerRegistrations(accountID: string, workspaceIDs: string[]) {
  if (resolveDesktopOS() === null) return Promise.resolve()
  const run = async () => {
    const account = { serverUrl: serverURL(), accountId: accountID }
    const attached = new Set((await listAttachedComputers(account)).map((computer) => computer.workspaceId))
    for (const workspaceID of workspaceIDs) {
      await registerWorkspace(account, workspaceID, attached)
    }
    await detachComputers(account, workspaceIDs)
  }
  const next = queue.then(run)
  queue = next.catch(() => {})
  return next
}

/** 读取本机在当前工作区的电脑注册状态与运行环境，尚未注册时先注册；非桌面端返回空电脑。 */
export async function currentComputer(): Promise<LocalComputer> {
  if (resolveDesktopOS() === null) return { computerId: "", toolchain: null }
  const account = await currentAccount()
  const workspaceID = requestWorkspace()
  const register = async () => {
    const attached = new Set((await listAttachedComputers(account)).map((computer) => computer.workspaceId))
    await registerWorkspace(account, workspaceID, attached)
  }
  // 注册失败时照常返回未注册的本机电脑，由下一次同步重试。
  const next = queue.then(register)
  queue = next.catch(() => {})
  await next.catch((error: unknown) => console.warn("注册电脑失败", error))
  return currentNativeComputer(account, workspaceID)
}
