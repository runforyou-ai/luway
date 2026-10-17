//go:build server

package reportpage

import "fmt"

// MedianExpr 返回按时长列汇总中位数秒数的 SQL 表达式，四舍五入为整数秒，没有样本时为空。
func MedianExpr(column string) string {
	return fmt.Sprintf("round(percentile_cont(0.5) WITHIN GROUP (ORDER BY %s))::int", column)
}

// P90Expr 返回按时长列汇总 P90 秒数的 SQL 表达式，四舍五入为整数秒，没有样本时为空。
func P90Expr(column string) string {
	return fmt.Sprintf("round(percentile_cont(0.9) WITHIN GROUP (ORDER BY %s))::int", column)
}
