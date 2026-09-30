import fs from "fs";
import path from "path";
import tailwindcss from "@tailwindcss/vite";
import { load as loadYaml } from "js-yaml";
import { viteStaticCopy } from "vite-plugin-static-copy";
import { defineConfig, searchForWorkspaceRoot } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// 应用版本以构建配置的 info.version 为准。
const buildConfigPath = path.resolve(import.meta.dirname, "../build/config.yml");
const buildConfig = loadYaml(fs.readFileSync(buildConfigPath, "utf8")) as {
  info?: { version?: string };
};
const appVersion = buildConfig.info?.version ?? "";

// 构建品牌作为界面默认品牌，启动检测后由服务端下发的品牌替换。
const buildBrandPath = path.resolve(import.meta.dirname, "../internal/common/brand/brand.json");
const buildBrand = JSON.parse(fs.readFileSync(buildBrandPath, "utf8")) as {
  slug: string;
  names: Record<string, string>;
  sdkName: string;
};

export default defineConfig({
  base: "./",
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
    __BUILD_BRAND__: JSON.stringify({ names: buildBrand.names, sdkName: buildBrand.sdkName, linkScheme: buildBrand.slug }),
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
    fs: {
      allow: [
        searchForWorkspaceRoot(process.cwd()),
        path.resolve(import.meta.dirname, "../internal/publicweb/composer-emojis.json"),
      ],
    },
  },
  plugins: [react(), tailwindcss(), wails("./bindings"), viteStaticCopy({
    targets: ["cmaps", "standard_fonts", "wasm"].map((directory) => ({
      src: `node_modules/pdfjs-dist/${directory}/*`,
      dest: `pdfjs/${directory}`,
      rename: { stripBase: true },
    })),
  })],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
      // 消息正文禁用原始 HTML，Streamdown 引用的 rehype-raw 以空插件替代。
      "rehype-raw": path.resolve(import.meta.dirname, "./src/lib/rehype-raw-disabled.ts"),
    },
  },
});
