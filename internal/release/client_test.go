// 版本查询的测试。全部打 httptest 起的假 GitHub，不碰真实网络。
package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 资产名必须与 B54.1 的 release.yml 产出逐字一致，否则查得到版本却下不到东西。
func TestAssetName(t *testing.T) {
	got := AssetName("v0.1.0", "darwin", "arm64")
	want := "handoff_v0.1.0_darwin_arm64.tar.gz"
	if got != want {
		t.Fatalf("AssetName=%q，期望 %q（必须与 .github/workflows/release.yml 一致）", got, want)
	}
}

func TestDesktopAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		ok           bool
	}{
		{"darwin", "arm64", "handoff-desktop_v0.3.1_darwin_arm64.dmg", true},
		{"windows", "amd64", "handoff-desktop_v0.3.1_windows_amd64.zip", true},
		{"linux", "amd64", "handoff-desktop_v0.3.1_linux_amd64.AppImage", true},
		// 发布流水线只出 darwin/arm64，没有 darwin/amd64 的薄壳资产。
		{"darwin", "amd64", "", false},
		{"freebsd", "amd64", "", false},
	}
	for _, c := range cases {
		got, ok := DesktopAssetName("v0.3.1", c.goos, c.goarch)
		if got != c.want || ok != c.ok {
			t.Fatalf("DesktopAssetName(%s/%s) = %q,%v，想要 %q,%v", c.goos, c.goarch, got, ok, c.want, c.ok)
		}
	}
}

// 薄壳资产用 handoff-desktop_ 前缀发布（release.yml 的 build-desktop-* 两个 job）。
// 这条钉死 CLI 侧拼出的资产名永远不会撞上薄壳包——两边都改名才会失效，那时这条会红。
//
// 为什么值得单列一条：AssetName 的 doc 里已经写明「格式必须与 release.yml 的产出
// 逐字一致，不一致的症状是查得到版本但下不到东西」。加了薄壳资产之后，这个格式
// 又多了一个必须避开的邻居——而两者只差一个字符（handoff_ 与 handoff-desktop_），
// 靠肉眼比对不可靠。
func TestAssetNameNeverMatchesDesktopAsset(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		name := AssetName("v1.2.3", goos, "arm64")
		if strings.HasPrefix(name, "handoff-desktop") {
			t.Fatalf("CLI 资产名撞上薄壳前缀: %s", name)
		}
		if !strings.HasPrefix(name, "handoff_") {
			t.Fatalf("CLI 资产名不再以 handoff_ 开头，install.sh 会取不到: %s", name)
		}
	}
}

// fakeAPI 返回一个假的 releases/latest 响应。
func fakeAPI(t *testing.T, body string, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/Xsxdot/handoff/releases/latest") {
			t.Errorf("请求路径不对: %s", r.URL.Path)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 正常响应：解析出 tag 与四平台资产 + checksums。
func TestLatestParsesTagAndAssets(t *testing.T) {
	body := `{"tag_name":"v0.2.0","assets":[
	  {"name":"handoff_v0.2.0_darwin_arm64.tar.gz","browser_download_url":"https://example/d1"},
	  {"name":"handoff_v0.2.0_linux_amd64.tar.gz","browser_download_url":"https://example/d2"},
	  {"name":"checksums.txt","browser_download_url":"https://example/c"}]}`
	srv := fakeAPI(t, body, 200)
	c := NewClient(nil)
	c.APIBase = srv.URL
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Tag != "v0.2.0" {
		t.Fatalf("Tag=%q", rel.Tag)
	}
	a, ok := rel.AssetFor("darwin", "arm64")
	if !ok || a.URL != "https://example/d1" {
		t.Fatalf("darwin/arm64 资产解析错: %+v ok=%v", a, ok)
	}
	ck, ok := rel.Checksums()
	if !ok || ck.URL != "https://example/c" {
		t.Fatalf("checksums 资产解析错: %+v ok=%v", ck, ok)
	}
}

// 本平台资产缺失必须能被查出来——这正是 D8 选 API 而不是解析 302 的理由：
// 缺资产在**查版本**这一步就暴露，而不是等到下载时才 404。
func TestAssetForMissingPlatform(t *testing.T) {
	rel := Release{Tag: "v0.2.0", Assets: []Asset{{Name: "handoff_v0.2.0_linux_amd64.tar.gz", URL: "u"}}}
	if _, ok := rel.AssetFor("darwin", "arm64"); ok {
		t.Fatal("不存在的平台资产不该报 ok")
	}
}

// 限流/服务端错误必须带上状态码——「查版本失败」不带码等于没法判断是限流还是挂了。
func TestLatestSurfacesHTTPStatus(t *testing.T) {
	srv := fakeAPI(t, `{"message":"API rate limit exceeded"}`, 403)
	c := NewClient(nil)
	c.APIBase = srv.URL
	_, err := c.Latest(context.Background())
	if err == nil {
		t.Fatal("403 应报错")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("报错应含状态码，得到: %v", err)
	}
}

// tag 为空的畸形响应要拦下来，不能往下走出一个空版本号。
//
// why：空 tag 会一路流到「tag != 当前版本」的比较里恒为真，于是每轮都尝试
// 下载一个名为 handoff__darwin_arm64.tar.gz 的东西，永远失败且永远重试。
func TestLatestRejectsEmptyTag(t *testing.T) {
	srv := fakeAPI(t, `{"tag_name":"","assets":[]}`, 200)
	c := NewClient(nil)
	c.APIBase = srv.URL
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("空 tag 应报错")
	}
}

// AssetName 的扩展名是与 release.yml 的契约。Windows 出 zip 而非 tar.gz：
// zip 在资源管理器里双击即开，且 Expand-Archive 人人都有，tar.exe 只有
// Win10 1803+ 才有。改这里必须同步改 workflow 与两个 install 脚本。
func TestAssetNameExtensionPerOS(t *testing.T) {
	for _, c := range []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "handoff_v1.2.3_darwin_arm64.tar.gz"},
		{"darwin", "amd64", "handoff_v1.2.3_darwin_amd64.tar.gz"},
		{"linux", "amd64", "handoff_v1.2.3_linux_amd64.tar.gz"},
		{"linux", "arm64", "handoff_v1.2.3_linux_arm64.tar.gz"},
		{"windows", "amd64", "handoff_v1.2.3_windows_amd64.zip"},
		{"windows", "arm64", "handoff_v1.2.3_windows_arm64.zip"},
	} {
		if got := AssetName("v1.2.3", c.goos, c.goarch); got != c.want {
			t.Errorf("AssetName(v1.2.3, %s, %s) = %q，期望 %q", c.goos, c.goarch, got, c.want)
		}
	}
}

// 默认仓库与端点不能被改掉——它们是硬约束。
func TestDefaults(t *testing.T) {
	if DefaultRepo != "Xsxdot/handoff" {
		t.Errorf("DefaultRepo=%q，GitHub owner 是 Xsxdot", DefaultRepo)
	}
	c := NewClient(nil)
	if c.APIBase != DefaultAPIBase || c.Repo != DefaultRepo {
		t.Errorf("NewClient 默认值不对: %+v", c)
	}
}
