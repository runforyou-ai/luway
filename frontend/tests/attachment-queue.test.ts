/** 验证真实上传执行器和附件队列的顺序发送、重试、取消及页面退出行为。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import { stripTypeScriptTypes } from "node:module"

const queueSource = readFileSync(
  new URL("../src/lib/attachment-queue.ts", import.meta.url),
  "utf8",
)
const queueCode =
  stripTypeScriptTypes(queueSource)
    .replace(
      /import\s*\{([\s\S]*?)\}\s*from\s*["']@\/api["'];?/,
      "const {$1} = api;",
    )
    .replace("export class AttachmentQueue", "class AttachmentQueue") +
  "\nexports.AttachmentQueue = AttachmentQueue;"
const storeSource = readFileSync(
  new URL("../src/lib/outgoing-message-store.ts", import.meta.url),
  "utf8",
)
const storeCode =
  stripTypeScriptTypes(storeSource).replace(/^export /gm, "") +
  "\nexports.OutgoingMessageStore = OutgoingMessageStore;"
const uploadSource = readFileSync(
  new URL("../src/api/uploads.ts", import.meta.url),
  "utf8",
)
const transferCode =
  stripTypeScriptTypes(
    uploadSource.slice(
      uploadSource.indexOf("export class FileTransfer"),
      uploadSource.indexOf("/** 上传一个文件片段"),
    ),
  ).replace("export class FileTransfer", "class FileTransfer") +
  "\nexports.FileTransfer = FileTransfer;"

/** 用可控制的请求运行实际上传执行器、附件队列和发送状态存储。 */
function host(overrides: Record<string, (...args: any[]) => any> = {}) {
  const sends: any[] = []
  const cancelled: string[] = []
  const errors: unknown[] = []
  let transfers = 0
  const customerSends: any[] = []
  const api: Record<string, any> = {
    FilePurpose: { FilePurposeMessageAttachment: "message_attachment" },
    MessageVisibility: { MessageVisibilityShared: "shared" },
    MessageAttachmentTransferStatus: {
      MessageAttachmentTransferReady: "ready",
      MessageAttachmentTransferPending: "pending",
      MessageAttachmentTransferFailed: "failed",
    },
    sendServiceAttachmentMessage: async (conversationId: string, input: any) => {
      customerSends.push({ conversationId, ...input })
      return {
        id: `saved-${input.clientMessageId}`,
        body: input.body,
        originatedAt: new Date().toISOString(),
        attachment: { id: input.fileId, transferStatus: "ready" },
      }
    },
    createFileUpload: async (input: any) => ({
      file: { id: `file-${input.fileName}` },
      partSize: 0,
      request: {},
    }),
    createFilePartUpload: async () => ({}),
    uploadFileSlice: async () => {
      transfers++
    },
    completeFileUpload: async (id: string) => ({ id }),
    cancelFileUpload: async (id: string) => {
      cancelled.push(id)
    },
    sendAttachmentMessage: async (input: any) => {
      sends.push(input)
      return {
        conversationId: input.conversationId || "created",
        conversation: input.conversationId ? null : { id: "created" },
        message: {
          id: `saved-${input.clientMessageId}`,
          body: input.body,
          originatedAt: new Date().toISOString(),
          attachment: { id: input.fileId },
        },
      }
    },
    ...overrides,
  }
  const exports: Record<string, any> = {}
  runInNewContext(transferCode, {
    exports,
    createFilePartUpload: (...args: any[]) => api.createFilePartUpload(...args),
    uploadFileSlice: (...args: any[]) => api.uploadFileSlice(...args),
  })
  api.FileTransfer = exports.FileTransfer
  runInNewContext(queueCode, {
    exports,
    api,
    crypto,
    AbortController,
    URL,
    requestAnimationFrame: (callback: () => void) => setTimeout(callback, 0),
    cancelAnimationFrame: clearTimeout,
    console: { warn() {} },
  })
  const storeExports: Record<string, any> = {}
  runInNewContext(storeCode, {
    exports: storeExports,
    setTimeout,
    clearTimeout,
    Map,
    Set,
  })
  const outgoing = new storeExports.OutgoingMessageStore()
  const queue = new exports.AttachmentQueue(
    outgoing,
    () => {},
    (error: unknown) => errors.push(error),
  )
  // 记录入队的任务对象，出队后仍可断言其最终状态。
  const recorded = new Map<string, any>()
  const enqueue = queue.enqueue.bind(queue)
  queue.enqueue = (...args: any[]) => {
    enqueue(...args)
    for (const item of queue.jobs) recorded.set(item.id, item)
  }
  const files = [1, 2].map((index) => ({
    id: `message-${index}`,
    body: index === 2 ? "说明" : "",
    file: new File(["contents"], `${index}.csv`),
    previewURL: "",
    imageWidth: 0,
    imageHeight: 0,
  }))
  return {
    queue,
    /** 模拟正式会话挂载时由共享 hook 移交草稿发送项。 */
    adopt: (draftID: string, conversationID: string) => outgoing.adopt(draftID, conversationID),
    /** 返回指定会话分组内的发送项。 */
    sent: (conversationID = "conversation") =>
      outgoing.snapshot().get(conversationID) ?? [],
    /** 按编号返回入队过的附件任务。 */
    job: (id: string) => recorded.get(id),
    /** 返回仍在队列中的附件任务。 */
    pending: (): any[] => queue.jobs,
    files,
    sends,
    customerSends,
    cancelled,
    errors,
    transfers: () => transfers,
  }
}

/** 等待异步队列达到目标状态。 */
async function settled(predicate: () => boolean) {
  for (let index = 0; index < 100; index++) {
    if (predicate()) return
    await new Promise((resolve) => setImmediate(resolve))
  }
  assert.fail("队列未达到预期状态")
}

test("后选的文件先上传完成时，等前面的附件发送后再按选择顺序发送", async () => {
  const gate = Promise.withResolvers<void>()
  const h = host({
    completeFileUpload: async (id: string) => {
      if (id === "file-1.csv") await gate.promise
      return { id }
    },
  })
  h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
  await settled(() => h.job("message-2").stage === "uploaded")
  assert.equal(h.sends.length, 0)
  gate.resolve()
  await settled(() => h.job("message-2").stage === "sent")
  assert.deepEqual(
    h.sends.map((item) => [item.clientMessageId, item.fileId, item.body]),
    [
      ["message-1", "file-1.csv", ""],
      ["message-2", "file-2.csv", "说明"],
    ],
  )
  assert.equal(h.sent().map((item: any) => item.status).join(","), "sent,sent")
})

test("前面的附件上传失败时不阻塞后续附件，重试成功后排在最后发送", async () => {
  let failFirst = true
  const h = host({
    completeFileUpload: async (id: string) => {
      if (id === "file-1.csv" && failFirst) {
        failFirst = false
        throw new Error("上传失败")
      }
      return { id }
    },
  })
  h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
  await settled(() => h.job("message-2").stage === "sent")
  assert.equal(h.job("message-1").stage, "failed")
  assert.equal(h.sent()[0].status, "failed")
  h.queue.retry("message-1")
  await settled(() => h.job("message-1").stage === "sent")
  assert.deepEqual(
    h.sends.map((item) => item.clientMessageId),
    ["message-2", "message-1"],
  )
})

test("同批后续附件仍在上传时重试失败的附件，重试的附件排在后续附件之后发送", async () => {
  const gate = Promise.withResolvers<void>()
  let failFirst = true
  const h = host({
    completeFileUpload: async (id: string) => {
      if (id === "file-1.csv" && failFirst) {
        failFirst = false
        throw new Error("上传失败")
      }
      if (id === "file-2.csv") await gate.promise
      return { id }
    },
  })
  h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
  await settled(() => h.job("message-1").stage === "failed")
  h.queue.retry("message-1")
  await settled(() => h.job("message-1").stage === "uploaded")
  assert.equal(h.sends.length, 0)
  gate.resolve()
  await settled(() => h.job("message-1").stage === "sent")
  assert.deepEqual(
    h.sends.map((item) => item.clientMessageId),
    ["message-2", "message-1"],
  )
})

test("发送失败后重试沿用同一发送编号和已上传的文件", async () => {
  let attempts = 0
  const q = host({
    sendAttachmentMessage: async (input: any) => {
      attempts++
      if (attempts === 1) throw new Error("响应丢失")
      return { conversationId: "conversation", conversation: null, message: { id: "saved", body: input.body, originatedAt: new Date().toISOString(), attachment: { id: input.fileId } } }
    },
  })
  q.queue.enqueue(q.files.slice(1), { conversationID: "conversation" }, () => {})
  await settled(() => q.job("message-2").stage === "failed")
  assert.equal(q.errors.length, 1)
  const transfers = q.transfers()
  q.queue.retry("message-2")
  await settled(() => q.job("message-2").stage === "sent")
  assert.equal(attempts, 2)
  assert.equal(q.transfers(), transfers)
  assert.equal(q.sent()[0].saved.body, "说明")
})

test("失败分片重试跳过已成功的片", async () => {
  const parts: number[] = []
  let fail = true
  let current = 0
  const h = host({
    createFileUpload: async () => ({ file: { id: "file" }, partSize: 2, request: {} }),
    createFilePartUpload: async (_id: string, input: { partNumber: number }) => {
      current = input.partNumber
      return {}
    },
    uploadFileSlice: async () => {
      parts.push(current)
      if (current === 2 && fail) {
        fail = false
        throw new Error("分片失败")
      }
    },
  })
  h.queue.enqueue(h.files.slice(0, 1), { conversationID: "conversation" }, () => {})
  await settled(() => h.job("message-1").stage === "failed")
  h.queue.retry("message-1")
  await settled(() => h.job("message-1").stage === "sent")
  assert.deepEqual(parts, [1, 2, 2, 3, 4])
})

test("取消上传中的附件会释放临时文件并放行同批后续附件", async () => {
  const gate = Promise.withResolvers<void>()
  let entered = false
  const h = host({
    completeFileUpload: async (id: string) => {
      if (id === "file-1.csv") {
        entered = true
        await gate.promise
      }
      return { id }
    },
  })
  h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
  await settled(() => entered && h.job("message-2").stage === "uploaded")
  h.queue.cancel("message-1")
  await settled(() => h.job("message-2").stage === "sent")
  gate.resolve()
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(h.job("message-1").stage, "cancelled")
  assert.deepEqual(h.cancelled, ["file-1.csv"])
  assert.deepEqual(h.sends.map((item) => item.clientMessageId), ["message-2"])
  assert.equal(h.sent().length, 1)
})

test("草稿首发后后续附件改用正式会话，只回调一次", async () => {
  const h = host()
  const created: unknown[] = []
  h.queue.enqueue(h.files, { conversationID: "", targetIdentityID: "peer" }, (conversation: unknown) => created.push(conversation))
  await settled(() => h.job("message-2").stage === "sent")
  assert.equal(h.sends[0].targetIdentityId, "peer")
  assert.equal(h.sends[0].conversationId, "")
  assert.equal(h.sends[1].targetIdentityId, "")
  assert.equal(h.sends[1].conversationId, "created")
  assert.deepEqual(created, [{ id: "created" }])
  assert.equal(h.job("message-1").conversationID, "created")
})

test("首发成功后保留草稿气泡，正式会话挂载时接管仍在上传的附件", async () => {
  const gate = Promise.withResolvers<void>()
  const h = host({
    completeFileUpload: async (id: string) => {
      if (id === "file-2.csv") await gate.promise
      return { id }
    },
  })
  h.queue.enqueue(h.files, { conversationID: "", targetIdentityID: "peer" }, () => {})
  await settled(() => h.job("message-1").stage === "sent")
  assert.equal(h.sent("draft:peer").length, 2)
  assert.equal(h.sent("created").length, 0)
  assert.equal(h.job("message-2").conversationID, "created")
  h.adopt("draft:peer", "created")
  assert.equal(h.sent("draft:peer").length, 0)
  assert.equal(h.sent("created").length, 2)
  assert.equal(h.sent("created")[1].status, "sending")
  assert.equal(h.job("message-2").conversationID, "created")
  gate.resolve()
  await settled(() => h.job("message-2").stage === "sent")
  assert.equal(h.sent("created").map((item: any) => item.status).join(","), "sent,sent")
})

test("失权时清除附件，迟到的发送结果不能恢复队列或打开详情", async () => {
  const gate = Promise.withResolvers<void>()
  let entered = false
  const h = host({
    sendAttachmentMessage: async (input: any) => {
      entered = true
      await gate.promise
      return { conversationId: "removed", conversation: null, message: { id: "saved", body: input.body, originatedAt: new Date().toISOString(), attachment: { id: input.fileId } } }
    },
  })
  let opened = 0
  h.queue.enqueue(h.files, { conversationID: "removed" }, () => {
    opened++
  })
  await settled(() => entered && h.job("message-2").stage === "uploaded")
  h.queue.forgetConversation("removed")
  gate.resolve()
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(h.pending().length, 0)
  assert.equal(h.sent("removed").length, 0)
  assert.equal(opened, 0)
  assert.equal(h.errors.length, 0)
  // 失权时释放尚未确认发送的临时文件。
  assert.deepEqual([...h.cancelled].sort(), ["file-1.csv", "file-2.csv"])
})

test("发送进行中离开页面，迟到的成功结果不改写失败状态，后续附件不再发送", async () => {
  const gate = Promise.withResolvers<void>()
  let entered = false
  const h = host({
    sendAttachmentMessage: async (input: any) => {
      entered = true
      await gate.promise
      return { conversationId: "conversation", conversation: null, message: { id: "saved", body: input.body, originatedAt: new Date().toISOString(), attachment: { id: input.fileId } } }
    },
  })
  h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
  await settled(() => entered && h.job("message-2").stage === "uploaded")
  h.queue.dispose()
  gate.resolve()
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(h.job("message-1").stage, "failed")
  assert.equal(h.sent().map((item: any) => item.status).join(","), "failed,failed")
})

test("离开页面后未发送的附件标记失败且不再发送", async () => {
  const gate = Promise.withResolvers<void>()
  const h = host({
    uploadFileSlice: async () => {
      await gate.promise
    },
  })
  h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
  await settled(() => h.pending().every((item: any) => item.transfer?.upload))
  h.queue.dispose()
  gate.resolve()
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(h.sends.length, 0)
  assert.equal(h.sent().map((item: any) => item.status).join(","), "failed,failed")
  assert.deepEqual([...h.cancelled].sort(), ["file-1.csv", "file-2.csv"])
})

test("客户会话附件走对客发送接口，引用只挂在首条附件上", async () => {
  const context = host()
  context.queue.start()
  context.queue.enqueue(
    context.files,
    {
      conversationID: "conversation",
      customer: true,
      replyTo: { id: "origin-message", body: "客户原话", deleted: false },
    },
    () => {},
  )
  await settled(() => context.customerSends.length === 2)
  assert.equal(context.sends.length, 0, "不应调用内部附件接口")
  assert.deepEqual(
    context.customerSends.map((item) => [item.conversationId, item.replyToMessageId, item.body]),
    [
      ["conversation", "origin-message", ""],
      ["conversation", "", "说明"],
    ],
  )
  const localReplies = context.sent().map((item: any) => item.replyTo?.id ?? "")
  assert.equal(localReplies.length, 2)
  assert.equal(localReplies[0], "origin-message", "首条本地气泡保留引用")
  assert.equal(localReplies[1], "", "其余附件不重复引用")
})

test("发送成功的文件出队，图片在释放或离开页面时出队并释放本地预览，状态未变时沿用同一视图", async () => {
  const revoked: string[] = []
  const h = host()
  const originalRevoke = URL.revokeObjectURL
  URL.revokeObjectURL = (url: string) => {
    revoked.push(url)
  }
  try {
    h.files[0].previewURL = "blob:image-1"
    h.queue.enqueue(h.files, { conversationID: "conversation" }, () => {})
    await settled(() => h.job("message-1").stage === "sent" && h.job("message-2").stage === "sent")
    assert.equal(h.pending().map((item: any) => item.id).join(","), "message-1")
    assert.equal(h.queue.batches.size, 0)
    const view = h.queue.find("saved-message-1", "message-1")
    assert.equal(view.previewURL, "blob:image-1")
    assert.equal(h.queue.find("saved-message-1", "message-1"), view)
    assert.equal(h.queue.find("saved-message-2", "message-2"), undefined)
    h.queue.dispose()
    assert.deepEqual(revoked, ["blob:image-1"])
    assert.equal(h.pending().length, 0)
  } finally {
    URL.revokeObjectURL = originalRevoke
  }
})
