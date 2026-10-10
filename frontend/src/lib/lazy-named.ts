/** 按需加载模块中的具名组件。 */
import { lazy, type ComponentType } from "react"

/** 返回首次渲染时加载 load 模块并取其 name 导出的懒加载组件。 */
// biome-ignore lint/suspicious/noExplicitAny: 导出组件的属性类型由各模块决定，懒加载组件沿用其属性类型。
export function lazyNamed<M extends Record<K, ComponentType<any>>, K extends string>(load: () => Promise<M>, name: K) {
  return lazy(() => load().then((module) => ({ default: module[name] })))
}
