/** shadcn 对话框。 */
import type * as React from "react"
import { XIcon } from "lucide-react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { useTranslation } from "react-i18next"

import { cn } from "@/lib/utils"
import { usePortalContainer } from "@/components/ui/portal-container"
import { focusContainerWithoutControls } from "@/lib/dialog-focus"

function Dialog(props: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />
}

function DialogPortal({
  container,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Portal>) {
  const pagePortal = usePortalContainer()
  if (pagePortal && !pagePortal.active) {
    return null
  }

  return (
    <DialogPrimitive.Portal
      data-slot="dialog-portal"
      container={container ?? pagePortal?.container}
      {...props}
    />
  )
}

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 z-50 bg-black/50 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0",
        className,
      )}
      {...props}
    />
  )
}

function DialogContent({
  className,
  children,
  closeDisabled = false,
  closeButtonClassName,
  onOpenAutoFocus,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & { closeDisabled?: boolean; closeButtonClassName?: string }) {
  const { t } = useTranslation("common")

  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "app-form fixed top-1/2 left-1/2 z-50 grid outline-hidden max-h-[calc(100svh-2rem)] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 gap-3.5 overflow-y-auto rounded-lg border bg-background p-5 shadow-lg data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95",
          className,
        )}
        onOpenAutoFocus={(event) => {
          onOpenAutoFocus?.(event)
          focusContainerWithoutControls(event)
        }}
        {...props}
      >
        {children}
        <DialogPrimitive.Close data-dialog-close disabled={closeDisabled} className={cn("absolute top-2.5 right-2.5 inline-flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus:outline-hidden", closeButtonClassName)}>
          <XIcon className="size-4" />
          <span className="sr-only">{t("actions.close")}</span>
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-1.5", className)} {...props} />
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn("text-lg font-semibold", className)} {...props} />
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn("text-sm text-muted-foreground", className)} {...props} />
}

export { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle }
