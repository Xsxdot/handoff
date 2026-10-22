// agy.go —— agy（Antigravity CLI）额度适配（B453 段 2，spec r1.1）。
//
// 职责：无 HTTP 接口，照 orca 以账号 home_dir 作 HOME 在所在机跑
// `agy -p /usage --output-format json --print-timeout 20s`，从信封里找 groups[].buckets[]
// （remaining_fraction、reset_time、window、disabled），已用 = (1−remaining)×100，每个模型组
// 的 5h/周各成一个窗口（Window.Group = 组名），disabled 的 bucket 不计。
// 版本门：`agy --version` < 1.1.11（或无法识别）时旧版会把 /usage 当普通 prompt 白花一次
// 额度——此时不执行 /usage，报 version_too_old。指纹取信封里的登录身份（邮箱/账号 id），
// 取不到留空。信封形状来自 facts-orca §1（**未经真机**，U4），解析对未知字段宽容。
//
// 边界：handoff 不读不写 agy 的凭据文件；子进程独立进程组，超时与 ctx 取消都杀整组；
// 非 unix 平台不执行（unsupported_platform）。
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	agyPrintTimeout = "20s"
	// agyDefaultTimeout ≥ print-timeout 20s + 余量（plan §6）。
	agyDefaultTimeout = 40 * time.Second
	agyVersionTimeout = 10 * time.Second
	agyStdoutLimit    = 1 << 20
	agyStderrTail     = 400
)

// agyMinVersion 是不会把 /usage 当 prompt 消耗额度的最低版本（orca 版本门）。
var agyMinVersion = [3]int{1, 1, 11}

var semverRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// Agy 是 agy 适配器。零值字段取缺省。
type Agy struct {
	Binary  string        // 缺省 "agy"（经 exec.LookPath 找，agentd 启动时已扩 PATH）
	Timeout time.Duration // /usage 子进程总时限，缺省 40s
}

// Probe 实现 Prober。req.Entry 对 agy 无意义（登录态由 CLI 自管）。
func (a *Agy) Probe(ctx context.Context, req Request) (Snapshot, error) {
	if !procGroupSupported {
		return Snapshot{}, fail(CategoryUnsupportedPlatform, "本平台未支持 agy 额度探测（需要进程组整组终止）")
	}
	if req.Home == "" {
		return Snapshot{}, fail(CategoryCredentialMissing, "账号未给凭据 HOME")
	}
	name := a.Binary
	if name == "" {
		name = "agy"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return Snapshot{}, fail(CategoryCLIMissing, "找不到 agy 可执行文件（%s）：在该机安装 agy 并确认 agentd 的 PATH 含其目录", name)
	}
	env := agyEnv(req.Home, req.Proxy)
	vout, _, verr := runGroup(ctx, agyVersionTimeout, bin, env, "--version")
	if verr != nil {
		log().Warn("agy 版本查询失败", "bin", bin, "cause", verr)
		return Snapshot{}, verr
	}
	version, ok := parseSemver(string(vout))
	if !ok || versionLess(version, agyMinVersion) {
		f := fail(CategoryVersionTooOld, "agy 版本过旧或无法识别（%s，需 ≥ 1.1.11），探测会消耗额度，已跳过",
			strings.TrimSpace(snippet(vout)))
		log().Warn("agy 版本门未过，跳过 /usage", "bin", bin, "version_output", strings.TrimSpace(snippet(vout)))
		return Snapshot{}, f
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = agyDefaultTimeout
	}
	started := time.Now()
	log().Info("agy /usage 开始", "bin", bin, "home", req.Home, "version", fmt.Sprint(version), "timeout", timeout)
	out, stderr, err := runGroup(ctx, timeout, bin, env, "-p", "/usage", "--output-format", "json", "--print-timeout", agyPrintTimeout)
	if err != nil {
		var f *Failure
		if errors.As(err, &f) && f.Category == CategoryUpstream && looksLikeLoginNeeded(stderr) {
			err = fail(CategoryCredentialMissing, "agy 未登录或登录态失效（在该机以该 HOME 执行 agy login）：%s", tail(stderr))
		}
		log().Warn("agy /usage 失败", "bin", bin, "elapsed", time.Since(started), "cause", err)
		return Snapshot{}, err
	}
	snap, err := ParseAgyUsage(out)
	if err != nil {
		log().Warn("agy /usage 输出解析失败", "elapsed", time.Since(started), "cause", err)
		return Snapshot{}, err
	}
	snap.Fingerprint = Fingerprint(agyIdentity(out))
	log().Info("agy /usage 完成", "windows", len(snap.Windows), "fingerprint_set", snap.Fingerprint != "",
		"elapsed", time.Since(started))
	return snap, nil
}

// agyEnv 以账号 HOME 运行；账号代理（或回落代理）非空时覆盖六个代理键，NO_PROXY 沿用进程环境。
func agyEnv(home, proxy string) []string {
	drop := map[string]bool{"HOME": true}
	if proxy != "" {
		for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
			drop[k] = true
		}
	}
	env := make([]string, 0, len(os.Environ())+7)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home)
	if proxy != "" {
		for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
			env = append(env, k+"="+proxy)
		}
	}
	return env
}

// runGroup 在独立进程组里跑一次命令：超时或 ctx 取消杀整组；非零退出归 upstream（带 stderr 尾部）。
func runGroup(ctx context.Context, timeout time.Duration, bin string, env []string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	isolateGroup(cmd)
	// 孙进程可能继承并占住输出管道：取消后最多再等 2s 就强关管道返回。
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &stdout, limit: agyStdoutLimit}
	cmd.Stderr = &limitedBuffer{buf: &stderr, limit: agyStdoutLimit}
	err := cmd.Run()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, stderr.Bytes(), fail(CategoryTimeout, "agy %s 超时（%s），已终止其进程组", strings.Join(args, " "), timeout)
		}
		return nil, stderr.Bytes(), fail(CategoryTimeout, "agy %s 被取消：%v", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		return nil, stderr.Bytes(), fail(CategoryUpstream, "agy %s 失败（%v）：%s", strings.Join(args, " "), err, tail(stderr.Bytes()))
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

type limitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.limit - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > agyStderrTail {
		s = "…" + s[len(s)-agyStderrTail:]
	}
	return s
}

func looksLikeLoginNeeded(stderr []byte) bool {
	s := strings.ToLower(string(stderr))
	for _, k := range []string{"not logged in", "login", "log in", "sign in", "unauthenticated", "authenticate"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func parseSemver(s string) ([3]int, bool) {
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// agyEnvelope 从 stdout 里取 JSON 信封：整体能解析就用整体，否则取最后一个以 { 开头
// 的行起到末尾（CLI 可能先打进度/告警行）。
func agyEnvelope(out []byte) (any, bool) {
	var v any
	if json.Unmarshal(bytes.TrimSpace(out), &v) == nil {
		return v, true
	}
	lines := bytes.Split(out, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if bytes.HasPrefix(bytes.TrimSpace(lines[i]), []byte("{")) {
			if json.Unmarshal(bytes.TrimSpace(bytes.Join(lines[i:], []byte("\n"))), &v) == nil {
				return v, true
			}
		}
	}
	for i := 0; i < len(lines); i++ { // 多行 JSON：从第一个 { 行起
		if bytes.HasPrefix(bytes.TrimSpace(lines[i]), []byte("{")) {
			if json.Unmarshal(bytes.TrimSpace(bytes.Join(lines[i:], []byte("\n"))), &v) == nil {
				return v, true
			}
		}
	}
	return nil, false
}

// ParseAgyUsage 解析 `agy -p /usage --output-format json` 的输出（外部缝上的解析入口）。
// 在信封里深度优先找第一个带 groups 数组的对象；信封带 error 时判 upstream。
func ParseAgyUsage(out []byte) (Snapshot, error) {
	env, ok := agyEnvelope(out)
	if !ok {
		return Snapshot{}, fail(CategoryParse, "agy 输出不是 JSON：%s", snippet(out))
	}
	if root, ok := env.(map[string]any); ok {
		if e, has := root["error"]; has && e != nil {
			msg := fmt.Sprint(e)
			if m, ok := e.(map[string]any); ok && m["message"] != nil {
				msg = fmt.Sprint(m["message"])
			}
			return Snapshot{}, fail(CategoryUpstream, "agy /usage 返回错误：%s", msg)
		}
	}
	groups, ok := findKey(env, "groups").([]any)
	if !ok {
		return Snapshot{}, fail(CategoryParse, "agy 输出里没有 groups：%s", snippet(out))
	}
	var snap Snapshot
	for _, g := range groups {
		gm := asMap(g)
		gname, _ := gm["name"].(string)
		buckets, _ := gm["buckets"].([]any)
		for _, b := range buckets {
			bm := asMap(b)
			if disabled, _ := bm["disabled"].(bool); disabled {
				continue
			}
			window, _ := bm["window"].(string)
			key := agyWindowKey(window)
			if key == "" {
				log().Debug("agy bucket 窗口未知，忽略", "group", gname, "window", window)
				continue
			}
			rem, ok := num(bm["remaining_fraction"])
			if !ok {
				continue
			}
			w := Window{Key: key, Group: gname, UsedPercent: clampPercent((1 - rem) * 100)}
			if raw, err := json.Marshal(bm["reset_time"]); err == nil {
				if t, perr := parseMoment(raw); perr == nil {
					w.ResetsAt = t
				}
			}
			snap.Windows = append(snap.Windows, w)
		}
	}
	if len(snap.Windows) == 0 {
		return Snapshot{}, fail(CategoryParse, "agy 输出里没有可用的 5h/周窗口（全部禁用或窗口未知）：%s", snippet(out))
	}
	return snap, nil
}

func agyWindowKey(window string) string {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "5h", "5_hour", "five_hour", "session":
		return WindowSession
	case "weekly", "week", "7d", "1w":
		return WindowWeekly
	case "monthly", "month", "30d":
		return WindowMonthly
	}
	return ""
}

// agyIdentity 在信封里找登录身份：优先 email 类键，其次账号/用户 id。取不到返回空。
func agyIdentity(out []byte) string {
	env, ok := agyEnvelope(out)
	if !ok {
		return ""
	}
	for _, k := range []string{"email", "account_email", "user_email", "account_id", "user_id"} {
		if s, ok := findKey(env, k).(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// findKey 深度优先找第一个名为 key 的字段值。
func findKey(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			return val
		}
		for _, child := range x {
			if r := findKey(child, key); r != nil {
				return r
			}
		}
	case []any:
		for _, child := range x {
			if r := findKey(child, key); r != nil {
				return r
			}
		}
	}
	return nil
}
