/** 资料图片预览按钮和跨平台图片选择交互。 */
import { useRef, useState, type ChangeEvent } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { selectImage } from "@/api"
import { ProfileAvatar } from "@/components/profile-avatar"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"

const imageContentTypes = new Set(["image/jpeg", "image/png", "image/webp"])
const maxImageByteSize = 5 * 1024 * 1024
const imageFileAccept = ".jpg,.jpeg,.png,.webp,image/jpeg,image/png,image/webp"

/** 展示资料图片预览，点击后按平台选择 JPEG、PNG 或 WebP 图片并交给业务表单。 */
export function ImagePicker({
  imageURL,
  name,
  fallback,
  label,
  disabled = false,
  loading = false,
  className,
  avatarClassName,
  onSelect,
}: {
  imageURL?: string
  name?: string
  fallback: "person" | "agent" | "group"
  label: string
  disabled?: boolean
  loading?: boolean
  className?: string
  avatarClassName?: string
  onSelect: (file: File) => void
}) {
  const { t } = useTranslation("common")
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [selecting, setSelecting] = useState(false)

  /** 校验图片并把有效文件交给业务表单。 */
  function acceptImage(file: File) {
    if (!imageContentTypes.has(file.type)) {
      toast.error(t("image.typeError"))
      return
    }
    if (file.size <= 0 || file.size > maxImageByteSize) {
      toast.error(t("image.sizeError"))
      return
    }
    onSelect(file)
  }

  /** 处理 Web 文件选择器返回的图片。 */
  function selectBrowserImage(event: ChangeEvent<HTMLInputElement>) {
    const selected = event.target.files?.[0]
    event.target.value = ""
    if (selected) acceptImage(selected)
  }

  /** 桌面端使用原生选择器，其余平台使用文件输入。 */
  async function chooseImage() {
    if (resolveAppPlatform() !== "desktop") {
      fileInputRef.current?.click()
      return
    }
    setSelecting(true)
    try {
      const selected = await selectImage()
      if (!selected.name) return
      // 原生选择器返回 Base64 内容，转换为浏览器文件。
      const binary = window.atob(selected.dataBase64)
      const content = new Uint8Array(binary.length)
      for (let index = 0; index < binary.length; index += 1) {
        content[index] = binary.charCodeAt(index)
      }
      acceptImage(new File([content], selected.name, { type: selected.contentType }))
    } catch (error) {
      console.warn("选择资料图片失败", error)
      toast.error(t("image.chooseError"))
    } finally {
      setSelecting(false)
    }
  }

  const busy = selecting || loading

  return (
    <div>
      <input
        ref={fileInputRef}
        className="sr-only"
        type="file"
        accept={imageFileAccept}
        tabIndex={-1}
        aria-hidden="true"
        onChange={selectBrowserImage}
      />
      <button
        type="button"
        className={cn(
          "group relative size-20 overflow-hidden rounded-2xl outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60",
          className,
        )}
        aria-label={label}
        disabled={disabled || busy}
        onClick={() => void chooseImage()}
      >
        <ProfileAvatar
          imageURL={imageURL}
          name={name}
          fallback={fallback}
          className={cn("size-full", avatarClassName)}
        />
        {!disabled || busy ? (
          <span
            className={cn(
              "absolute inset-0 flex items-center justify-center bg-black/55 px-2 text-center text-xs font-medium text-white opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100",
              busy && "opacity-100",
            )}
          >
            {busy ? (
              <LoaderCircleIcon className="animate-spin" />
            ) : imageURL ? (
              t("image.change")
            ) : (
              label
            )}
          </span>
        ) : null}
      </button>
    </div>
  )
}
