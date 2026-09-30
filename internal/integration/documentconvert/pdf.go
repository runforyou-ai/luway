//go:build server

package documentconvert

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
)

// 标题行字号不小于正文字号的倍数、标题行的最大字数、最多标题层级，以及中日韩文字与全角标点的起始码位。
const (
	pdfHeadingScale    = 1.15
	pdfHeadingMaxRunes = 60
	pdfHeadingLevels   = 3
	pdfWideRune        = '\u2e80'
)

// pdfLine 表示页面上的一行文字，坐标为 PDF 点坐标，纵轴向上。
type pdfLine struct {
	Page   int
	Text   string
	Size   float64
	Left   float64
	Right  float64
	Top    float64
	Bottom float64
}

// convertPDF 按页提取带字号的文本行，按字号识别标题并按行距划分段落。
func convertPDF(ctx context.Context, pool pdfium.Pool, data []byte) (string, error) {
	instance, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return "", err
	}
	defer instance.Close()
	document, err := instance.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return "", err
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: document.Document})
	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: document.Document})
	if err != nil {
		return "", err
	}
	var lines []pdfLine
	// 同一行内按字体切分的文本块合并为一行，行字号取字数最多的文本块。
	for index := range count.PageCount {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, err := instance.GetPageTextStructured(&requests.GetPageTextStructured{
			Page:                   requests.Page{ByIndex: &requests.PageByIndex{Document: document.Document, Index: index}},
			Mode:                   requests.GetPageTextStructuredModeRects,
			CollectFontInformation: true,
		})
		if err != nil {
			return "", err
		}
		dominant := 0
		for _, rect := range page.Rects {
			text := strings.TrimRight(rect.Text, "\r\n")
			if strings.TrimSpace(text) == "" {
				continue
			}
			size := 0.0
			if rect.FontInformation != nil {
				size = rect.FontInformation.RenderedSize
			}
			position := rect.PointPosition
			current := len(lines) - 1
			if current < 0 || lines[current].Page != index || position.Top < lines[current].Bottom || position.Bottom > lines[current].Top || position.Left < lines[current].Left {
				lines = append(lines, pdfLine{Page: index, Text: text, Size: size, Left: position.Left, Right: position.Right, Top: position.Top, Bottom: position.Bottom})
				dominant = utf8.RuneCountInString(text)
				continue
			}
			line := &lines[current]
			last, _ := utf8.DecodeLastRuneInString(line.Text)
			first, _ := utf8.DecodeRuneInString(text)
			// 间距明显的两个文本块之间补空格，与中日韩文字或全角标点相邻时直接拼接。
			if position.Left-line.Right > max(size, line.Size)*0.2 && !unicode.IsSpace(last) && !unicode.IsSpace(first) && last < pdfWideRune && first < pdfWideRune {
				line.Text += " "
			}
			line.Text += text
			if runes := utf8.RuneCountInString(text); runes > dominant {
				line.Size, dominant = size, runes
			}
			line.Right, line.Top, line.Bottom = max(line.Right, position.Right), max(line.Top, position.Top), min(line.Bottom, position.Bottom)
		}
	}
	return renderPDFLines(lines), nil
}

// renderPDFLines 以字数最多的字号为正文字号，较大字号的短行按字号从大到小映射为标题层级，行距明显变大或字号变化时分段。
func renderPDFLines(lines []pdfLine) string {
	weights := map[float64]int{}
	for index := range lines {
		lines[index].Text = strings.TrimSpace(lines[index].Text)
		lines[index].Size = math.Round(lines[index].Size*2) / 2
		weights[lines[index].Size] += utf8.RuneCountInString(lines[index].Text)
	}
	body, weight := 0.0, -1
	for size, count := range weights {
		if count > weight || count == weight && size < body {
			body, weight = size, count
		}
	}
	// 较大字号的短行按字号从大到小取前几档作为标题层级，其余档归入最低层级。
	var sizes []float64
	headings := make([]bool, len(lines))
	for index, line := range lines {
		headings[index] = body > 0 && line.Size >= body*pdfHeadingScale && utf8.RuneCountInString(line.Text) <= pdfHeadingMaxRunes && strings.IndexFunc(line.Text, unicode.IsLetter) >= 0
		if headings[index] && !slices.Contains(sizes, line.Size) {
			sizes = append(sizes, line.Size)
		}
	}
	slices.SortFunc(sizes, func(a, b float64) int { return cmp.Compare(b, a) })
	var builder strings.Builder
	for index, line := range lines {
		continued := false
		if index > 0 {
			previous := lines[index-1]
			separator := "\n\n"
			// 同一页内字号与标题属性相同且行距不超过行高的相邻行属于同一段落，连续同级标题合并为一行。
			if previous.Page == line.Page && previous.Size == line.Size && headings[index-1] == headings[index] && previous.Bottom-line.Top <= (previous.Top-previous.Bottom)*0.8 {
				separator = "\n"
				if headings[index] {
					separator, continued = " ", true
				}
			}
			builder.WriteString(separator)
		}
		if headings[index] && !continued {
			builder.WriteString(strings.Repeat("#", min(slices.Index(sizes, line.Size)+1, pdfHeadingLevels)) + " ")
		}
		builder.WriteString(line.Text)
	}
	return builder.String()
}
