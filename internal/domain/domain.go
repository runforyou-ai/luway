// Package domain 定义跨应用层共享的纯领域值（枚举、目录、无 IO 规则）。
//
// 禁止放入 API DTO、传输类型、持久化模型或 appservice 形状。
// 依赖方向由 layercheck 强制（仅可依赖 domain/common）；类型内容靠评审与本注释约束。
package domain
