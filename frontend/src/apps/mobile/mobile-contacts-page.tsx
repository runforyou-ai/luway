/** 移动端通讯录分类入口。 */
import {
  BotIcon,
  ChevronRightIcon,
  ContactRoundIcon,
  UsersRoundIcon,
  UserRoundIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import { MobilePageHeader, MobileScrollArea } from "@/apps/mobile/mobile-page"

const categories = [
  { path: "employees", icon: UserRoundIcon },
  { path: "teams", icon: UsersRoundIcon },
  { path: "external", icon: ContactRoundIcon },
  { path: "assistants", icon: BotIcon },
] as const

/** 展示同事、团队、外部联系人和我的助理四个分类的固定入口。 */
export function MobileContactsPage() {
  const { t } = useTranslation(["mobile", "contacts"])
  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("tabs.contacts")} />
      <MobileScrollArea storageKey="contacts">
        <ul className="divide-y">
          {categories.map(({ path, icon: Icon }) => (
            <li key={path}>
              <Link
                to={`/contacts/${path}`}
                state={{ mobileBack: true }}
                className="flex min-h-16 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
              >
                <span className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
                  <Icon className="size-5" />
                </span>
                <span className="min-w-0 flex-1 text-[15px] font-medium">
                  {t(`contacts:scopes.${path}`)}
                </span>
                <ChevronRightIcon className="size-4 text-muted-foreground" />
              </Link>
            </li>
          ))}
        </ul>
      </MobileScrollArea>
    </section>
  )
}

