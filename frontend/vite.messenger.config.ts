/** 单独打包访客正文组件，产物随 Go 服务端发布。 */
import path from "path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      // 消息正文禁用原始 HTML，Streamdown 引用的 rehype-raw 以空插件替代。
      "rehype-raw": path.resolve(import.meta.dirname, "./src/lib/rehype-raw-disabled.ts"),
    },
  },
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: "../internal/publicweb/dist",
    emptyOutDir: true,
    lib: {
      entry: "src/publicweb/markdown.tsx",
      name: "MessengerMarkdown",
      formats: ["iife"],
      fileName: () => "markdown.js",
      cssFileName: "markdown",
    },
  },
})
