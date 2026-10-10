/** 应用构建共用的编译期常量、插件与模块别名。 */
import fs from "fs";
import path from "path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { load as loadYaml } from "js-yaml";
import type { UserConfig } from "vite";
import { viteStaticCopy } from "vite-plugin-static-copy";
import { sharedAliases } from "./vite.aliases.ts";

/** 返回应用构建共用的配置：应用版本与构建品牌常量、React 与 Tailwind 插件、PDF 渲染资源复制与源码别名；modules 是依赖安装目录。 */
export function appBuildConfig(modules: string): UserConfig {
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
    serverURL?: string;
  };

  return {
    base: "./",
    define: {
      __APP_VERSION__: JSON.stringify(appVersion),
      __BUILD_BRAND__: JSON.stringify({ names: buildBrand.names, sdkName: buildBrand.sdkName, linkScheme: buildBrand.slug, serverURL: (buildBrand.serverURL ?? "").replace(/\/+$/, "") }),
    },
    plugins: [react(), tailwindcss(), viteStaticCopy({
      targets: ["cmaps", "standard_fonts", "wasm"].map((directory) => ({
        src: `${modules}/pdfjs-dist/${directory}/*`,
        dest: `pdfjs/${directory}`,
        rename: { stripBase: true },
      })),
    })],
    resolve: {
      alias: sharedAliases,
    },
  };
}
