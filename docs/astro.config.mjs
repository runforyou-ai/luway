// 产品文档站点配置：站点名称取构建品牌，构建产物由服务端在 /docs/ 下提供。
import { readFileSync } from "node:fs"

import { unified } from "@astrojs/markdown-remark"
import starlight from "@astrojs/starlight"
import { defineConfig } from "astro/config"
import starlightLinksValidator from "starlight-links-validator"

import { remarkBrand } from "./src/plugins/remark-brand.mjs"

const brand = JSON.parse(
  readFileSync(new URL("../internal/common/brand/brand.json", import.meta.url), "utf8"),
)
const zhName = brand.names["zh-CN"] ?? brand.names["en-US"]
const enName = brand.names["en-US"]

export default defineConfig({
  base: "/docs",
  trailingSlash: "always",
  outDir: "../internal/productdocs/dist/site",
  // 开发预览端口被占用时直接失败。
  vite: { server: { strictPort: true } },
  // 与 Web 端共用构建品牌的站点图标。
  publicDir: "../frontend/public",
  markdown: {
    processor: unified({
      remarkPlugins: [[remarkBrand, { names: { "zh-cn": zhName, en: enName } }]],
    }),
  },
  integrations: [
    starlight({
      title: { "zh-CN": `${zhName}文档`, en: `${enName} Docs` },
      // 部署配置了品牌图标时由服务端替换。
      favicon: "/favicon.png",
      defaultLocale: "zh-cn",
      locales: {
        "zh-cn": { label: "简体中文", lang: "zh-CN" },
        en: { label: "English", lang: "en" },
      },
      sidebar: [
        {
          label: "使用手册",
          translations: { en: "User guide" },
          items: [{ autogenerate: { directory: "guide" } }],
        },
        {
          label: "接入指南",
          translations: { en: "Integrations" },
          items: [{ autogenerate: { directory: "integrations" } }],
        },
        {
          label: "部署与运维",
          translations: { en: "Deployment" },
          items: [{ autogenerate: { directory: "deployment" } }],
        },
        {
          label: "版本说明",
          translations: { en: "Release notes" },
          items: [{ autogenerate: { directory: "releases" } }],
        },
      ],
      plugins: [starlightLinksValidator()],
    }),
  ],
})
