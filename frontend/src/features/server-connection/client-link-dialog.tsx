/** Web 端「在客户端中使用」对话框：打开桌面端或用手机扫码，客户端随即进入当前服务器的连接确认。 */
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useQRCode } from "@/hooks/use-qr-code"
import { useBrand } from "@/lib/brand"

/** 展示唤起桌面端的按钮和供移动端扫码的连接链接二维码。 */
export function ClientLinkDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation(["connection", "common"])
  const brand = useBrand()
  // Web 端页面来源即当前服务器的部署地址。
  const link = `${brand.linkScheme}://connect?server=${encodeURIComponent(window.location.origin)}`
  const qrCode = useQRCode(link, open)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("clientLink.title")}</DialogTitle>
          <DialogDescription>{t("clientLink.description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-6 text-sm">
          <section className="space-y-2">
            <p className="font-medium">{t("clientLink.desktop")}</p>
            <p className="text-muted-foreground">{t("clientLink.desktopHelp")}</p>
            <Button asChild className="mt-1">
              <a href={link}>{t("clientLink.openDesktop")}</a>
            </Button>
          </section>
          <section className="space-y-2">
            <p className="font-medium">{t("clientLink.mobile")}</p>
            <p className="text-muted-foreground">{t("clientLink.mobileHelp")}</p>
            {/* 二维码生成前保留同尺寸占位，生成后不改变布局；生成失败时在占位内提供重试。 */}
            {qrCode.dataURL ? (
              <img src={qrCode.dataURL} alt={t("clientLink.qrCodeAlt")} className="mt-1 size-40 rounded-md border" />
            ) : (
              <div className="mt-1 flex size-40 items-center justify-center rounded-md border bg-muted/30">
                {qrCode.failed ? (
                  <Button type="button" variant="outline" size="sm" onClick={qrCode.retry}>
                    {t("common:actions.retry")}
                  </Button>
                ) : null}
              </div>
            )}
          </section>
        </div>
      </DialogContent>
    </Dialog>
  )
}
