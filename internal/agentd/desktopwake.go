// desktopwake.go —— 桌面端信箱与 continue 深链的保留底座（B434 引入；B435 X 退役其余部分）。
//
// 职责：O2 驻场者门铃（residentwake.go#Server.ringResidentDoorbells）复用的六组底座——
//   - 写信：writeDesktopLetter / writeDesktopLetterOnce 把一封信原子写进本机信箱
//     （$HOME/.handoff/desktop-wake/<CLI 会话 id>.<19 位 UnixNano>.md，同纳秒顺延不覆盖）；
//   - continue 深链：claudeContinueURL；
//   - 信箱根：Server.desktopMailboxHome（信箱 HOME 规则唯一定义）；
//   - opener：desktopOpener / PlatformDesktopOpener / Server.SetDesktopOpener（装配在 cmd/agentd.go）；
//   - 清 tmp：Server.sweepDesktopLetterTmp；
//   - 时钟：Server.desktopClock（与薄壳状态 TTL 共用的测试缝）。
//
// 边界：写信箱与发深链是 gateway 对本机的出站 I/O（d_gateway），不进 d_execution、不经 keystone。
// B434 卡级桌面端席位、拉起器、拉起材料、卡级来信与失败段已随 B435 X 删除（契约 b435-contract.md §7）；
// 本文件名被 O2 源码守卫（residentwake_test.go#TestB435ResidentBridgeSourceGuard）按路径扫描，不得改名。
// 信箱格式契约见 docs/superpowers/specs/b434-contract.md 条 67–70、81–84（保留条）。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// desktopOpener 把一条 claude:// 深链交给本机桌面端（生产为 macOS `open`）。
// 测试注入 spy；nil 视为「本机不能发深链」。
type desktopOpener func(url string) error

// errDesktopMailboxKeyUnsafe 是 writeDesktopLetter 拒绝不安全信箱键（CLI 会话 id 为空、
// 含路径分隔符、以 '.' 开头）时包装的哨兵（契约 r12 第 6 条）：驻场者门铃据此判定为确定性
// 拒绝、不再重试（residentwake.go#Server.residentDoorbellUndelivered）。
var errDesktopMailboxKeyUnsafe = errors.New("信箱键不安全")

// desktopLetterNameRetries 是同一 CLI 会话、同一纳秒（或并发写者撞名）时文件名序号
// 顺延的上限：每撞一次 UnixNano 段 +1。64 远大于同纳秒能出现的写者数，超过即报错
// （按「未投递」处置）而不是覆盖。
const desktopLetterNameRetries = 64

// writeDesktopLetter 把 letter 原子写进 home 下的信箱（契约 §3.6，P1=A）。
//
// 参数：home 写信进程用户的 HOME；cliSession 席位身份里的 CLI 会话 id（信箱键）；
// letter 来信全文；now 取 UnixNano 作文件名序号。
// 返回：最终路径 $home/.handoff/desktop-wake/<cliSession>.<19 位零填充 UnixNano>.md。
// 过程：目录不存在以 0o700 创建（条 70）；同目录写 .<最终名>.tmp（O_EXCL、0o600）→
// Sync → os.Link 成最终名 → 删临时名。并发读者只见完整信（条 69；mod 跳过点开头文件）。
// 为什么用 Link 不用 Rename：rename 会静默覆盖同名信——同会话同纳秒两次写信（测试固定
// 时钟、或转交收件与本机消费循环并发）第二封会吞掉第一封（条 68，台账 L4 实测）；Link
// 遇已存在的最终名返回 EEXIST，据此把序号 +1 重试，从不覆盖。
// 注意：cliSession 为空、含 '/' 或 '\'、以 '.' 开头一律拒绝且不碰文件系统——席位
// session_id 段允许这些字符（proto.validateSeatPart），不拦会把信写出信箱目录或写成
// mod 不读的隐藏文件；拒绝错误包 errDesktopMailboxKeyUnsafe（契约 r12 第 6 条）。HOME 为空
// 不属信箱键问题，不包该哨兵。失败时清理本次临时文件。
func writeDesktopLetter(home, cliSession, letter string, now time.Time) (string, error) {
	switch {
	case cliSession == "":
		return "", fmt.Errorf("%w：CLI 会话 id 为空", errDesktopMailboxKeyUnsafe)
	case strings.ContainsAny(cliSession, `/\`):
		return "", fmt.Errorf("%w：%q 含路径分隔符，拒绝写信", errDesktopMailboxKeyUnsafe, cliSession)
	case strings.HasPrefix(cliSession, "."):
		return "", fmt.Errorf("%w：%q 以点开头（会被当成临时/隐藏文件），拒绝写信", errDesktopMailboxKeyUnsafe, cliSession)
	case strings.TrimSpace(home) == "":
		return "", errors.New("信箱所在 HOME 为空")
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("建信箱目录 %s: %w", dir, err)
	}
	base := now.UnixNano()
	for i := int64(0); i < desktopLetterNameRetries; i++ {
		name := fmt.Sprintf("%s.%019d.md", cliSession, base+i)
		final := filepath.Join(dir, name)
		taken, err := writeDesktopLetterOnce(filepath.Join(dir, "."+name+".tmp"), final, letter)
		if err != nil {
			return "", err
		}
		if !taken {
			return final, nil
		}
	}
	return "", fmt.Errorf("信箱 %s 里 %s 起连续 %d 个序号都已占用，拒绝覆盖", dir, cliSession, desktopLetterNameRetries)
}

// writeDesktopLetterOnce 以 tmp → final 的固定名写一次信：tmp 或 final 已存在 →
// taken=true（调用方换下一个序号，不覆盖）；其余失败返回错误并删除本次 tmp。
// Link 成功后删 tmp 失败不报错：信已按最终名落盘（mod 只读最终名），残留的点开头
// tmp 由 sweepDesktopLetterTmp 按年龄清理。
func writeDesktopLetterOnce(tmp, final, letter string) (taken bool, err error) {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("建信箱临时文件 %s: %w", tmp, err)
	}
	if _, err := f.WriteString(letter); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return false, fmt.Errorf("写信箱临时文件 %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return false, fmt.Errorf("落盘信箱临时文件 %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("关闭信箱临时文件 %s: %w", tmp, err)
	}
	if err := os.Link(tmp, final); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, fs.ErrExist) {
			return true, nil
		}
		return false, fmt.Errorf("信件落名 %s → %s: %w", tmp, final, err)
	}
	_ = os.Remove(tmp)
	return false, nil
}

// desktopLetterTmpMaxAge 是信箱临时文件的残留判定年龄：正常写信从建 tmp 到删 tmp 在
// 毫秒级，超过一小时仍在的只可能是写信中途进程崩溃的遗留（V review 遗留项）。
const desktopLetterTmpMaxAge = time.Hour

// sweepDesktopLetterTmp 删除 home 信箱里本 CLI 会话、修改时间早于 desktopLetterTmpMaxAge
// 的临时文件（.<cliSession>.<19 位>.md.tmp）。只在本会话一封信成功落盘后调用（此时
// cliSession 已过 writeDesktopLetter 的安全校验）；用真实墙钟比 mtime（不用 desktopClock
// 测试缝——缝被固定到未来时会误删并发写者的新 tmp）。尽力而为：失败只 Warn，不影响投递。
func (s *Server) sweepDesktopLetterTmp(home, cliSession string) {
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.log.Warn("清理信箱临时文件：读目录失败", "dir", dir, "cause", err)
		return
	}
	pattern := regexp.MustCompile(`^\.` + regexp.QuoteMeta(cliSession) + `\.[0-9]{19}\.md\.tmp$`)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !pattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < desktopLetterTmpMaxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("清理信箱临时文件失败", "file", e.Name(), "cause", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.log.Info("已清理信箱残留临时文件", "dir", dir, "cli_session", cliSession, "removed", removed)
	}
}

// desktopClock 是桌面端时钟缝（V review m3）：生产 desktopNow 为 nil 取 time.Now；测试固定
// 它来复现同纳秒写信、推进失败段退避窗。与薄壳状态 TTL（desktopstate.go）共用同一缝。
func (s *Server) desktopClock() time.Time {
	if s.desktopNow != nil {
		return s.desktopNow()
	}
	return time.Now()
}

// claudeContinueURL 构造 claude://code/continue?session=<url 编码的桌面端会话 id>（条 83）。
// 先过 proto.ValidateDesktopSessionID：id 不合法即报错，调用方不得调 opener（条 84）。
func claudeContinueURL(desktopSession string) (string, error) {
	if err := proto.ValidateDesktopSessionID(desktopSession); err != nil {
		return "", fmt.Errorf("continue 深链的桌面端会话 id 非法: %w", err)
	}
	return proto.ClaudeContinueURLPrefix + url.QueryEscape(desktopSession), nil
}

// errDesktopDeepLinkUnsupported 是非 darwin 平台生产 opener 的固定错误（契约 §3.7）：
// 门铃照常写信，continue 一步按「信已落盘、continue 未发出」处置。
var errDesktopDeepLinkUnsupported = errors.New("本平台不支持 Claude 桌面端深链")

// desktopOpenTimeout 是 darwin `open` 子进程的上限：open 只把 URL 交给 LaunchServices
// 即返回（毫秒级），挂住多半是 GUI 会话异常（屏幕锁定、登出）；给上限免得拖住单协程消费循环。
const desktopOpenTimeout = 15 * time.Second

// PlatformDesktopOpener 按 GOOS 给出生产 opener（契约 §3.7、r12 第 3 条）：darwin →
// openDesktopDeepLinkDarwin；其余平台 → 恒返回 errDesktopDeepLinkUnsupported。
// 参数 goos 而非读 runtime.GOOS：测试在任何平台都能锁住两条分支。
// 注意：NewServer 不调用它——装配只在 cmd/agentd.go 组装点经 SetDesktopOpener 完成，测试进程
// 经 NewServer 得到的 Server 保持 nil opener，永远不会在开发机上真的拉起桌面端。
func PlatformDesktopOpener(goos string) func(string) error {
	if goos != "darwin" {
		return func(string) error { return errDesktopDeepLinkUnsupported }
	}
	return openDesktopDeepLinkDarwin
}

// SetDesktopOpener 装配 continue 深链 opener（契约 r12 第 3 条，与既有 srv.SetX 同形）：只在
// 组装期调用一次，运行期只读不加锁。open 为 nil 等价于未装配（门铃按「信已落盘、
// continue 未发出」处置）。
func (s *Server) SetDesktopOpener(open func(string) error) {
	s.desktopOpen = open
	s.log.Info("桌面端 continue 深链 opener 已装配", "installed", open != nil)
}

// openDesktopDeepLinkDarwin 以 `open <link>` 把 continue 深链交给 Claude 桌面端。
//   - 只放行 proto.ClaudeContinueURLPrefix 开头的链接（opener 不做通用 URL 打开器）；
//   - 子进程带 desktopOpenTimeout 上限；失败把合并输出带进错误，交调用方（门铃）的
//     「未打开」出口记 Error。
func openDesktopDeepLinkDarwin(link string) error {
	if !strings.HasPrefix(link, proto.ClaudeContinueURLPrefix) {
		return fmt.Errorf("拒绝打开非 continue 深链: %q", link)
	}
	ctx, cancel := context.WithTimeout(context.Background(), desktopOpenTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "open", link).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open %s: %w（输出：%s）", link, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// desktopMailboxHome 是信箱根目录规则的唯一定义（契约 r10 第 11 条、r12 第 10 条）：
// Server.desktopHome 非空（装配期注入，测试指向临时目录）原样返回，否则取写信进程用户的
// os.UserHomeDir()。写信方（驻场者门铃）只经它取信箱根，不各自拼 HOME。返回：UserHomeDir
// 失败的原错误（调用方按「未投递」处置）。
func (s *Server) desktopMailboxHome() (string, error) {
	if s.desktopHome != "" {
		return s.desktopHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("取信箱所在 HOME: %w", err)
	}
	return home, nil
}
