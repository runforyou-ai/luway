//go:build server

package documentconvert

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

// convertCSV 逐行读取 CSV 并转换为以首行为表头的 Markdown 表格。
func convertCSV(data []byte) (string, error) {
	reader := csv.NewReader(strings.NewReader(decodeText(data)))
	reader.FieldsPerRecord, reader.LazyQuotes, reader.ReuseRecord = -1, true, true
	var table tableCells
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return table.markdown(), nil
		}
		if err != nil {
			return "", err
		}
		table.add(row)
	}
}

// convertXLSX 逐行读取每个工作表并转换为二级标题加 Markdown 表格，单元格取按数字格式显示的值；解压后总大小超过上限时返回 errContentTooLarge。
func convertXLSX(data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	// 按压缩包登记的解压大小累计，读取时由 zip 校验实际大小与登记一致。
	var expanded uint64
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > maxExpandedBytes-expanded {
			return "", errContentTooLarge
		}
		expanded += entry.UncompressedSize64
	}
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer file.Close()
	var builder strings.Builder
	for _, sheet := range file.GetSheetList() {
		rows, err := file.Rows(sheet)
		if err != nil {
			return "", err
		}
		var table tableCells
		for rows.Next() {
			row, err := rows.Columns()
			if err != nil {
				rows.Close()
				return "", err
			}
			table.add(row)
		}
		if err := errors.Join(rows.Error(), rows.Close()); err != nil {
			return "", err
		}
		if content := table.markdown(); content != "" {
			builder.WriteString("## " + sheet + "\n\n" + content + "\n")
		}
	}
	return builder.String(), nil
}

// tableCells 逐行收集表格的非空行，width 是最宽行的单元格数。
type tableCells struct {
	rows  [][]string
	width int
}

// add 复制一行单元格并把单元格内空白合并为单个空格、转义竖线，全部为空的行跳过。
func (t *tableCells) add(row []string) {
	normalized := make([]string, len(row))
	blank := true
	for index, cell := range row {
		normalized[index] = strings.Join(strings.Fields(strings.ReplaceAll(cell, "|", `\|`)), " ")
		blank = blank && normalized[index] == ""
	}
	if !blank {
		t.rows = append(t.rows, normalized)
		t.width = max(t.width, len(normalized))
	}
}

// markdown 以首行为表头输出 Markdown 表格，各行按最宽行补齐。
func (t *tableCells) markdown() string {
	var builder strings.Builder
	for index, row := range t.rows {
		builder.WriteString("|")
		for column := range t.width {
			cell := ""
			if column < len(row) {
				cell = row[column]
			}
			builder.WriteString(" " + cell + " |")
		}
		builder.WriteString("\n")
		if index == 0 {
			builder.WriteString("|" + strings.Repeat(" --- |", t.width) + "\n")
		}
	}
	return builder.String()
}

// markdownTable 跳过空行后以首行为表头输出 Markdown 表格，各行按最宽行补齐，单元格内空白合并为单个空格。
func markdownTable(rows [][]string) string {
	var table tableCells
	for _, row := range rows {
		table.add(row)
	}
	return table.markdown()
}
