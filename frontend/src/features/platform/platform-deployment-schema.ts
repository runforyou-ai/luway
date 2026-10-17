/** 对象存储表单校验规则，与服务端校验保持一致。 */
import { z } from "zod"

import { isHTTPEndpoint } from "@/lib/http-url"

type DeploymentTranslator = (
  key: "deployment.validation.urlInvalid" | "deployment.validation.storageRequired",
) => string

/** 去掉首尾空白与末尾斜杠。 */
function trimURL(value: string) {
  return value.trim().replace(/\/+$/, "")
}

/** 创建对象存储的表单校验：开启时接口地址与公开地址为完整地址，区域、存储桶与访问密钥必填。 */
export function createStorageSchema(t: DeploymentTranslator) {
  return z
    .object({
      endpoint: z.string().transform(trimURL),
      publicBaseURL: z.string().transform(trimURL),
      region: z.string().trim(),
      bucket: z.string().trim(),
      accessKeyID: z.string().trim(),
      secretAccessKey: z.string().trim(),
      forcePathStyle: z.boolean(),
    })
    .superRefine((values, context) => {
      for (const field of ["endpoint", "publicBaseURL"] as const) {
        if (!isHTTPEndpoint(values[field])) context.addIssue({ code: "custom", path: [field], message: t("deployment.validation.urlInvalid") })
      }
      for (const field of ["region", "bucket", "accessKeyID", "secretAccessKey"] as const) {
        if (!values[field]) context.addIssue({ code: "custom", path: [field], message: t("deployment.validation.storageRequired") })
      }
    })
}

export type StorageFormValues = z.infer<ReturnType<typeof createStorageSchema>>
