// 双方言表集合对等检（B156.3.7 Task C，协调者 2026-08-26 追加）。
//
// 职责：锁住 ensureSchema 两方言 DDL 的建表集合相等。同一张表只登记进一个
// 方言块时（git 合并冲突只报其中一块、另一块被静默自动合上的真实事故形状），
// 本测试当场翻红——而不是等真 PG 在生产建表时才炸；Task A 的 PG 腿无 DSN 时
// skip 恒绿，盖不住这个缺口，本测试无条件跑。
//
// 边界：判据是「CREATE TABLE 多重集相等」（排序后逐元素等值，重数计入）而不是
// 点名某张表——以后任何人在任一方言块加表漏掉另一块都会红，判据本身不用跟着改；
// 只比集合挡不住同一张表在同一支内重复登记（审阅轮实测该变异存活：集合相等而
// 重数不等），故必须逐元素比对而非包含式差集；也不用行号当判据（行号会漂）。
// 索引与种子 INSERT 不进判据：表缺席会让它们当场失败，危害面小于表缺席且不属
// 本卡范围。
package ledger

import (
	"regexp"
	"slices"
	"testing"
)

// createTableName 从一条 DDL 语句里取 CREATE TABLE IF NOT EXISTS 的表名。
var createTableName = regexp.MustCompile(`(?m)^CREATE TABLE IF NOT EXISTS (\w+)`)

func TestDDLDialectParity(t *testing.T) {
	tablesOf := func(stmts []string) []string {
		var names []string
		for _, stmt := range stmts {
			if m := createTableName.FindStringSubmatch(stmt); m != nil {
				names = append(names, m[1])
			}
		}
		return names
	}
	pg := tablesOf(ddlStatements(true))
	sqlite := tablesOf(ddlStatements(false))

	// 空集守卫：解析失配（DDL 形态或正则被改动）时两集合同为空也算「相等」，
	// 对等检会静默失效成摆设——先把这种空转绿钉死成红。
	if len(pg) == 0 || len(sqlite) == 0 {
		t.Fatalf("表名解析空结果: pg=%d sqlite=%d——createTableName 正则或 DDL 形态已变，对等检失效", len(pg), len(sqlite))
	}

	// 重数判据：排序后逐元素等值（多重集相等）。防「少了」用包含式差集够，
	// 防「多了 / 重复了」只能逐元素等值或计数——集合相等而重数不等的变异
	// （同一支重复登记一张表）在纯集合判据下存活，这里把它钉死成红。
	pgSorted := slices.Clone(pg)
	sqliteSorted := slices.Clone(sqlite)
	slices.Sort(pgSorted)
	slices.Sort(sqliteSorted)
	if !slices.Equal(pgSorted, sqliteSorted) {
		t.Fatalf("两方言建表多重集不等（重数计入判据，排序后逐元素比对）:\nPG 排序后: %v\nSQLite 排序后: %v", pgSorted, sqliteSorted)
	}
}
