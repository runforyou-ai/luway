/** 本机设置：这台电脑为 AI 员工提供的运行环境、本地 MCP 服务与技能，当前页签与地址同步。 */
import { useEffect, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { BlocksIcon, PackageIcon, SparklesIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router"
import { toast } from "sonner"

import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceContent } from "@/components/resource-content"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { AddLocalMCPServerDialog, InstallLocalSkillDialog } from "@/features/settings/local-environment-dialogs"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import {
  getLocalEnvironment,
  installLocalToolchain,
  LocalMCPServerType,
  LocalSkillSource,
  LocalToolchainFailure,
  LocalToolchainState,
  openLocalToolchainFolder,
  removeLocalMCPServer,
  removeLocalSkill,
  uninstallLocalToolchain,
  updateLocalToolchain,
  type LocalEnvironment,
  type LocalMCPServer,
  type LocalSkill,
  type LocalSkillSourceId,
} from "@/platform/native"

/** 本机设置的页签，首项为缺省页签。 */
const localTabs = ["toolchain", "mcp", "skills"] as const

/** 按地址中的页签显示运行环境、本地 MCP 服务或技能。 */
export function LocalEnvironmentSettings() {
  const { t } = useTranslation("settings")
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = localTabs.find((value) => value === searchParams.get("tab")) ?? localTabs[0]
  const environment = useResource(resourceKeys.localEnvironment(), () => getLocalEnvironment(), {
    staleTime: 0,
  })

  // 缺省或无效页签统一写回地址，刷新时恢复同一页签。
  useEffect(() => {
    if (searchParams.get("tab") === tab) return
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams, tab])

  return (
    <Tabs
      value={tab}
      onValueChange={(value) => {
        const next = new URLSearchParams(searchParams)
        next.set("tab", value)
        setSearchParams(next, { replace: true })
      }}
    >
      <TabsList>
        <TabsTrigger value="toolchain">{t("local.tabs.toolchain")}</TabsTrigger>
        <TabsTrigger value="mcp">{t("local.tabs.mcp")}</TabsTrigger>
        <TabsTrigger value="skills">{t("local.tabs.skills")}</TabsTrigger>
      </TabsList>
      <ResourceContent resources={environment} errorMessage={t("local.loadError")}>
        {environment.data ? (
          <>
            <TabsContent value="toolchain" forceMount className="mt-6 data-[state=inactive]:hidden">
              <ToolchainSettings environment={environment.data} />
            </TabsContent>
            <TabsContent value="mcp" forceMount className="mt-6 data-[state=inactive]:hidden">
              <LocalMCPServers servers={environment.data.mcpServers} />
            </TabsContent>
            <TabsContent value="skills" forceMount className="mt-6 data-[state=inactive]:hidden">
              <LocalSkills skills={environment.data.skills} />
            </TabsContent>
          </>
        ) : null}
      </ResourceContent>
    </Tabs>
  )
}

/** 展示运行环境的状态、各组件版本与安装位置，并提供打开位置、检查更新、卸载与重新安装。 */
function ToolchainSettings({ environment }: { environment: LocalEnvironment }) {
  const { t } = useTranslation(["settings", "common"])
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  // 运行环境操作结束后无论成败都刷新本机环境。
  const toolchainAction = useMutation({
    mutationFn: ({ action }: { kind: "update" | "install"; action: () => Promise<string> }) => action(),
    onSettled: () => void invalidate(resourceKeys.localEnvironment()),
  })
  const pending = toolchainAction.isPending ? toolchainAction.variables.kind : null
  // 卸载结束后无论成败都刷新本机环境与当前电脑，部分卸载的状态同样反映到界面。
  const uninstallation = useConfirmedAction<true>({
    action: () =>
      uninstallLocalToolchain().finally(() => {
        void invalidate(resourceKeys.localEnvironment())
        void invalidate(resourceKeys.currentComputer())
      }),
    successMessage: () => t("local.toolchain.uninstalled"),
    errorMessage: () => t("local.toolchain.uninstallError"),
    logLabel: "卸载运行环境",
  })
  const uninstalling = uninstallation.pending
  const { toolchain } = environment
  const uninstalled = toolchain.state === LocalToolchainState.LocalToolchainStateUninstalled
  const updating = pending === "update" || toolchain.updating
  const components = [
    { name: "uv", version: environment.uvVersion },
    { name: "Node.js", version: environment.nodeVersion },
    { name: "Python", version: environment.pythonVersion },
  ]

  /** 执行运行环境操作并反馈结果，结束后刷新本机环境。 */
  function run(kind: "update" | "install", action: () => Promise<string>, logLabel: string) {
    toolchainAction.mutate({ kind, action }, {
      onSuccess: (message) => toast.success(message),
      onError: (error) => reportError(error, { log: logLabel, fallback: t(`local.toolchain.${kind}Error`) }),
    })
  }

  /** 在系统文件管理器中打开安装位置。 */
  async function openFolder() {
    try {
      await openLocalToolchainFolder()
    } catch (error) {
      console.warn("打开运行环境安装位置失败", error)
      toast.error(t("local.toolchain.openError"))
    }
  }

  return (
    <>
      <FieldGroup className="gap-8">
        <Field>
          {/* 组件标题行右侧放运行环境的操作。 */}
          <div className="flex items-center justify-between gap-3">
            <FieldLabel>{t("local.toolchain.components")}</FieldLabel>
            <div className="flex shrink-0 gap-2">
              {uninstalled ? (
                <Button
                  type="button"
                  size="sm"
                  disabled={pending !== null}
                  onClick={() =>
                    run(
                      "install",
                      async () => {
                        await installLocalToolchain()
                        return t("local.toolchain.installStarted")
                      },
                      "安装运行环境",
                    )
                  }
                >
                  {pending === "install" ? t("local.toolchain.installing") : t("local.toolchain.install")}
                </Button>
              ) : (
                <>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={updating || uninstalling || toolchain.state !== LocalToolchainState.LocalToolchainStateReady}
                    onClick={() =>
                      run(
                        "update",
                        async () => ((await updateLocalToolchain()).updated ? t("local.toolchain.updated") : t("local.toolchain.upToDate")),
                        "更新运行环境",
                      )
                    }
                  >
                    {updating ? t("local.toolchain.updating") : t("local.toolchain.update")}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={updating || uninstalling}
                    onClick={() => uninstallation.select(true)}
                  >
                    {t("local.toolchain.uninstall")}
                  </Button>
                </>
              )}
            </div>
          </div>
          <FieldDescription>{t("local.toolchain.description")}</FieldDescription>
          <ResourceListFrame>
            <ResourceTable
              columns={[
                {
                  key: "component",
                  header: t("local.toolchain.components"),
                  cellClassName: "min-w-0",
                  cell: (component) => (
                    <ResourceRowIdentity
                      icon={PackageIcon}
                      name={component.name}
                      secondary={component.version || <ComponentPendingState environment={environment} />}
                    />
                  ),
                },
              ]}
              rows={components}
              rowKey={(component) => component.name}
              empty={null}
            />
          </ResourceListFrame>
        </Field>
        <Field>
          <FieldLabel>{t("local.toolchain.location")}</FieldLabel>
          <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
            <code className="flex min-h-8 min-w-0 flex-1 items-center font-mono text-sm break-all">
              {environment.location}
            </code>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="shrink-0"
              disabled={uninstalled}
              onClick={() => void openFolder()}
            >
              {t("local.toolchain.open")}
            </Button>
          </div>
        </Field>
      </FieldGroup>
      <ConfirmationDialog
        {...uninstallation.dialog}
        title={t("local.toolchain.uninstallTitle")}
        description={t("local.toolchain.uninstallDescription")}
        pendingLabel={t("local.toolchain.uninstalling")}
        destructive
      />
    </>
  )
}

/** 组件尚未安装时在版本位置说明原因：正在安装、安装失败（以警示色显示原因）或未安装。 */
function ComponentPendingState({ environment }: { environment: LocalEnvironment }) {
  const { t } = useTranslation("settings")
  const { toolchain } = environment
  switch (toolchain.state) {
    case LocalToolchainState.LocalToolchainStatePreparing:
      return t("local.toolchain.componentStates.installing")
    case LocalToolchainState.LocalToolchainStateFailed:
      return (
        <span className="text-destructive">
          {toolchain.failure === LocalToolchainFailure.LocalToolchainFailureDownload
            ? t("local.toolchain.componentStates.downloadFailed")
            : toolchain.failure === LocalToolchainFailure.LocalToolchainFailureVerify
              ? t("local.toolchain.componentStates.verifyFailed")
              : t("local.toolchain.componentStates.installFailed")}
        </span>
      )
    default:
      return t("local.toolchain.componentStates.notInstalled")
  }
}

/** 列出本地 MCP 服务，可添加新服务或删除其中一个。 */
function LocalMCPServers({ servers }: { servers: LocalMCPServer[] }) {
  const { t } = useTranslation(["settings", "common"])
  const [adding, setAdding] = useState(false)
  const removal = useConfirmedAction<LocalMCPServer>({
    action: (server) => removeLocalMCPServer(server.name),
    invalidateKeys: () => [resourceKeys.localEnvironment()],
    successMessage: () => t("local.mcp.remove.success"),
    errorMessage: () => t("local.mcp.remove.error"),
    logLabel: "删除本地 MCP 服务",
  })

  return (
    <>
      <div className="mb-4 flex justify-end">
        <Button type="button" variant="outline" onClick={() => setAdding(true)}>
          {t("local.mcp.add.action")}
        </Button>
      </div>
      <ResourceListFrame>
        <ResourceTable
          columns={[
            {
              key: "server",
              header: t("local.mcp.columns.name"),
              cellClassName: "min-w-0",
              cell: (server) => (
                <ResourceRowIdentity
                  icon={BlocksIcon}
                  name={server.name}
                  secondary={t(`local.mcp.types.${server.type || LocalMCPServerType.LocalMCPServerTypeStdio}`)}
                  description={server.url || [server.command, ...server.args].join(" ")}
                />
              ),
            },
          ]}
          rows={servers}
          rowKey={(server) => server.name}
          empty={t("local.mcp.empty")}
          rowActions={(server) => [
            {
              key: "remove",
              label: t("common:actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => removal.select(server),
            },
          ]}
        />
      </ResourceListFrame>
      <ConfirmationDialog
        {...removal.dialog}
        title={t("local.mcp.remove.title", { name: removal.item?.name ?? "" })}
        description={t("local.mcp.remove.description")}
        pendingLabel={t("common:actions.deleting")}
      />
      <AddLocalMCPServerDialog open={adding} onOpenChange={setAdding} />
    </>
  )
}

/** 列出这台电脑上可用的技能及其来源，可安装新技能，在这里安装的技能可以删除。 */
function LocalSkills({ skills }: { skills: LocalSkill[] }) {
  const { t } = useTranslation(["settings", "common"])
  const [installing, setInstalling] = useState(false)
  const removal = useConfirmedAction<LocalSkill>({
    action: (skill) => removeLocalSkill(skill.name),
    invalidateKeys: () => [resourceKeys.localEnvironment()],
    successMessage: () => t("local.skills.remove.success"),
    errorMessage: () => t("local.skills.remove.error"),
    logLabel: "删除技能",
  })

  return (
    <>
      <div className="mb-4 flex justify-end">
        <Button type="button" variant="outline" onClick={() => setInstalling(true)}>
          {t("local.skills.install.action")}
        </Button>
      </div>
      <ResourceListFrame>
        <ResourceTable
          columns={[
            {
              key: "skill",
              header: t("local.skills.columns.name"),
              cellClassName: "min-w-0",
              cell: (skill) => (
                <ResourceRowIdentity
                  icon={SparklesIcon}
                  name={skill.name}
                  secondary={t(`local.skills.sources.${skill.source as LocalSkillSourceId}`)}
                  description={skill.description}
                />
              ),
            },
          ]}
          rows={skills}
          rowKey={(skill) => skill.name}
          empty={t("local.skills.empty")}
          rowActions={(skill) =>
            skill.source === LocalSkillSource.LocalSkillSourceManaged
              ? [
                  {
                    key: "remove",
                    label: t("common:actions.delete"),
                    destructive: true,
                    separatorBefore: true,
                    onSelect: () => removal.select(skill),
                  },
                ]
              : []
          }
        />
      </ResourceListFrame>
      <ConfirmationDialog
        {...removal.dialog}
        title={t("local.skills.remove.title", { name: removal.item?.name ?? "" })}
        description={t("local.skills.remove.description")}
        pendingLabel={t("common:actions.deleting")}
      />
      <InstallLocalSkillDialog open={installing} onOpenChange={setInstalling} />
    </>
  )
}
