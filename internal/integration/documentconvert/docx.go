//go:build server

package documentconvert

import (
	"archive/zip"
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// docxDocument 持有渲染 Word 正文所需的标题样式、列表编号格式与外部链接。
type docxDocument struct {
	headings map[string]int
	ordered  map[string]map[string]bool
	links    map[string]string
}

// convertDOCX 把 Word 正文中的标题、段落、列表和表格转换为 Markdown。
func convertDOCX(data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	root, err := readPart(archive, "word/document.xml")
	if err != nil {
		return "", err
	}
	body := root.child("document").child("body")
	if body == nil {
		return "", errors.New("docx body missing")
	}
	relationships, err := readRelationships(archive, "word/document.xml")
	if err != nil {
		return "", err
	}
	document := &docxDocument{headings: map[string]int{}, ordered: map[string]map[string]bool{}, links: map[string]string{}}
	for id, item := range relationships {
		if item.External {
			document.links[id] = item.Target
		}
	}
	// 按样式名称或大纲级别识别标题样式。
	styles, err := readPart(archive, "word/styles.xml")
	if err != nil {
		return "", err
	}
	for _, style := range styles.child("styles").elements() {
		name := strings.ToLower(style.child("name").attr("val"))
		level := 0
		if name == "title" {
			level = 1
		} else if number, found := strings.CutPrefix(name, "heading "); found {
			level, _ = strconv.Atoi(number)
		} else if outline, err := strconv.Atoi(style.child("pPr").child("outlineLvl").attr("val")); err == nil && outline < 9 {
			level = outline + 1
		}
		if style.Name == "style" && level > 0 {
			document.headings[style.attr("styleId")] = level
		}
	}
	// 按编号定义记录每个列表级别是否为有序编号。
	numbering, err := readPart(archive, "word/numbering.xml")
	if err != nil {
		return "", err
	}
	abstract := map[string]map[string]bool{}
	for _, item := range numbering.child("numbering").elements() {
		switch item.Name {
		case "abstractNum":
			levels := map[string]bool{}
			for _, level := range item.Children {
				if level.Name == "lvl" {
					format := level.child("numFmt").attr("val")
					levels[level.attr("ilvl")] = format != "bullet" && format != "none" && format != ""
				}
			}
			abstract[item.attr("abstractNumId")] = levels
		case "num":
			document.ordered[item.attr("numId")] = abstract[item.child("abstractNumId").attr("val")]
		}
	}
	var blocks []string
	document.blocks(body, &blocks)
	return strings.Join(blocks, "\n\n"), nil
}

// blocks 按文档顺序渲染段落与表格，并展开内容控件等包装元素。
func (d *docxDocument) blocks(node *xmlNode, blocks *[]string) {
	for _, child := range node.Children {
		switch child.Name {
		case "p":
			if block := d.paragraph(child); block != "" {
				*blocks = append(*blocks, block)
			}
		case "tbl":
			if block := d.table(child); block != "" {
				*blocks = append(*blocks, block)
			}
		case "sectPr":
		default:
			d.blocks(child, blocks)
		}
	}
}

// paragraph 按段落样式、大纲级别和列表编号渲染一个段落。
func (d *docxDocument) paragraph(node *xmlNode) string {
	var builder strings.Builder
	d.text(node, &builder)
	text := strings.TrimSpace(builder.String())
	if text == "" {
		return ""
	}
	properties := node.child("pPr")
	level := d.headings[properties.child("pStyle").attr("val")]
	if outline, err := strconv.Atoi(properties.child("outlineLvl").attr("val")); err == nil && outline < 9 {
		level = outline + 1
	}
	if level > 0 {
		return strings.Repeat("#", min(level, 6)) + " " + strings.Join(strings.Fields(text), " ")
	}
	if list := properties.child("numPr"); list != nil {
		depth, _ := strconv.Atoi(list.child("ilvl").attr("val"))
		marker := "- "
		if d.ordered[list.child("numId").attr("val")][list.child("ilvl").attr("val")] {
			marker = "1. "
		}
		return strings.Repeat("  ", depth) + marker + text
	}
	return text
}

// text 递归拼接文字、制表符与换行，外部超链接输出为 Markdown 链接。
func (d *docxDocument) text(node *xmlNode, builder *strings.Builder) {
	for _, child := range node.Children {
		switch child.Name {
		case "t":
			builder.WriteString(child.Text)
		case "tab":
			builder.WriteString(" ")
		case "br", "cr":
			builder.WriteString("\n")
		case "hyperlink":
			target := d.links[child.attr("r:id")]
			if target == "" {
				d.text(child, builder)
				continue
			}
			var label strings.Builder
			d.text(child, &label)
			builder.WriteString("[" + strings.TrimSpace(label.String()) + "](" + target + ")")
		case "pPr", "rPr", "drawing", "pict", "object", "Fallback":
		default:
			d.text(child, builder)
		}
	}
}

// table 把表格转换为 Markdown 表格，横向合并按跨度重复单元格，纵向合并沿用上一行的值。
func (d *docxDocument) table(node *xmlNode) string {
	var rows [][]string
	for _, row := range node.Children {
		if row.Name != "tr" {
			continue
		}
		var cells []string
		for _, cell := range row.Children {
			if cell.Name != "tc" {
				continue
			}
			var paragraphs []string
			d.blocks(cell, &paragraphs)
			text := strings.Join(paragraphs, " ")
			properties := cell.child("tcPr")
			// 纵向合并的后续单元格沿用上一行同列的值。
			if merge := properties.child("vMerge"); merge != nil && merge.attr("val") != "restart" && len(rows) > 0 && len(cells) < len(rows[len(rows)-1]) {
				text = rows[len(rows)-1][len(cells)]
			}
			span, err := strconv.Atoi(properties.child("gridSpan").attr("val"))
			if err != nil || span < 1 {
				span = 1
			}
			for range span {
				cells = append(cells, text)
			}
		}
		rows = append(rows, cells)
	}
	return strings.TrimSpace(markdownTable(rows))
}
