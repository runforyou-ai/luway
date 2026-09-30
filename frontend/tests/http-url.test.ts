/** 验证共享地址校验保留网页与服务端点的业务边界。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { isHTTPEndpoint, isHTTPURL } from "../src/lib/http-url.ts"

test("网页允许查询与片段，服务端点只接受纯地址", () => {
  for (const value of ["https://example.com", "http://localhost:8084/api", "https://例子.测试/v1/"]) {
    assert.equal(isHTTPURL(value), true)
    assert.equal(isHTTPEndpoint(value), true)
  }
  for (const value of ["https://example.com/?q=1", "https://example.com/#section"]) {
    assert.equal(isHTTPURL(value), true)
    assert.equal(isHTTPEndpoint(value), false)
  }
})

test("两种地址均拒绝相对路径、其他协议和认证信息", () => {
  for (const value of ["", "/api", "example.com", "file:///tmp/a", "javascript:alert(1)", "ftp://example.com", "https://user:secret@example.com", "http://user@example.com"]) {
    assert.equal(isHTTPURL(value), false, value)
    assert.equal(isHTTPEndpoint(value), false, value)
  }
})
