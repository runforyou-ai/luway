/** 单独构建产品文档站点的样式与字体，产物随 Go 服务端发布。 */
import { defineConfig } from "vite"

export default defineConfig({
  base: "./",
  publicDir: false,
  build: {
    outDir: "../internal/productdocs/dist/site",
    emptyOutDir: true,
    rollupOptions: {
      input: "src/product-docs/site.css",
      output: {
        assetFileNames: (asset) =>
          asset.names.some((name) => name.endsWith(".css")) ? "site.css" : "fonts/[name]-[hash][extname]",
      },
    },
  },
})
