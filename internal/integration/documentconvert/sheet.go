//go:build server

package documentconvert

import (
	"bytes"
	"encoding/csv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// convertCSV 把 CSV 转换为以首行为表头的 Markdown 表格。
func convertCSV(data []byte) (string, error) {
	reader := csv.NewReader(strings.NewReader(decodeText(data)))
	reader.FieldsPerRecord, reader.LazyQuotes = -1, true
	rows, err := reader.ReadAll()
	if err != nil {
		return "", err
	}
	return markdownTable(rows), nil
}

// convertXLSX 把每个工作表转换为二级标题加 Markdown 表格，单元格取按数字格式显示的值。
func convertXLSX(data []byte) (string, error) {
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer file.Close()
	var builder strings.Builder
	for _, sheet := range file.GetSheetList() {
		rows, err := file.GetRows(sheet)
		if err != nil {
			return "", err
		}
		if content := markdownTable(rows); content != "" {
			builder.WriteString("## " + sheet + "\n\n" + content + "\n")
		}
	}
	return builder.String(), nil
}

// markdownTable 跳过空行后以首行为表头输出 Markdown 表格，各行按最宽行补齐，单元格内空白合并为单个空格。
func markdownTable(rows [][]string) string {
	cells := make([][]string, 0, len(rows))
	width := 0
	for _, row := range rows {
		normalized := make([]string, len(row))
		blank := true
		for index, cell := range row {
			normalized[index] = strings.Join(strings.Fields(strings.ReplaceAll(cell, "|", `\|`)), " ")
			blank = blank && normalized[index] == ""
		}
		if !blank {
			cells = append(cells, normalized)
			width = max(width, len(normalized))
		}
	}
	var builder strings.Builder
	for index, row := range cells {
		builder.WriteString("|")
		for column := range width {
			cell := ""
			if column < len(row) {
				cell = row[column]
			}
			builder.WriteString(" " + cell + " |")
		}
		builder.WriteString("\n")
		if index == 0 {
			builder.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	return builder.String()
}
