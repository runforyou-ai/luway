/** 在线文档的 Markdown 富文本编辑器。 */
import { useEffect, useRef } from "react"
import { useTranslation } from "react-i18next"
import { EditorContent, useEditor, type Editor } from "@tiptap/react"
import StarterKit from "@tiptap/starter-kit"
import { Markdown } from "@tiptap/markdown"
import {
  Table,
  TableCell,
  TableHeader,
  TableRow,
} from "@tiptap/extension-table"
import {
  BoldIcon,
  Heading1Icon,
  Heading2Icon,
  Heading3Icon,
  ItalicIcon,
  ListIcon,
  ListOrderedIcon,
  QuoteIcon,
  SquareCodeIcon,
  TableIcon,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

// Markdown 表格的每个单元格只能容纳一个子节点，编辑器按此限制单元格内容。
const SingleBlockCell = TableCell.extend({ content: "paragraph" })
const SingleBlockHeader = TableHeader.extend({ content: "paragraph" })

/** 编辑 Markdown 正文，内容随编辑序列化回 Markdown。 */
export function KnowledgeDocumentEditor({
  id,
  value,
  disabled,
  onChange,
  fieldRef,
}: {
  id: string
  value: string
  disabled: boolean
  onChange: (value: string) => void
  fieldRef: (instance: HTMLTextAreaElement | null) => void
}) {
  const { t } = useTranslation("knowledgeBase")
  const initial = useRef(value)
  // 挂载时把 Markdown 解析成文档会产生结构规范化，只有用户实际编辑后才回写表单。
  const edited = useRef(false)
  const editor = useEditor({
    extensions: [
      StarterKit,
      Markdown,
      Table.configure({ resizable: false }),
      TableRow,
      SingleBlockHeader,
      SingleBlockCell,
    ],
    content: initial.current,
    contentType: "markdown",
    editable: !disabled,
    editorProps: {
      handleDOMEvents: {
        beforeinput: () => markEdited(),
        keydown: () => markEdited(),
        paste: () => markEdited(),
        drop: () => markEdited(),
        cut: () => markEdited(),
      },
      attributes: {
        id,
        class:
          "min-h-96 max-w-none px-4 py-3 text-sm outline-none [&_h1]:mb-3 [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:mb-2 [&_h2]:mt-5 [&_h2]:text-lg [&_h2]:font-semibold [&_h3]:mb-2 [&_h3]:mt-4 [&_h3]:font-semibold [&_p]:my-2 [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-6 [&_blockquote]:my-2 [&_blockquote]:border-l-2 [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_pre]:my-2 [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-3 [&_table]:my-3 [&_table]:w-full [&_table]:table-fixed [&_table]:border-collapse [&_td]:border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:bg-muted [&_th]:px-2 [&_th]:py-1",
      },
    },
    onUpdate: ({ editor }) => {
      if (!edited.current) return
      onChange(editor.markdown?.serialize(editor.getJSON()) ?? "")
    },
  })

  /** 记录一次用户编辑，事件继续按默认行为处理。 */
  function markEdited() {
    edited.current = true
    return false
  }

  useEffect(() => {
    editor?.setEditable(!disabled)
  }, [editor, disabled])
  if (!editor) return null
  return (
    <div className="relative rounded-md border">
      <EditorToolbar editor={editor} disabled={disabled} onEdit={markEdited} />
      <EditorContent editor={editor} />
      {/* 正文必填由透明的原生控件承载，浏览器把校验提示显示在编辑区域左上角。 */}
      <textarea
        ref={fieldRef}
        aria-hidden="true"
        tabIndex={-1}
        required
        value={value}
        onChange={() => {}}
        className="pointer-events-none absolute inset-0 h-full w-full resize-none border-0 bg-transparent p-0 text-transparent caret-transparent outline-none"
        aria-label={t("documents.content")}
      />
    </div>
  )
}

/** 以图标展示标题、行内样式、块结构和表格的插入操作。 */
function EditorToolbar({
  editor,
  disabled,
  onEdit,
}: {
  editor: Editor
  disabled: boolean
  onEdit: () => void
}) {
  const { t } = useTranslation("knowledgeBase")
  const actions = [
    {
      key: "heading1",
      label: t("documents.editor.heading1"),
      icon: <Heading1Icon />,
      active: editor.isActive("heading", { level: 1 }),
      run: () => editor.chain().focus().toggleHeading({ level: 1 }).run(),
    },
    {
      key: "heading2",
      label: t("documents.editor.heading2"),
      icon: <Heading2Icon />,
      active: editor.isActive("heading", { level: 2 }),
      run: () => editor.chain().focus().toggleHeading({ level: 2 }).run(),
    },
    {
      key: "heading3",
      label: t("documents.editor.heading3"),
      icon: <Heading3Icon />,
      active: editor.isActive("heading", { level: 3 }),
      run: () => editor.chain().focus().toggleHeading({ level: 3 }).run(),
    },
    {
      key: "bold",
      label: t("documents.editor.bold"),
      icon: <BoldIcon />,
      active: editor.isActive("bold"),
      run: () => editor.chain().focus().toggleBold().run(),
    },
    {
      key: "italic",
      label: t("documents.editor.italic"),
      icon: <ItalicIcon />,
      active: editor.isActive("italic"),
      run: () => editor.chain().focus().toggleItalic().run(),
    },
    {
      key: "bulletList",
      label: t("documents.editor.bulletList"),
      icon: <ListIcon />,
      active: editor.isActive("bulletList"),
      run: () => editor.chain().focus().toggleBulletList().run(),
    },
    {
      key: "orderedList",
      label: t("documents.editor.orderedList"),
      icon: <ListOrderedIcon />,
      active: editor.isActive("orderedList"),
      run: () => editor.chain().focus().toggleOrderedList().run(),
    },
    {
      key: "blockquote",
      label: t("documents.editor.blockquote"),
      icon: <QuoteIcon />,
      active: editor.isActive("blockquote"),
      run: () => editor.chain().focus().toggleBlockquote().run(),
    },
    {
      key: "codeBlock",
      label: t("documents.editor.codeBlock"),
      icon: <SquareCodeIcon />,
      active: editor.isActive("codeBlock"),
      run: () => editor.chain().focus().toggleCodeBlock().run(),
    },
    {
      key: "table",
      label: t("documents.editor.table"),
      icon: <TableIcon />,
      active: editor.isActive("table"),
      run: () =>
        editor
          .chain()
          .focus()
          .insertTable({ rows: 3, cols: 3, withHeaderRow: true })
          .run(),
    },
  ]
  return (
    <div className="flex flex-wrap gap-1 border-b px-2 py-2">
      {actions.map((action) => (
        <Button
          key={action.key}
          type="button"
          variant="ghost"
          size="icon-sm"
          disabled={disabled}
          aria-label={action.label}
          aria-pressed={action.active}
          title={action.label}
          className={cn(action.active && "bg-accent")}
          onClick={() => {
            onEdit()
            action.run()
          }}
        >
          {action.icon}
        </Button>
      ))}
    </div>
  )
}
