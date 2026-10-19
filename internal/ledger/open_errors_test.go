package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// 瞬时（连接数超限、网络、DNS、锁等待超时）→ 退避重试；配置性（DSN 坏、
// 鉴权失败、库不存在）→ fail 快。判错的代价：把配置错误当瞬时 = 永远重试
// 掩盖错配；把瞬时当配置 = 今天的事故（一次 53300 就放弃）。
func TestIsTransientOpenError(t *testing.T) {
	pg := func(code string) error {
		return fmt.Errorf("连接账本库: %w", &pgconn.PgError{Code: code, Message: "x"})
	}
	_, parseErr := pgconn.ParseConfig("postgres://u:p@h:notaport/db")
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"too many clients 53300", pg("53300"), true},
		{"cannot connect now 57P03", pg("57P03"), true},
		{"admin shutdown 57P01", pg("57P01"), true},
		{"connection failure 08006", pg("08006"), true},
		{"lock timeout 55P03", pg("55P03"), true},
		{"serialization 40001", pg("40001"), true},
		{"deadlock 40P01", pg("40P01"), true},
		{"bad password 28P01", pg("28P01"), false},
		{"auth spec 28000", pg("28000"), false},
		{"db missing 3D000", pg("3D000"), false},
		{"ddl syntax 42601", pg("42601"), false},
		{"dns", fmt.Errorf("x: %w", &net.DNSError{Err: "no such host", Name: "h"}), true},
		{"net op", fmt.Errorf("x: %w", &net.OpError{Op: "dial", Err: errors.New("refused")}), true},
		{"eof", fmt.Errorf("tls: %w", io.EOF), true},
		{"deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), true},
		{"dsn parse", parseErr, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsTransientOpenError(c.err); got != c.want {
			t.Errorf("%s: IsTransientOpenError(%v)=%v，期望 %v", c.name, c.err, got, c.want)
		}
	}
}
