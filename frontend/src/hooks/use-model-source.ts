/** 读取本部署的模型来源：platform 仅平台能力池，workspace 为工作区自配。 */
import { AIModelSource, loadInstallationStatus, type AIModelSource as ModelSource } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 平台 Auto 在业务配置中的引用值。 */
export const platformModelRef = "auto"

/** 返回部署模型来源；成功读到安装状态前 ready 为 false，避免失败回退误挂载平台创建默认值。 */
export function useModelSource(): {
  modelSource: ModelSource
  platformOnly: boolean
  ready: boolean
  loading: boolean
  error: unknown
  refresh: () => Promise<unknown>
} {
  const installation = useResource(resourceKeys.installationStatus(), (signal) => loadInstallationStatus(signal))
  const modelSource = installation.data?.modelSource ?? AIModelSource.Workspace
  const ready = Boolean(installation.data)
  return {
    modelSource,
    platformOnly: modelSource === AIModelSource.Platform,
    ready,
    loading: !ready && !installation.error,
    error: !ready ? installation.error : undefined,
    refresh: installation.refresh,
  }
}
