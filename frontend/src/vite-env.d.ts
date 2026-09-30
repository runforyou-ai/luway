/** Vite 客户端类型声明。 */
/// <reference types="vite/client" />

/** 构建配置中的应用版本。 */
declare const __APP_VERSION__: string
/** 构建品牌的产品名称、网站嵌入脚本对象名和唤起客户端的链接协议名。 */
declare const __BUILD_BRAND__: { names: Record<string, string>; sdkName: string; linkScheme: string }
