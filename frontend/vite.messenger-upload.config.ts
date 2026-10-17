/** 打包网站 Messenger 随页加载的正式上传 IIFE。 */
import { defineConfig } from "vite"

export default defineConfig({
  build: {
    outDir: "../internal/web/messenger/dist",
    emptyOutDir: false,
    lib: {
      entry: "src/publicweb/upload.ts",
      name: "MessengerUpload",
      formats: ["iife"],
      fileName: () => "upload.js",
    },
  },
})
