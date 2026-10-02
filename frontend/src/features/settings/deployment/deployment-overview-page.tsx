/** 部署设置的概览页：实例标识、服务端版本、安装时间、账号与工作区规模和工作区上限。 */
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { getDeploymentOverview } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource } from "@/hooks/use-resource"

/** 只读展示部署概况，实例标识可一键复制。 */
export function DeploymentOverviewPage() {
  const { t } = useTranslation(["deployment", "common"])
  const { formatDateTime } = useDateTime()
  const { copied, copy } = useCopyFeedback<"instance">()
  const overview = useResource(resourceKeys.deploymentOverview(), (signal) => getDeploymentOverview(signal), { staleTime: 0 })
  const data = overview.data

  /** 复制实例标识，失败时提示手动复制。 */
  async function copyInstanceID(value: string) {
    if (!(await copy(value, "instance"))) toast.error(t("overview.copyError"))
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("overview.title")} description={t("overview.description")} />
      <PageContent variant="form">
        <ResourceContent resources={overview} errorMessage={t("overview.loadError")}>
          {data ? (
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="deployment-instance-id">{t("overview.instanceId")}</FieldLabel>
                <div className="flex items-center gap-2">
                  <Input id="deployment-instance-id" value={data.instanceId} readOnly className="font-mono text-muted-foreground" />
                  <Button type="button" variant="outline" className="h-11 shrink-0" onClick={() => void copyInstanceID(data.instanceId)}>
                    {copied === "instance" ? t("common:actions.copied") : t("common:actions.copy")}
                  </Button>
                </div>
                <FieldDescription>{t("overview.instanceIdHelp")}</FieldDescription>
              </Field>
              <div className="grid gap-6 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="deployment-version">{t("overview.version")}</FieldLabel>
                  <Input id="deployment-version" value={data.version} readOnly className="text-muted-foreground" />
                </Field>
                <Field>
                  <FieldLabel htmlFor="deployment-installed-at">{t("overview.installedAt")}</FieldLabel>
                  <Input id="deployment-installed-at" value={formatDateTime(data.installedAt)} readOnly className="text-muted-foreground" />
                </Field>
              </div>
              <div className="grid gap-6 sm:grid-cols-3">
                <Field>
                  <FieldLabel htmlFor="deployment-account-count">{t("overview.accountCount")}</FieldLabel>
                  <Input id="deployment-account-count" value={data.accountCount} readOnly className="text-muted-foreground tabular-nums" />
                </Field>
                <Field>
                  <FieldLabel htmlFor="deployment-workspace-count">{t("overview.workspaceCount")}</FieldLabel>
                  <Input id="deployment-workspace-count" value={data.workspaceCount} readOnly className="text-muted-foreground tabular-nums" />
                </Field>
                <Field>
                  <FieldLabel htmlFor="deployment-workspace-limit">{t("overview.workspaceLimit")}</FieldLabel>
                  <Input
                    id="deployment-workspace-limit"
                    value={data.capabilities.workspaceLimit === 0 ? t("overview.unlimited") : data.capabilities.workspaceLimit}
                    readOnly
                    className="text-muted-foreground tabular-nums"
                  />
                </Field>
              </div>
            </FieldGroup>
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}
