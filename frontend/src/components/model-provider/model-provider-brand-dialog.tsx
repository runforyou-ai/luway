/** 添加模型服务供应商时选择品牌的弹窗。 */
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import type { AIProviderBrand } from "@/api"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { ModelProviderBrandIcon } from "@/components/model-provider/model-provider-brand-icon"
import {
  aiProviderBrandConfigs,
  aiProviderBrandOrder,
} from "@/components/model-provider/model-provider-brands"

/** 以卡片展示可接入的品牌，选中后进入 createPath 给出的该品牌添加页。 */
export function ModelProviderBrandDialog({
  open,
  onOpenChange,
  createPath,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  createPath: (brand: AIProviderBrand) => string
}) {
  const { t } = useTranslation("integrations")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("modelServices.brandDialog.title")}</DialogTitle>
          <DialogDescription>
            {t("modelServices.brandDialog.description")}
          </DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {aiProviderBrandOrder.map((brand) => {
            const name = t(aiProviderBrandConfigs[brand].nameKey)
            return (
              <Link
                key={brand}
                to={createPath(brand)}
                className="flex min-w-0 items-center gap-3 rounded-lg border bg-card p-4 transition-colors hover:bg-accent/50 focus-visible:border-primary focus-visible:outline-hidden"
              >
                <ModelProviderBrandIcon brand={brand} />
                <span className="truncate text-sm font-medium">{name}</span>
              </Link>
            )
          })}
        </div>
      </DialogContent>
    </Dialog>
  )
}
