// 读取查询的阶段诊断件（B409.6 S4）：把「连接池等待 → SQL 首包 → 行读取 →
// 完整读取」的阶段计时与读数收敛为一个私有 helper，供 U1–U5 的 B409 读取查询
// 复用。不是全 Store 全 SQL 的通用拦截器——只有显式接入的查询才产生阶段日志；
// 未接入的既有查询行为零变化。
//
// 字段语义（与 Wave 0 RoomMessagesBeforeContext 的既有阶段一致）：
//   - pool_wait 用 DB.Conn(ctx) 单次测量（先例 rooms.go：DB.Stats().WaitDuration
//     是全池累计值，并发下不能归因给单个请求）；
//   - sql_call 只覆盖 QueryContext（SQL 执行到首批结果到达）；
//   - row_read 覆盖行迭代/扫描；慢首包与大量结果传输的成本由此分开；
//   - db_read 是连接取得后的完整读取（sql_call 与 row_read 是它的子段，
//     相加不冒充总时长，也不与 pool_wait 相加）；
//   - 失败（含取消）保留已完成阶段的读数与 failed_stage，成功为空。
//
// 日志红线：这里与各查询日志只写阶段名、纳秒、行数、字节与有限 error_class，
// 不写 SQL 文本、绑定参数、消息正文或 driver 错误原文（error 原文仅随既有
// 错误返回路径上抛给调用方，不作为 cause 落日志的是本文件的分类值）。
package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// 失败阶段名。start = 查询开始前的预检取消；pool = 连接获取；sql = QueryContext；
// row_read = 行迭代/扫描。
const (
	stageFailedStart   = "start"
	stageFailedPool    = "pool"
	stageFailedSQL     = "sql"
	stageFailedRowRead = "row_read"
)

// readStage 汇总一次只读查询的阶段计时与读数（纳秒/行/字节）。
type readStage struct {
	poolWaitNs  int64
	sqlCallNs   int64
	rowReadNs   int64
	dbReadNs    int64
	rowsRead    int
	resultBytes int64
	failedStage string
}

// logAttrs 展开为 slog 键值对，调用方拼接语义字段：
//
//	log().Info("xx完成", append(diag.Attrs(ctx), st.logAttrs()...)...)
func (st readStage) logAttrs() []any {
	return []any{
		"pool_wait_ns", st.poolWaitNs,
		"sql_call_ns", st.sqlCallNs,
		"row_read_ns", st.rowReadNs,
		"db_read_ns", st.dbReadNs,
		"rows_read", st.rowsRead,
		"result_bytes", st.resultBytes,
	}
}

// readErrorClass 把读路径错误归入有限的安全分类。日志只写分类，不写 driver
// 错误原文——底层错误字符串不可视为安全（B409.6 脱敏红线）。pool 失败与
// SQL 失败分开归类，诊断时能直接看出是拿连接还是执行语句出的问题。
func readErrorClass(err error, failedStage string) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case failedStage == stageFailedPool:
		return "pool_error"
	default:
		return "sql_error"
	}
}

// timedReadQuery 以「显式取连接」方式执行一次只读查询并分离阶段计时。
//
// 参数 scan 在行读取窗口内执行，返回（已读行数，结果字节，错误）；结果字节
// 是数据库驱动交给应用层的 payload 字节数，不代表 TCP/网卡 wire bytes。
// 失败（含取消）返回已完成阶段的 readStage；failed_stage 指出停在哪一段。
// scan 返回的 rows 已由本 helper 关闭，调用方不得重复关闭。
func (s *Store) timedReadQuery(ctx context.Context, query string, args []any,
	scan func(rows *sql.Rows) (int, int64, error)) (readStage, error) {

	started := time.Now()
	var st readStage
	conn, err := s.db.Conn(ctx)
	st.poolWaitNs = time.Since(started).Nanoseconds()
	if err != nil {
		st.failedStage = stageFailedPool
		return st, fmt.Errorf("获取账本连接: %w", err)
	}
	defer conn.Close()
	dbStarted := time.Now()
	queryStarted := time.Now()
	rows, qerr := conn.QueryContext(ctx, s.q(query), args...)
	st.sqlCallNs = time.Since(queryStarted).Nanoseconds()
	if qerr != nil {
		st.dbReadNs = time.Since(dbStarted).Nanoseconds()
		st.failedStage = stageFailedSQL
		return st, qerr
	}
	defer rows.Close()
	rowStarted := time.Now()
	n, bytes, serr := scan(rows)
	st.rowReadNs = time.Since(rowStarted).Nanoseconds()
	st.rowsRead, st.resultBytes = n, bytes
	st.dbReadNs = time.Since(dbStarted).Nanoseconds()
	if serr != nil {
		st.failedStage = stageFailedRowRead
		return st, serr
	}
	return st, nil
}
