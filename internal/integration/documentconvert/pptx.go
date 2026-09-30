//go:build server

package documentconvert

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
)

// pptxSlide 持有渲染单页幻灯片所需的压缩包与该页的部件关系。
type pptxSlide struct {
	archive       *zip.Reader
	relationships map[string]relationship
}

// convertPPTX 按幻灯片顺序输出标题、文本框、表格、图表数据和备注，标题占位符渲染为一级标题。
func convertPPTX(data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	presentation, err := readPart(archive, "ppt/presentation.xml")
	if err != nil {
		return "", err
	}
	relationships, err := readRelationships(archive, "ppt/presentation.xml")
	if err != nil {
		return "", err
	}
	var blocks []string
	for _, item := range presentation.child("presentation").child("sldIdLst").elements() {
		target := relationships[item.attr("r:id")].Target
		root, err := readPart(archive, target)
		if err != nil {
			return "", err
		}
		slideRelationships, err := readRelationships(archive, target)
		if err != nil {
			return "", err
		}
		slide := &pptxSlide{archive: archive, relationships: slideRelationships}
		if err := slide.blocks(root.child("sld").child("cSld").child("spTree"), &blocks); err != nil {
			return "", err
		}
		// 备注页只取备注正文占位符，幻灯片缩略图与页码占位符不输出。
		for _, item := range slideRelationships {
			if item.Type != "notesSlide" {
				continue
			}
			notes, err := readPart(archive, item.Target)
			if err != nil {
				return "", err
			}
			for _, shape := range notes.child("notes").child("cSld").child("spTree").elements() {
				if shape.Name == "sp" && shape.child("nvSpPr").child("nvPr").child("ph").attr("type") == "body" {
					if text := shapeText(shape); text != "" {
						blocks = append(blocks, text)
					}
				}
			}
		}
	}
	return strings.Join(blocks, "\n\n"), nil
}

// blocks 按形状顺序渲染文本框、表格与图表，并展开组合形状。
func (s *pptxSlide) blocks(tree *xmlNode, blocks *[]string) error {
	for _, shape := range tree.elements() {
		switch shape.Name {
		case "sp":
			text := shapeText(shape)
			if text == "" {
				continue
			}
			// 标题占位符合并为单行一级标题。
			if kind := shape.child("nvSpPr").child("nvPr").child("ph").attr("type"); kind == "title" || kind == "ctrTitle" {
				text = "# " + strings.Join(strings.Fields(text), " ")
			}
			*blocks = append(*blocks, text)
		case "graphicFrame":
			data := shape.child("graphic").child("graphicData")
			if chart := data.child("chart"); chart != nil {
				text, err := s.chart(s.relationships[chart.attr("r:id")].Target)
				if err != nil {
					return err
				}
				if text != "" {
					*blocks = append(*blocks, text)
				}
				continue
			}
			var rows [][]string
			for _, row := range data.child("tbl").elements() {
				if row.Name != "tr" {
					continue
				}
				var cells []string
				for _, cell := range row.elements() {
					if cell.Name == "tc" {
						cells = append(cells, slideText(cell.child("txBody")))
					}
				}
				rows = append(rows, cells)
			}
			if table := strings.TrimSpace(markdownTable(rows)); table != "" {
				*blocks = append(*blocks, table)
			}
		case "grpSp":
			if err := s.blocks(shape, blocks); err != nil {
				return err
			}
		case "AlternateContent":
			if err := s.blocks(shape.child("Choice"), blocks); err != nil {
				return err
			}
		}
	}
	return nil
}

// chart 输出图表标题和数据表，首列为类别，其余各列为系列取值。
func (s *pptxSlide) chart(target string) (string, error) {
	root, err := readPart(s.archive, target)
	if err != nil {
		return "", err
	}
	chart := root.child("chartSpace").child("chart")
	header := []string{""}
	var columns [][]string
	var categories []string
	for _, plot := range chart.child("plotArea").elements() {
		for _, series := range plot.elements() {
			if series.Name != "ser" || series.child("val") == nil {
				continue
			}
			// 系列名称取自引用缓存，没有引用时取直接给出的名称。
			name := ""
			if names := cacheValues(series.child("tx")); len(names) > 0 {
				name = names[0]
			} else if value := series.child("tx").child("v"); value != nil {
				name = value.Text
			}
			header = append(header, name)
			columns = append(columns, cacheValues(series.child("val")))
			if len(categories) == 0 {
				categories = cacheValues(series.child("cat"))
			}
		}
	}
	if len(columns) == 0 {
		return "", nil
	}
	rows := [][]string{header}
	count := len(categories)
	for _, column := range columns {
		count = max(count, len(column))
	}
	for index := range count {
		row := []string{""}
		if index < len(categories) {
			row[0] = categories[index]
		}
		for _, column := range columns {
			value := ""
			if index < len(column) {
				value = column[index]
			}
			row = append(row, value)
		}
		rows = append(rows, row)
	}
	table := strings.TrimSpace(markdownTable(rows))
	if title := strings.Join(strings.Fields(slideText(chart.child("title"))), " "); title != "" {
		return title + "\n\n" + table, nil
	}
	return table, nil
}

// cacheValues 按数据点序号返回图表引用缓存中的取值，缺失的数据点为空字符串。
func cacheValues(node *xmlNode) []string {
	var values []string
	for _, child := range node.elements() {
		if child.Name != "pt" {
			values = append(values, cacheValues(child)...)
			continue
		}
		index, err := strconv.Atoi(child.attr("idx"))
		if err != nil || index < 0 {
			continue
		}
		for len(values) <= index {
			values = append(values, "")
		}
		if value := child.child("v"); value != nil {
			values[index] = value.Text
		}
	}
	return values
}

// shapeText 返回形状内非空段落的文字，段落之间以换行分隔。
func shapeText(shape *xmlNode) string {
	var paragraphs []string
	for _, paragraph := range shape.child("txBody").elements() {
		if text := strings.TrimSpace(slideText(paragraph)); paragraph.Name == "p" && text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	return strings.Join(paragraphs, "\n")
}

// slideText 递归拼接文本运行与字段文字，段内换行输出为换行，段落之间以空格分隔。
func slideText(node *xmlNode) string {
	var builder strings.Builder
	for _, child := range node.elements() {
		switch child.Name {
		case "t":
			builder.WriteString(child.Text)
		case "br":
			builder.WriteString("\n")
		case "p":
			builder.WriteString(slideText(child) + " ")
		case "pPr", "rPr", "endParaRPr":
		default:
			builder.WriteString(slideText(child))
		}
	}
	return builder.String()
}
