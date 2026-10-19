// PG 账本连接的有界性参数与打开错误分类（2026-10-19 mac-mini 重启事故）。
//
// 职责：给出账本进程级连接上限、建库阶段的锁/语句等待上限，以及「打开失败
// 是瞬时还是配置性」的唯一判据。边界：只判错误形状，不做重试——退避重试是
// 调用方（agentd 组装点）的事，CLI 照旧 fail 快。
package ledger

import (
	"context"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// PGMaxOpenConns 是单进程（一个 agentd 或一次 CLI 调用）对共享账本的连接上限。
//
// 为什么必须显式设：账本走 database/sql，其默认 MaxOpenConns=0 即无上限——
// 镜像持有者一启动就有每任务一个 goroutine（实测 517 个）并发读库，各开一条
// 连接，单机即可打穿共享库 max_connections=90。设上限后多余的查询在池上排队，
// 账本查询都是毫秒级，排队代价可忽略。
//
// 全队最坏连接数 = 机器数 × PGMaxOpenConns + 并发 CLI 数 × (PGMaxOpenConns+1)，
// 其中 +1 是 `card wait --follow` 的 LISTEN 裸连接（agentd 不开 LISTEN）。CLI
// 调用基本串行查询，实际只占 1~2 条；建库阶段另有 ≤2 条短连接，Open 返回前关闭。
const PGMaxOpenConns = 8

// pgMaxIdleConns 保留的空闲连接数：稳态够用，又不长期霸占共享库的名额。
const pgMaxIdleConns = 2

// pgConnMaxIdleTime 空闲连接回收时限。
const pgConnMaxIdleTime = 5 * time.Minute

// pgConnectTimeout 单次建连（TCP + TLS + 启动报文）上限；DSN 自带 connect_timeout 时以 DSN 为准。
// 没有它时，对端接了 TCP 却不回握手会让 Open 永远挂住。
const pgConnectTimeout = 10 * time.Second

// schemaLockTimeout 建库阶段（ensureSchema）的锁等待上限。ALTER TABLE cards 要
// AccessExclusiveLock、迁移要全局写锁 advisory lock；任一会话持锁时不设上限就
// 无限等待，且 ALTER 排队期间挡住其后全部 cards 读（锁护航）。只施加在建库
// 专用连接上，不影响运行期 mutate 的锁语义。
const schemaLockTimeout = 10 * time.Second

// schemaStatementTimeout 建库阶段单条语句上限（回填类迁移的兜底）。
const schemaStatementTimeout = 60 * time.Second

// IsTransientOpenError 报告账本打开/建库失败是否为瞬时错误（值得退避重试）。
//
// 瞬时：连接数超限（53xxx）、库正在启停（57P0x）、连接异常（08xxx）、
// 锁等待超时（55P03）、序列化/死锁（40001/40P01）、网络与 DNS 错误、
// 超时、EOF、坏连接。配置性（返回 false，调用方应 fail 快）：DSN 解析失败、
// 鉴权失败（28xxx）、库不存在（3D000）、证书校验失败、其它 SQL 错误。
// nil 返回 false。
func IsTransientOpenError(err error) bool {
	if err == nil {
		return false
	}
	// 服务端给了 SQLSTATE 就以它为准——鉴权失败也包在 ConnectError 里，必须先判
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.Code
		switch {
		case strings.HasPrefix(code, "08"), strings.HasPrefix(code, "53"),
			strings.HasPrefix(code, "57P"):
			return true
		case code == "55P03", code == "40001", code == "40P01":
			return true
		}
		return false
	}
	var parseErr *pgconn.ParseConfigError
	if errors.As(err, &parseErr) {
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) || errors.As(err, &certInvalid) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// 建连失败但服务端没给 SQLSTATE：网络/TLS 层问题
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr)
}

// OpenErrorReason 把打开错误压成一句给人看的短因：只给 SQLSTATE 与服务端报文，
// 不含连接串、主机与账号——agentd 启动闸对未鉴权请求也作答，不能借它外泄库地址。
func OpenErrorReason(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Sprintf("SQLSTATE %s %s", pgErr.Code, pgErr.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "超时"
	}
	return "网络错误"
}
