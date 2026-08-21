package engine

import (
	"bytes"
	"testing"
)

// 环没写满时，since(0) 必须原样回放全部内容且不报截断。
func TestRingSinceWithinCapacity(t *testing.T) {
	r := newRing(16)
	r.write([]byte("hello"))
	data, start, truncated := r.since(0)
	if string(data) != "hello" || start != 0 || truncated {
		t.Fatalf("since(0) = (%q, %d, %v)，期望 (\"hello\", 0, false)", data, start, truncated)
	}
	if r.total() != 5 {
		t.Fatalf("total = %d，期望 5", r.total())
	}
}

// 表驱动钉住 since 的三类边界：环内续传、被覆盖后截断、游标越界。
func TestRingSinceBoundaries(t *testing.T) {
	r := newRing(8)
	r.write([]byte("0123456789ab")) // 写 12 字节，环容量 8 → 最旧可用字节序号为 4

	cases := []struct {
		name      string
		from      uint64
		wantData  string
		wantStart uint64
		wantTrunc bool
	}{
		{"从头请求被截断到环头", 0, "456789ab", 4, true},
		{"正好命中环头不算截断", 4, "456789ab", 4, false},
		{"环内续传", 10, "ab", 10, false},
		{"游标等于总量返回空", 12, "", 12, false},
		{"游标越界按当前尾部处理", 99, "", 12, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, start, truncated := r.since(c.from)
			if string(data) != c.wantData || start != c.wantStart || truncated != c.wantTrunc {
				t.Fatalf("since(%d) = (%q, %d, %v)，期望 (%q, %d, %v)",
					c.from, data, start, truncated, c.wantData, c.wantStart, c.wantTrunc)
			}
		})
	}
}

// 单次写入超过环容量时，只保留最后 size 个字节，且 total 仍按真实写入量累加。
func TestRingWriteLargerThanCapacity(t *testing.T) {
	r := newRing(4)
	r.write([]byte("abcdefgh"))
	data, start, truncated := r.since(0)
	if !bytes.Equal(data, []byte("efgh")) || start != 4 || !truncated {
		t.Fatalf("since(0) = (%q, %d, %v)，期望 (\"efgh\", 4, true)", data, start, truncated)
	}
	if r.total() != 8 {
		t.Fatalf("total = %d，期望 8", r.total())
	}
}
