/** 把原始文档转换为内容阅读所需的文本、HTML、工作表或 PDF 数据。 */
import DOMPurify from "dompurify"

/** 文档预览内容：按格式给出文本、HTML、工作表或 PDF 数据。 */
export type DocumentPreview =
  | { kind: "unsupported" }
  | { kind: "text" | "markdown"; content: string }
  | { kind: "html"; content: string }
  | { kind: "sheets"; sheets: { name: string; content: string }[] }
  | { kind: "pdf"; content: Uint8Array<ArrayBuffer> }

/** 读取带 BOM 的 Unicode 文本，并识别常见中文文件编码。 */
export function decodeDocumentText(bytes: Uint8Array): string {
  if (bytes[0] === 0xff && bytes[1] === 0xfe) return new TextDecoder("utf-16le").decode(bytes)
  if (bytes[0] === 0xfe && bytes[1] === 0xff) return new TextDecoder("utf-16be").decode(bytes)
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes)
  } catch {
    return new TextDecoder("gb18030").decode(bytes)
  }
}

/** 清理文档 HTML，并在独立沙箱内使用统一的阅读排版。 */
function previewHTML(content: string): string {
  const clean = DOMPurify.sanitize(content, {
    WHOLE_DOCUMENT: false,
    FORBID_TAGS: ["style", "form", "input", "button", "textarea", "select", "iframe", "object", "embed", "svg", "math"],
    FORBID_ATTR: ["style", "srcset"],
  })
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: https: http:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><style>
    body{font:15px/1.8 system-ui,sans-serif;color:#202124;background:#fff;margin:0;padding:32px;overflow-wrap:anywhere}
    h1,h2,h3,h4{line-height:1.4}p{margin:0 0 1em}table{border-collapse:collapse;max-width:100%;font-size:14px}td,th{border:1px solid #ddd;padding:8px 12px;vertical-align:top;min-width:60px}th{background:#f7f7f7}img{max-width:100%;height:auto}pre{white-space:pre-wrap}a{color:#2563eb}blockquote{border-left:3px solid #ddd;padding-left:16px;margin-left:0}
  </style></head><body>${clean}</body></html>`
}

/** 根据允许上传的原文件扩展名解析预览内容。 */
export async function parseDocumentPreview(name: string, bytes: Uint8Array<ArrayBuffer>): Promise<DocumentPreview> {
  const extension = name.split(".").pop()?.toLowerCase()
  switch (extension) {
    case "pdf":
      return { kind: "pdf", content: bytes }
    case "md":
    case "markdown":
      return { kind: "markdown", content: decodeDocumentText(bytes) }
    case "txt":
    case "json":
      return { kind: "text", content: decodeDocumentText(bytes) }
    case "html":
    case "htm":
      return { kind: "html", content: previewHTML(decodeDocumentText(bytes)) }
    case "docx": {
      const { default: mammoth } = await import("mammoth/mammoth.browser.js")
      const result = await mammoth.convertToHtml({ arrayBuffer: bytes.buffer })
      return { kind: "html", content: previewHTML(result.value) }
    }
    case "xlsx":
    case "csv": {
      // 精简版只含 XLSX 与 CSV 等常用格式，预览只需读取工作表。
      const { default: xlsx } = await import("xlsx/dist/xlsx.mini.min.js")
      const workbook =
        extension === "csv"
          ? xlsx.read(decodeDocumentText(bytes), { type: "string", raw: true })
          : xlsx.read(bytes, { type: "array" })
      return {
        kind: "sheets",
        sheets: workbook.SheetNames.map((name) => ({
          name,
          content: previewHTML(xlsx.utils.sheet_to_html(workbook.Sheets[name])),
        })),
      }
    }
    default:
      return { kind: "unsupported" }
  }
}
