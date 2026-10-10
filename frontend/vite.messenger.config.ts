/** 单独打包访客正文组件，产物随 Go 服务端发布。 */
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import { sharedAliases } from "./vite.aliases.ts"

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: sharedAliases,
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
