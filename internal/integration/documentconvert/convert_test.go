//go:build server

package documentconvert

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// convertBytes 使用新建转换器转换一份原件。
func convertBytes(t *testing.T, converter *Converter, name string, data []byte) string {
	t.Helper()
	markdown, err := converter.Convert(context.Background(), name, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return markdown
}

// zipArchive 按部件路径和内容生成 OOXML 压缩包。
func zipArchive(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range parts {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestConvertText 验证文本编码识别与不支持格式的原因码。
func TestConvertText(t *testing.T) {
	converter := NewConverter()
	gbk, err := simplifiedchinese.GBK.NewEncoder().String("退款说明")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"a.txt": []byte("\xEF\xBB\xBF退款说明\n"), "b.md": []byte(gbk), "c.json": []byte("退款说明")} {
		if markdown := convertBytes(t, converter, name, data); markdown != "退款说明" {
			t.Fatalf("%s markdown=%q", name, markdown)
		}
	}
	var failure *Error
	if _, err := converter.Convert(context.Background(), "a.wav", strings.NewReader("RIFF")); !errors.As(err, &failure) || failure.Code != "unsupported_file" {
		t.Fatalf("err=%v", err)
	}
	if _, err := converter.Convert(context.Background(), "a.docx", strings.NewReader("not zip")); !errors.As(err, &failure) || failure.Code != "parse_failed" {
		t.Fatalf("err=%v", err)
	}
}

// TestConvertHTML 验证标题、表格合并单元格、脚本剔除，以及 meta 声明编码与无确定声明时的 UTF-8 解码。
func TestConvertHTML(t *testing.T) {
	page := `<html><head><meta charset="gbk"><script>alert(1)</script></head><body><h1>退款</h1><p>七天内可退。</p>
<table><tr><th>项目</th><th>时限</th></tr><tr><td colspan="2">全部商品</td></tr></table></body></html>`
	data, err := simplifiedchinese.GBK.NewEncoder().String(page)
	if err != nil {
		t.Fatal(err)
	}
	markdown := convertBytes(t, NewConverter(), "page.html", []byte(data))
	for _, want := range []string{"# 退款", "七天内可退。", "| 项目 | 时限 |", "| 全部商品 | 全部商品 |"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown=%q want %q", markdown, want)
		}
	}
	if strings.Contains(markdown, "alert") {
		t.Fatalf("markdown=%q", markdown)
	}

	// 显式声明的编码优先于 UTF-8 判断：「漏」的 GBK 字节恰好也是合法 UTF-8。
	leak, err := simplifiedchinese.GBK.NewEncoder().String(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=gbk"></head><body><p>漏</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if markdown := convertBytes(t, NewConverter(), "leak.html", []byte(leak)); markdown != "漏" {
		t.Fatalf("markdown=%q", markdown)
	}

	// 声明位于前 1024 字节之后的 UTF-8 页面按 UTF-8 解码。
	long := "<html><head><style>/*" + strings.Repeat("x", 1200) + "*/</style><meta charset=\"utf-8\"></head><body><h1>退款说明</h1></body></html>"
	if markdown := convertBytes(t, NewConverter(), "long.html", []byte(long)); markdown != "# 退款说明" {
		t.Fatalf("markdown=%q", markdown)
	}
}

// TestConvertSheets 验证 CSV 与 XLSX 的表格输出、显示值和空行跳过。
func TestConvertSheets(t *testing.T) {
	converter := NewConverter()
	csv := convertBytes(t, converter, "a.csv", []byte("编号,金额\n\n00123,\"1|2\"\n"))
	if csv != "| 编号 | 金额 |\n| --- | --- |\n| 00123 | 1\\|2 |" {
		t.Fatalf("csv=%q", csv)
	}

	file := excelize.NewFile()
	defer file.Close()
	if err := file.SetSheetName("Sheet1", "订单"); err != nil {
		t.Fatal(err)
	}
	style, err := file.NewStyle(&excelize.Style{NumFmt: 2})
	if err != nil {
		t.Fatal(err)
	}
	for cell, value := range map[string]any{"A1": "编号", "B1": "金额", "A3": "00123", "B3": 12.5} {
		if err := file.SetCellValue("订单", cell, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.SetCellStyle("订单", "B3", "B3", style); err != nil {
		t.Fatal(err)
	}
	if _, err := file.NewSheet("空表"); err != nil {
		t.Fatal(err)
	}
	buffer, err := file.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	xlsx := convertBytes(t, converter, "a.xlsx", buffer.Bytes())
	if xlsx != "## 订单\n\n| 编号 | 金额 |\n| --- | --- |\n| 00123 | 12.50 |" {
		t.Fatalf("xlsx=%q", xlsx)
	}
}

// TestConvertDOCX 验证标题样式、列表、外部链接与合并单元格。
func TestConvertDOCX(t *testing.T) {
	const w = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	document := `<w:document ` + w + `><w:body>
<w:p><w:pPr><w:pStyle w:val="1"/></w:pPr><w:r><w:t>退款政策</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">签收后 </w:t></w:r><w:del><w:r><w:delText>三</w:delText></w:r></w:del><w:r><w:t>七天内可退，详见</w:t></w:r><w:hyperlink r:id="rId9"><w:r><w:t>帮助中心</w:t></w:r></w:hyperlink></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>保留包装</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>联系客服</w:t></w:r></w:p>
<w:sdt><w:sdtContent><w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>类型</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>时限</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>全部商品</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc><w:tc><w:p><w:r><w:t>七天</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl></w:sdtContent></w:sdt>
<w:sectPr/></w:body></w:document>`
	styles := `<w:styles ` + w + `><w:style w:type="paragraph" w:styleId="1"><w:name w:val="heading 1"/></w:style></w:styles>`
	numbering := `<w:numbering ` + w + `><w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>`
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId9" Type="hyperlink" Target="https://example.com/help" TargetMode="External"/></Relationships>`
	data := zipArchive(t, map[string]string{"word/document.xml": document, "word/styles.xml": styles, "word/numbering.xml": numbering, "word/_rels/document.xml.rels": rels})
	markdown := convertBytes(t, NewConverter(), "a.docx", data)
	want := "# 退款政策\n\n签收后 七天内可退，详见[帮助中心](https://example.com/help)\n\n- 保留包装\n\n  1. 联系客服\n\n| 类型 | 时限 |\n| --- | --- |\n| 全部商品 | 全部商品 |\n| 全部商品 | 七天 |"
	if markdown != want {
		t.Fatalf("markdown=%q\nwant=%q", markdown, want)
	}
}

// TestConvertPPTX 验证幻灯片顺序、标题占位符、文本框、表格、图表数据与备注。
func TestConvertPPTX(t *testing.T) {
	const ns = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	presentation := `<p:presentation ` + ns + `><p:sldIdLst><p:sldId id="257" r:id="rId3"/><p:sldId id="256" r:id="rId2"/></p:sldIdLst></p:presentation>`
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId2" Type="slide" Target="slides/slide1.xml"/><Relationship Id="rId3" Type="slide" Target="/ppt/slides/slide2.xml"/></Relationships>`
	first := `<p:sld ` + ns + `><p:cSld><p:spTree>
<p:sp><p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>售后</a:t></a:r></a:p></p:txBody></p:sp>
<p:grpSp><p:sp><p:txBody><a:p><a:r><a:t>七天无理由</a:t></a:r></a:p><a:p><a:r><a:t>运费自理</a:t></a:r></a:p></p:txBody></p:sp></p:grpSp>
<p:graphicFrame><a:graphic><a:graphicData><c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="rId5"/></a:graphicData></a:graphic></p:graphicFrame>
<p:graphicFrame><a:graphic><a:graphicData><a:tbl><a:tr><a:tc><a:txBody><a:p><a:r><a:t>渠道</a:t></a:r></a:p></a:txBody></a:tc></a:tr><a:tr><a:tc><a:txBody><a:p><a:r><a:t>网站</a:t></a:r></a:p></a:txBody></a:tc></a:tr></a:tbl></a:graphicData></a:graphic></p:graphicFrame>
</p:spTree></p:cSld></p:sld>`
	second := `<p:sld ` + ns + `><p:cSld><p:spTree><p:sp><p:nvSpPr><p:nvPr><p:ph type="ctrTitle"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>客服手册</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	slideRels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="../charts/chart1.xml"/><Relationship Id="rId6" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/notesSlide1.xml"/></Relationships>`
	chart := `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><c:chart><c:title><c:tx><c:rich><a:p><a:r><a:t>处理时长</a:t></a:r></a:p></c:rich></c:tx></c:title><c:plotArea><c:barChart><c:ser>
<c:tx><c:strRef><c:f>Sheet1!$B$1</c:f><c:strCache><c:pt idx="0"><c:v>小时</c:v></c:pt></c:strCache></c:strRef></c:tx>
<c:cat><c:strRef><c:strCache><c:ptCount val="2"/><c:pt idx="0"><c:v>普通订单</c:v></c:pt><c:pt idx="1"><c:v>加急订单</c:v></c:pt></c:strCache></c:strRef></c:cat>
<c:val><c:numRef><c:numCache><c:pt idx="0"><c:v>48</c:v></c:pt><c:pt idx="1"><c:v>24</c:v></c:pt></c:numCache></c:numRef></c:val>
</c:ser></c:barChart></c:plotArea></c:chart></c:chartSpace>`
	notes := `<p:notes ` + ns + `><p:cSld><p:spTree>
<p:sp><p:nvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr></p:sp>
<p:sp><p:nvSpPr><p:nvPr><p:ph type="body"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>退款申请必须在签收后七天内提交</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:nvPr><p:ph type="sldNum"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>1</a:t></a:r></a:p></p:txBody></p:sp>
</p:spTree></p:cSld></p:notes>`
	data := zipArchive(t, map[string]string{"ppt/presentation.xml": presentation, "ppt/_rels/presentation.xml.rels": rels, "ppt/slides/slide1.xml": first, "ppt/slides/slide2.xml": second,
		"ppt/slides/_rels/slide1.xml.rels": slideRels, "ppt/charts/chart1.xml": chart, "ppt/notesSlides/notesSlide1.xml": notes})
	markdown := convertBytes(t, NewConverter(), "a.pptx", data)
	want := "# 客服手册\n\n# 售后\n\n七天无理由\n运费自理\n\n处理时长\n\n|  | 小时 |\n| --- | --- |\n| 普通订单 | 48 |\n| 加急订单 | 24 |\n\n| 渠道 |\n| --- |\n| 网站 |\n\n退款申请必须在签收后七天内提交"
	if markdown != want {
		t.Fatalf("markdown=%q\nwant=%q", markdown, want)
	}
}

// TestConvertPDF 验证按字号识别标题并按行距划分段落。
func TestConvertPDF(t *testing.T) {
	content := "BT /F1 24 Tf 72 720 Td (Refund Policy) Tj ET\n" +
		"BT /F1 12 Tf 72 690 Td (Returns are accepted within seven days) Tj ET\n" +
		"BT /F1 12 Tf 72 676 Td (after delivery of the order.) Tj ET\n" +
		"BT /F1 12 Tf 72 640 Td (Contact support for shipping labels.) Tj ET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buffer bytes.Buffer
	buffer.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for index, object := range objects {
		offsets[index] = buffer.Len()
		fmt.Fprintf(&buffer, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := buffer.Len()
	fmt.Fprintf(&buffer, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&buffer, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&buffer, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	markdown := convertBytes(t, NewConverter(), "a.pdf", buffer.Bytes())
	want := "# Refund Policy\n\nReturns are accepted within seven days\nafter delivery of the order.\n\nContact support for shipping labels."
	if markdown != want {
		t.Fatalf("markdown=%q\nwant=%q", markdown, want)
	}
}
