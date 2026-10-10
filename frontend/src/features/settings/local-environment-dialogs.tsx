/** 本机设置中添加本地 MCP 服务与安装技能的弹窗。 */
import { useMemo } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm, useWatch, type UseFormRegisterReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"
import { addLocalMCPServer, installLocalSkill, LocalMCPServerType } from "@/platform/native"

/** 本地 MCP 服务可选的连接方式，首项为缺省值。 */
const localMCPServerTypes = [
  LocalMCPServerType.LocalMCPServerTypeStdio,
  LocalMCPServerType.LocalMCPServerTypeSSE,
  LocalMCPServerType.LocalMCPServerTypeHTTP,
] as const

/** 按行拆分文本，去掉首尾空白与空行。 */
function splitLines(text: string) {
  return text.split("\n").map((line) => line.trim()).filter(Boolean)
}

/** 把每行一项的「名称分隔符取值」解析为键值表，没有分隔符的行忽略。 */
function parsePairs(text: string, separator: string) {
  const pairs: Record<string, string> = {}
  for (const line of splitLines(text)) {
    const index = line.indexOf(separator)
    if (index > 0) pairs[line.slice(0, index).trim()] = line.slice(index + 1).trim()
  }
  return pairs
}

/** 试启动并添加本地 MCP 服务：本地进程填写启动命令、参数与环境变量，SSE 与 Streamable HTTP 服务填写地址与请求头。 */
export function AddLocalMCPServerDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation(["settings", "common"])
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const schema = useMemo(
    () =>
      z
        .object({
          name: z.string().trim().min(1)
            .regex(/^[A-Za-z0-9][A-Za-z0-9_-]*$/, t("local.mcp.add.validation.nameInvalid")),
          type: z.nativeEnum(LocalMCPServerType),
          command: z.string().trim(),
          args: z.string(),
          env: z.string(),
          url: z.string().trim(),
          headers: z.string(),
        })
        .superRefine((values, context) => {
          // 本地进程需要启动命令，SSE 与 Streamable HTTP 服务需要地址。
          if (values.type === LocalMCPServerType.LocalMCPServerTypeStdio) {
            if (!values.command) context.addIssue({ code: "custom", path: ["command"], message: t("local.mcp.add.validation.commandRequired") })
          } else if (!/^https?:\/\/\S+$/.test(values.url)) {
            context.addIssue({ code: "custom", path: ["url"], message: t("local.mcp.add.validation.urlInvalid") })
          }
        }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { name: "", type: LocalMCPServerType.LocalMCPServerTypeStdio, command: "", args: "", env: "", url: "", headers: "" },
  })
  const type = useWatch({ control: form.control, name: "type" })
  const local = type === LocalMCPServerType.LocalMCPServerTypeStdio
  const addition = useMutation({
    mutationFn: (values: Values) =>
      addLocalMCPServer(values.type === LocalMCPServerType.LocalMCPServerTypeStdio
        ? { name: values.name, type: values.type, command: values.command, args: splitLines(values.args), env: parsePairs(values.env, "="), url: "", headers: {} }
        : { name: values.name, type: values.type, command: "", args: [], env: {}, url: values.url, headers: parsePairs(values.headers, ":") }),
    onSuccess: () => void invalidate(resourceKeys.localEnvironment()),
  })
  const { isSubmitting } = form.formState

  /** 提交服务配置，试启动成功后刷新本机环境并关闭弹窗。 */
  function submit(values: Values) {
    // 失败由 onError 提示；返回请求让提交状态覆盖整个请求。
    return addition.mutateAsync(values, {
      onSuccess: () => {
        toast.success(t("local.mcp.add.success"))
        form.reset()
        onOpenChange(false)
      },
      onError: (error) => reportError(error, { log: "添加本地 MCP 服务", fallback: t("local.mcp.add.error") }),
    }).catch(() => undefined)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !isSubmitting && onOpenChange(next)}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("local.mcp.add.title")}</DialogTitle>
          <DialogDescription>{t("local.mcp.add.description")}</DialogDescription>
        </DialogHeader>
        <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
          <FieldGroup className="gap-5">
            <FormInputField name="name" control={form.control} label={t("local.mcp.add.name")} disabled={isSubmitting} autoFocus />
            <Controller
              name="type"
              control={form.control}
              render={({ field }) => (
                <Field>
                  <FieldLabel htmlFor="local-mcp-type">{t("local.mcp.add.type")}</FieldLabel>
                  <NativeSelect {...field} id="local-mcp-type" disabled={isSubmitting}>
                    {localMCPServerTypes.map((value) => (
                      <option key={value} value={value}>{t(`local.mcp.types.${value}`)}</option>
                    ))}
                  </NativeSelect>
                </Field>
              )}
            />
            {local ? (
              <>
                <FormInputField name="command" control={form.control} label={t("local.mcp.add.command")} disabled={isSubmitting} />
                <TextareaField id="local-mcp-args" registration={form.register("args")} label={t("local.mcp.add.args")} help={t("local.mcp.add.argsHelp")} disabled={isSubmitting} />
                <TextareaField id="local-mcp-env" registration={form.register("env")} label={t("local.mcp.add.env")} help={t("local.mcp.add.envHelp")} disabled={isSubmitting} />
              </>
            ) : (
              <>
                <FormInputField name="url" control={form.control} label={t("local.mcp.add.url")} disabled={isSubmitting} />
                <TextareaField id="local-mcp-headers" registration={form.register("headers")} label={t("local.mcp.add.headers")} help={t("local.mcp.add.headersHelp")} disabled={isSubmitting} />
              </>
            )}
          </FieldGroup>
          <div className="flex items-center justify-end gap-2">
            <Button type="button" variant="outline" disabled={isSubmitting} onClick={() => onOpenChange(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
              {isSubmitting ? t("local.mcp.add.submitting") : t("local.mcp.add.submit")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** 选填的多行文本字段，每行一项。 */
function TextareaField({
  id,
  registration,
  label,
  help,
  disabled,
}: {
  id: string
  registration: UseFormRegisterReturn
  label: string
  help: string
  disabled: boolean
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Textarea {...registration} id={id} rows={3} disabled={disabled} className="font-mono text-xs" />
      <FieldDescription>{help}</FieldDescription>
    </Field>
  )
}

/** 带帮助文案的单行文本字段，required 为 true 时标签带必填标记。 */
function DescribedInputField({
  id,
  registration,
  label,
  help,
  required,
  disabled,
  autoFocus,
}: {
  id: string
  registration: UseFormRegisterReturn
  label: string
  help: string
  required: boolean
  disabled: boolean
  autoFocus?: boolean
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id} required={required}>{label}</FieldLabel>
      <Input {...registration} id={id} disabled={disabled} autoFocus={autoFocus} />
      <FieldDescription>{help}</FieldDescription>
    </Field>
  )
}

/** 安装技能表单校验规则。 */
const installSkillSchema = z.object({
  source: z.string().trim().min(1),
  name: z.string().trim(),
})

/** 从来源把技能安装到这台电脑，来源含多个技能时按名称选择。 */
export function InstallLocalSkillDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation(["settings", "common"])
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  type Values = z.infer<typeof installSkillSchema>
  const form = useForm<Values>({
    resolver: zodResolver(installSkillSchema),
    shouldUseNativeValidation: true,
    defaultValues: { source: "", name: "" },
  })
  const installation = useMutation({
    mutationFn: (values: Values) => installLocalSkill(values),
    onSuccess: () => void invalidate(resourceKeys.localEnvironment()),
  })
  const { isSubmitting } = form.formState

  /** 安装技能，成功后刷新本机环境并关闭弹窗。 */
  function submit(values: Values) {
    // 失败由 onError 提示；返回请求让提交状态覆盖整个请求。
    return installation.mutateAsync(values, {
      onSuccess: () => {
        toast.success(t("local.skills.install.success"))
        form.reset()
        onOpenChange(false)
      },
      onError: (error) => reportError(error, { log: "安装技能", fallback: t("local.skills.install.error") }),
    }).catch(() => undefined)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !isSubmitting && onOpenChange(next)}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("local.skills.install.title")}</DialogTitle>
          <DialogDescription>{t("local.skills.install.description")}</DialogDescription>
        </DialogHeader>
        <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
          <FieldGroup className="gap-5">
            <DescribedInputField
              id="local-skill-source"
              registration={form.register("source")}
              label={t("local.skills.install.source")}
              help={t("local.skills.install.sourceHelp")}
              required
              disabled={isSubmitting}
              autoFocus
            />
            <DescribedInputField
              id="local-skill-name"
              registration={form.register("name")}
              label={t("local.skills.install.name")}
              help={t("local.skills.install.nameHelp")}
              required={false}
              disabled={isSubmitting}
            />
          </FieldGroup>
          <div className="flex items-center justify-end gap-2">
            <Button type="button" variant="outline" disabled={isSubmitting} onClick={() => onOpenChange(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
              {isSubmitting ? t("local.skills.install.submitting") : t("local.skills.install.submit")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
