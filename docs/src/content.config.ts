// 产品文档内容集合，使用 Starlight 的文档加载器与字段定义。
import { defineCollection } from "astro:content"
import { docsLoader } from "@astrojs/starlight/loaders"
import { docsSchema } from "@astrojs/starlight/schema"

export const collections = {
  docs: defineCollection({ loader: docsLoader(), schema: docsSchema() }),
}
