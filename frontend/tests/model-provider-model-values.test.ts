/** 验证模型目录被服务端拒绝时的回滚规则。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { stripTypeScriptTypes } from "node:module"
import { test } from "node:test"
import { runInNewContext } from "node:vm"

const source =
  stripTypeScriptTypes(
    readFileSync(
      new URL(
        "../src/features/integrations/model-services/model-provider-model-values.ts",
        import.meta.url,
      ),
      "utf8",
    ),
  ).replace(/export function/g, "function") +
  "\nexports.rollbackModels = rollbackModels"
const module: Record<string, any> = {}
runInNewContext(source, { exports: module })

type Model = { key: string; identifier: string; name: string }
const rollback = (saved: Model[], requested: Model[], current: Model[]) =>
  JSON.parse(JSON.stringify(module.rollbackModels(saved, requested, current)))

const a = { key: "ka", identifier: "a", name: "A" }
const b = { key: "kb", identifier: "b", name: "B" }
const c = { key: "kc", identifier: "c", name: "C" }

test("撤回请求中的删除，保留请求发出后对其他模型的修改", () => {
  const editedB = { key: "kb", identifier: "b", name: "B2" }
  assert.deepEqual(rollback([a, b], [b], [editedB]), [a, editedB])
})

test("撤回请求中的新增和修改，保留请求发出后新增的模型", () => {
  const renamedA = { key: "ka", identifier: "a", name: "A2" }
  assert.deepEqual(rollback([a], [renamedA, b], [renamedA, b, c]), [a, c])
})

test("请求发出后又改过的模型保留当前值", () => {
  const requestedA = { key: "ka", identifier: "a", name: "A2" }
  const currentA = { key: "ka", identifier: "a", name: "A3" }
  assert.deepEqual(rollback([a], [requestedA], [currentA]), [currentA])
})

test("请求中删除后又重新添加的模型不重复补回", () => {
  const readdedA = { key: "kd", identifier: "a", name: "A-new" }
  assert.deepEqual(rollback([a, b], [b], [b, readdedA]), [b, readdedA])
})

test("请求发出后又修改了上游标识的模型保留当前值，不补回旧记录", () => {
  const requestedA = { key: "ka", identifier: "a2", name: "A2" }
  const currentA = { key: "ka", identifier: "a3", name: "A3" }
  assert.deepEqual(rollback([a, b], [requestedA, b], [currentA, b]), [currentA, b])
})

test("撤回请求中对上游标识的修改", () => {
  const renamedA = { key: "ka", identifier: "a2", name: "A" }
  assert.deepEqual(rollback([a], [renamedA], [renamedA]), [a])
})
