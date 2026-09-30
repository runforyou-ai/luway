/** 首次安装页。 */
import { SetupForm } from "@/features/installation/setup-form"
import { useBrandName } from "@/lib/brand"

/** 展示首次安装表单。 */
export function SetupPage() {
  const productName = useBrandName()
  return (
    <main className="flex min-h-svh w-full items-center justify-center p-6 md:p-10">
      <div className="w-full max-w-sm">
        <div className="mb-6 text-center">
          <p className="text-lg font-semibold tracking-tight">{productName}</p>
        </div>
        <SetupForm />
      </div>
    </main>
  )
}
