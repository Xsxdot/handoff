// 本文件锁死「地址缺 host 的 client 必须当场自曝」这一条。
//
// 为什么值得一个专门的测试文件：空地址曾静默退化成 baseURL="http:"，请求 URL
// 变成 http:/api/status，错误文案是 "http: no Host in request URL"——一个配置
// 缺失被伪装成网络故障，排查要从错误文案一路倒推到字符串裁剪。
package client

import (
	"context"
	"strings"
	"testing"
)

// TestEmptyAddrPoisonsClient：空地址 → 请求明确报「地址不含主机名」。
func TestEmptyAddrPoisonsClient(t *testing.T) {
	_, err := New("", "tok").Status(context.Background())
	if err == nil {
		t.Fatal("空地址必须报错")
	}
	if !strings.Contains(err.Error(), "地址不含主机名") {
		t.Fatalf("错误文案要指向根因，实得: %v", err)
	}
	if strings.Contains(err.Error(), "no Host in request URL") {
		t.Fatalf("不许再把配置缺失伪装成网络错误: %v", err)
	}
}

// TestNormalAddrNotPoisoned：正常地址不受影响（毒化不能误伤）。
func TestNormalAddrNotPoisoned(t *testing.T) {
	if err := New("127.0.0.1:7777", "tok").checkInit(); err != nil {
		t.Fatalf("正常地址不该被毒化: %v", err)
	}
	if err := New("http://127.0.0.1:7777/", "tok").checkInit(); err != nil {
		t.Fatalf("带 scheme 与尾斜杠的地址不该被毒化: %v", err)
	}
}
