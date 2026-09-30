/** 修改密码表单校验规则。 */
import { z } from "zod"

type PasswordTranslator = (
  key:
    | "password.validation.currentPasswordRequired"
    | "password.validation.newPasswordRequired"
    | "password.validation.newPasswordTooShort"
    | "password.validation.newPasswordTooLong"
    | "password.validation.confirmPasswordRequired"
    | "password.validation.passwordMismatch",
) => string

/** 创建修改密码表单校验。 */
export function createChangePasswordSchema(t: PasswordTranslator) {
  return z
    .object({
      currentPassword: z
        .string()
        .min(1, t("password.validation.currentPasswordRequired")),
      newPassword: z
        .string()
        .min(1, t("password.validation.newPasswordRequired"))
        .min(8, t("password.validation.newPasswordTooShort"))
        .refine(
          (password) => new TextEncoder().encode(password).length <= 72,
          t("password.validation.newPasswordTooLong"),
        ),
      confirmPassword: z
        .string()
        .min(1, t("password.validation.confirmPasswordRequired")),
    })
    .superRefine((values, context) => {
      if (
        values.confirmPassword !== "" &&
        values.confirmPassword !== values.newPassword
      ) {
        context.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["confirmPassword"],
          message: t("password.validation.passwordMismatch"),
        })
      }
    })
}

export type ChangePasswordFormValues = z.infer<
  ReturnType<typeof createChangePasswordSchema>
>
