/** shadcn 轻提示。 */
import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { useTheme } from "next-themes"
import { useTranslation } from "react-i18next"
import { Toaster as Sonner, type ToasterProps } from "sonner"

const Toaster = ({
  toastOptions,
  style,
  closeButton = true,
  ...props
}: ToasterProps) => {
  const { theme = "system" } = useTheme()
  const { t } = useTranslation("common")

  return (
    <Sonner
      {...props}
      theme={theme as ToasterProps["theme"]}
      className="toaster group"
      closeButton={closeButton}
      containerAriaLabel={t("notifications")}
      toastOptions={{
        closeButtonAriaLabel: t("closeNotification"),
        ...toastOptions,
      }}
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
          ...style,
        } as React.CSSProperties
      }
    />
  )
}

export { Toaster }
