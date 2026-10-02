/** 知识文档的新增入口，包含上传批次及每个原件的进度。 */
import { useRef, useState } from "react"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { AttachmentName } from "@/components/attachment-name"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { formatFileSize } from "@/lib/file-size"
import { useReturnLink } from "@/hooks/use-return-to"
import { useKnowledgeDocumentUpload, knowledgeDocumentFormats } from "./use-knowledge-document-upload"
import { KnowledgeWebImportDialog } from "./knowledge-web-import-dialog"

/** 在文档列表中提供新增入口、显示批次进度并保留失败重试入口。 */
export function KnowledgeDocumentUpload({ baseId }: { baseId: string }) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const returnLink = useReturnLink()
  const picker = useRef<HTMLInputElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const [dragging, setDragging] = useState(false)
  const [importing, setImporting] = useState(false)
  const { open, busy, items, run, select, close, show } = useKnowledgeDocumentUpload({ baseId })
  const selected = items.length > 0
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            ref={trigger}
            variant="subtle"
            size="icon-sm"
            disabled={busy || open}
            aria-label={t("documents.create.action")}
            title={t("documents.create.action")}
          >
            <PlusIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={show}>
            {t("documents.create.upload")}
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link
              to={returnLink(`/knowledge-bases/${baseId}/documents/new`)}
            >
              {t("documents.create.write")}
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setImporting(true)}>
            {t("documents.create.import")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Dialog
        open={open}
        onOpenChange={(value) => {
          if (!value) close()
        }}
      >
        <DialogContent
          className="sm:max-w-xl"
          closeDisabled={busy}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            trigger.current?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>{t("documents.upload.action")}</DialogTitle>
          </DialogHeader>
          <div
            className={`rounded-md border-2 border-dashed p-6 text-center ${dragging ? "border-primary bg-accent" : "border-border"}`}
            onDragOver={(event) => {
              event.preventDefault()
              event.dataTransfer.dropEffect = selected ? "none" : "copy"
              if (!selected) setDragging(true)
            }}
            onDragLeave={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false)
            }}
            onDrop={(event) => {
              event.preventDefault()
              setDragging(false)
              select(Array.from(event.dataTransfer.files))
            }}
          >
            <p className="mb-3 text-sm">{t("documents.upload.drop")}</p>
            <Button variant="outline" disabled={selected} onClick={() => picker.current?.click()}>
              {t("documents.upload.choose")}
            </Button>
            <DialogDescription className="mt-3 text-xs leading-5">
              {t("documents.upload.unsupported")}
              <br />
              {t("documents.upload.description")}
            </DialogDescription>
            <input
              ref={picker}
              type="file"
              multiple
              accept={knowledgeDocumentFormats.join(",")}
              className="hidden"
              disabled={selected}
              aria-label={t("documents.upload.choose")}
              onChange={(event) => {
                select(Array.from(event.target.files ?? []))
                event.target.value = ""
              }}
            />
          </div>
          <div className="max-h-64 space-y-4 overflow-auto">
            {items.map((item, index) => (
              <div key={index} className="space-y-2">
                <div className="flex items-center justify-between gap-4 text-sm">
                  <span className="min-w-0 flex-1" title={item.file.name}>
                    <AttachmentName name={item.file.name} />
                  </span>
                  <span className="shrink-0 text-muted-foreground">{t(`documents.upload.${item.stage}`)}</span>
                </div>
                <div
                  role="progressbar"
                  className="h-1.5 w-full overflow-hidden rounded-full bg-primary/20"
                  aria-valuemin={0}
                  aria-valuemax={Math.max(1, item.file.size)}
                  aria-valuenow={item.stage === "saved" ? Math.max(1, item.file.size) : item.bytes}
                  aria-label={item.file.name}
                >
                  <div
                    className="h-full bg-primary"
                    style={{ width: `${item.stage === "saved" ? 100 : (item.bytes / Math.max(1, item.file.size)) * 100}%` }}
                  />
                </div>
                <p className="text-xs text-muted-foreground">{formatFileSize(item.file.size)}</p>
              </div>
            ))}
          </div>
          <div className="flex justify-end gap-3 pt-5">
            {!busy && items.some((item) => item.stage === "failed") && (
              <Button onClick={() => void run()}>{t("documents.upload.retry")}</Button>
            )}
            <Button variant="outline" disabled={busy} onClick={close}>
              {t("common:actions.close")}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <KnowledgeWebImportDialog
        baseId={baseId}
        open={importing}
        triggerRef={trigger}
        onClose={() => setImporting(false)}
      />
    </>
  )
}
