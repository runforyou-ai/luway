/** 打包网站 Messenger 懒加载的正式实时 SDK。 */
import { defineConfig } from "vite"

export default defineConfig({
  build: {
    outDir: "../internal/publicweb/dist",
    emptyOutDir: false,
    lib: {
      entry: "src/publicweb/realtime.ts",
      name: "MessengerRealtime",
      formats: ["iife"],
      fileName: () => "realtime.js",
    },
  },
})
