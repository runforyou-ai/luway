/** 验证 Zod 表单解析器为顶层、数组与嵌套对象字段写入和清除浏览器原生校验提示，并只在正在编辑的字段或第一个无效字段弹出提示。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { JSDOM } from "jsdom"

const dom = new JSDOM("<!doctype html><div id=root></div>")
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  IS_REACT_ACT_ENVIRONMENT: true,
})
const React = await import("react")
const { createRoot } = await import("react-dom/client")
const { Controller, useFieldArray, useForm } = await import("react-hook-form")
const { z } = await import("zod")
const { zodResolver } = await import("../src/lib/zod-resolver.ts")

const schema = z.object({
  title: z.string().min(1, "请填写标题"),
  settings: z.object({ note: z.string().max(3, "备注过长") }),
  periods: z.array(z.object({ start: z.string(), end: z.string() })).superRefine((items, context) => {
    items.forEach((item, index) => {
      if (item.end <= item.start) context.addIssue({ code: "custom", path: [index, "end"], message: "结束须晚于开始" })
      if (index > 0 && item.start < items[index - 1].end) context.addIssue({ code: "custom", path: [index, "start"], message: "时段重叠" })
    })
  }),
})
type Values = z.input<typeof schema>

/** 渲染带顶层、嵌套对象与数组字段的表单，返回按编号取输入框和修改输入值的方法。 */
async function renderForm() {
  let form: ReturnType<typeof useForm<Values>> | undefined
  /** 数组字段：每个时段一个结束时间输入框。 */
  function Periods({ control }: { control: NonNullable<typeof form>["control"] }) {
    const { fields } = useFieldArray({ control, name: "periods" })
    const names = fields.flatMap((_, index) => [`periods.${index}.start`, `periods.${index}.end`] as const)
    return fields.flatMap((item, index) =>
      (["start", "end"] as const).map((key) =>
        React.createElement(Controller<Values>, {
          key: `${item.id}-${key}`,
          control,
          name: `periods.${index}.${key}`,
          rules: { deps: names },
          render: ({ field }) => React.createElement("input", { ...field, id: `${key}-${index}`, value: String(field.value) }),
        }),
      ),
    )
  }
  /** 使用原生校验提示的测试表单。 */
  function Form() {
    form = useForm<Values>({
      resolver: zodResolver(schema),
      shouldUseNativeValidation: true,
      mode: "onChange",
      defaultValues: {
        title: "标题",
        settings: { note: "" },
        periods: [
          { start: "09:00", end: "12:00" },
          { start: "13:00", end: "18:00" },
        ],
      },
    })
    return React.createElement(
      "form",
      null,
      React.createElement("input", { ...form.register("title"), id: "title" }),
      React.createElement("input", { ...form.register("settings.note"), id: "note" }),
      React.createElement(Periods, { control: form.control }),
      React.createElement("button", { type: "button", id: "action" }),
    )
  }
  const root = createRoot(document.getElementById("root")!)
  await React.act(() => root.render(React.createElement(Form)))
  const reported: string[] = []
  const input = (id: string) => {
    const element = document.getElementById(id) as HTMLInputElement
    element.reportValidity = () => {
      reported.push(id)
      return element.checkValidity()
    }
    return element
  }
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!
  /** 以用户输入方式聚焦并修改输入框，等待校验完成。 */
  async function change(id: string, value: string) {
    const element = input(id)
    for (const other of document.querySelectorAll("input")) input(other.id)
    element.focus()
    await React.act(async () => {
      setter.call(element, value)
      element.dispatchEvent(new dom.window.Event("input", { bubbles: true }))
    })
    await React.act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }
  return { input, change, reported, form: () => form!, unmount: () => React.act(() => root.unmount()) }
}

test("数组与嵌套对象字段出错时写入原生提示，改正后清除", async () => {
  const view = await renderForm()
  await view.change("end-0", "08:00")
  assert.equal(view.input("end-0").validationMessage, "结束须晚于开始")
  await view.change("end-0", "19:00")
  assert.equal(view.input("end-0").validationMessage, "")
  await view.change("note", "超过三个字")
  assert.equal(view.input("note").validationMessage, "备注过长")
  await view.change("note", "短")
  assert.equal(view.input("note").validationMessage, "")
  await view.unmount()
})

test("顶层字段沿用原生提示", async () => {
  const view = await renderForm()
  await view.change("title", "")
  assert.equal(view.input("title").validationMessage, "请填写标题")
  await view.change("title", "新标题")
  assert.equal(view.input("title").validationMessage, "")
  await view.unmount()
})

test("跨字段错误只写入依赖字段，不弹出提示也不移动焦点", async () => {
  const view = await renderForm()
  await view.change("end-0", "14:00")
  assert.equal(view.input("start-1").validationMessage, "时段重叠")
  assert.equal(view.input("end-0").validationMessage, "")
  assert.deepEqual(view.reported, [])
  assert.equal(document.activeElement, view.input("end-0"))
  await view.change("end-0", "12:30")
  assert.equal(view.input("start-1").validationMessage, "")
  await view.unmount()
})

test("焦点不在参与校验的字段上时只弹出第一个无效字段", async () => {
  const view = await renderForm()
  await view.change("title", "")
  await view.change("note", "超过三个字")
  view.reported.length = 0
  ;(document.getElementById("action") as HTMLButtonElement).focus()
  await React.act(async () => {
    await view.form().trigger()
  })
  assert.deepEqual(view.reported, ["title"])
  assert.equal(view.input("note").validationMessage, "备注过长")
  await view.unmount()
})
