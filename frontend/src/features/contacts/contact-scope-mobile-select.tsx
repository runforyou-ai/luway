/** 窄视口下的通讯录范围切换选择器。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { NativeSelect } from "@/components/ui/native-select"
import type { ContactScope } from "@/features/contacts/contact-scope"

/** 在窄视口用下拉选择切换通讯录范围。 */
export function ContactScopeMobileSelect({ scope }: { scope: ContactScope }) {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()

  return (
    <div className="w-full md:hidden">
      <NativeSelect
        className="h-8 w-full"
        aria-label={t("scopeNavigation")}
        value={scope}
        onChange={(event) =>
          navigate(
            event.target.value === "team"
              ? "/contacts/teams"
              : `/contacts/${event.target.value}`,
          )
        }
      >
        <option value="employees">{t("scopes.employees")}</option>
        <option value="team">{t("scopes.teams")}</option>
        <option value="external">{t("scopes.external")}</option>
      </NativeSelect>
    </div>
  )
}
