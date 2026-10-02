/** 应用内帮助侧栏：在当前页面旁显示产品文档正文，站内链接在侧栏内切换。 */
import { useEffect, useRef, useState, type MouseEvent } from "react"
import { ExternalLinkIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { getProductDocPage } from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import {
  parseProductDocsHref,
  productDocsLocale,
  productDocsPages,
  productDocsSlugPath,
  type ProductDocsPage,
} from "@/lib/product-docs"
import { openExternalURL } from "@/platform/external-navigation"
import { openProductDocsPath } from "@/platform/product-docs"
import "@/product-docs/content.css"

/** 打开登记的文档页面，page 为空时关闭；关闭后回到触发入口所在的页面状态。 */
export function ProductDocSheet({
  page,
  onClose,
}: {
  page: ProductDocsPage | null
  onClose: () => void
}) {
  const { t, i18n } = useTranslation("common")
  const locale = productDocsLocale(i18n.language)
  // 侧栏内跳转到的页面与锚点；为空时显示入口登记的页面。
  const [target, setTarget] = useState<{ slug: string; hash: string } | null>(null)
  const slug = target?.slug ?? (page ? productDocsPages[page] : "")
  const hash = target?.hash ?? ""
  const body = useRef<HTMLDivElement>(null)
  const resource = useResource(
    resourceKeys.productDocPage(locale, slug),
    (signal) => getProductDocPage({ locale, path: slug }, signal),
    { enabled: page !== null },
  )
  const doc = resource.data

  // 正文切换后定位到链接指定的标题，未指定时回到顶部。
  useEffect(() => {
    if (!doc) return
    const heading = hash ? body.current?.querySelector(`[id="${CSS.escape(hash)}"]`) : null
    if (heading) heading.scrollIntoView({ block: "start" })
    else body.current?.scrollIntoView({ block: "start" })
  }, [doc, hash])

  /** 拦截正文链接：标题锚点在侧栏内滚动，同语言文档页面在侧栏内切换，其余地址用系统浏览器打开。 */
  function openLink(event: MouseEvent<HTMLDivElement>) {
    const anchor = (event.target as Element).closest("a")
    const href = anchor?.getAttribute("href")
    if (!href) return
    event.preventDefault()
    if (href.startsWith("#")) {
      body.current?.querySelector(`[id="${CSS.escape(decodeURIComponent(href.slice(1)))}"]`)?.scrollIntoView({ block: "start" })
      return
    }
    const link = parseProductDocsHref(href)
    if (link && link.locale === locale) {
      setTarget({ slug: link.slug, hash: link.hash })
      return
    }
    void (href.startsWith("/") ? openProductDocsPath(href) : openExternalURL(href))
  }

  return (
    <Sheet
      open={page !== null}
      onOpenChange={(open) => {
        if (open) return
        setTarget(null)
        onClose()
      }}
    >
      <SheetContent className="w-full gap-0 p-0 sm:max-w-2xl">
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle>{doc?.title ?? t("productDocs")}</SheetTitle>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div ref={body} className="scroll-mt-6 p-6">
            <ResourceContent resources={resource} errorMessage={t("productDocsLoadError")}>
              {doc ? (
                <div className="docs-content" onClick={openLink} dangerouslySetInnerHTML={{ __html: doc.html }} />
              ) : null}
            </ResourceContent>
          </div>
        </ScrollArea>
        <SheetFooter className="border-t px-6 py-3">
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="self-start"
            onClick={() => void openProductDocsPath(`${doc?.path ?? productDocsSlugPath(locale, slug)}${hash ? `#${encodeURIComponent(hash)}` : ""}`)}
          >
            <ExternalLinkIcon />
            {t("openFullDocs")}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
