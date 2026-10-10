//go:build server

// Package integrationtest 用真实 PostgreSQL 覆盖跨 Action、应用服务和存储的业务流程。
//
// 测试共享当前 worktree 的测试数据库，按唯一业务键或测试内清理保持数据隔离；
// 企业安装相关测试使用本轮新建的空数据库。
package integrationtest
