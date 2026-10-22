// login_api.go —— 新增登录、单 HOME 检查与「立即发现」的 HTTP 面（B464 段 3，plan A10–A12、
// Q18、V13）。三条路由与账号路由同一 api mux/鉴权门：
//   - POST /api/logins/home?machine=：入口机转到目标机（一跳，保留 ?machine= 供目标机自证），
//     目标机只在 ~/.handoff/home/ 下建 0700 目录并给出登录命令；ssh 行由入口机按设备表
//     User@host 换算（relay 目标与本机不给）；
//   - POST /api/logins/check?machine=：目标机对指定 HOME 跑单 HOME 发现并写账本，回答「新账号 /
//     已挂到账号」；同样一跳转发，转发到达非目标机 409；
//   - POST /api/accounts/discover[?machine=]：无 machine = 入口机对全部已登记机器（含本机）并发
//     扇出，单机失败只写进该行、整体恒 200；带防环头时只跑本机、不再外扇。
//
// 边界：
//   - 登录命令表只在本文件一处（plan Q18/R0-8），建 HOME 响应与 LoginView.relogin 同源；
//   - 不读凭据：建目录只说位置与命令；检查交给 accountwork 的离线取材（回短指纹与 label）；
//   - 目录名白名单 [a-z0-9-]、只建在 ~/.handoff/home/ 下、拒绝穿越与覆盖已存在目录；
//   - darwin 上的 claude 登录存在钥匙串、同机所有 HOME 共用（Q6 分支 b，accounts.LoginMainHomeOnly），
//     新增登录只允许主 HOME，因此本端点在该平台拒绝新建隔离 HOME 并说明原因。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd/accountwork"
	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// discoverPerMachineTimeout 是「立即发现」单台机器的预算（plan §9：≤30s/机）。
const discoverPerMachineTimeout = 30 * time.Second

// loginHomeDir 取展开 `~` 的主 HOME。测试替换成夹具目录（不改进程 HOME，避免 Go 模块缓存等
// 副作用）；生产为 os.UserHomeDir。
var loginHomeDir = os.UserHomeDir

// loginGOOS 是本机平台（kind 规则用）。测试注入以锁住 darwin/linux 两种新增登录行为。
var loginGOOS = runtime.GOOS

// loginDirPattern 是新增登录目录名的字符集（plan Q18）：只允许小写字母、数字与连字符。
var loginDirPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// isolatedHomePrefix 是隔离 HOME 的机器无关写法前缀（与账号域 accounts.MainHome 配套）。
const isolatedHomePrefix = "~/.handoff/home/"

// SetAccountDiscoverer 注入本机发现器（只在挂共享账本的机器上装配）。
func (s *Server) SetAccountDiscoverer(d *accountwork.Discoverer) { s.accountDiscoverer = d }

// loginCommand 是一条登录命令与补充做法（plan R0-8 的命令表）。
type loginCommand struct {
	run  string
	note string
}

// loginCommands 是各 kind 的登录命令（真机核实后的原文，plan R0-8）。第三方 API key 需要
// provider，在 loginCommandFor 里单独拼。
var loginCommands = map[string]loginCommand{
	accounts.KindOpencodeGo: {run: "opencode console login",
		note: "第三方 API key 登录：opencode auth login -p <provider>"},
	accounts.KindCodex:  {run: "codex login", note: "远端或无浏览器机器用 codex login --device-auth"},
	accounts.KindGrok:   {run: "grok login"},
	accounts.KindAgy:    {run: "agy", note: "交互运行 agy，按提示完成登录"},
	accounts.KindClaude: {run: "claude auth login"},
}

// loginAgentKinds 是新增登录可选 agent → 账号 kind（A11 的五值；apikey 不是新增登录入口，
// 它由 opencode 的第三方 key 登录被发现）。
var loginAgentKinds = map[string]string{
	"opencode": accounts.KindOpencodeGo,
	"codex":    accounts.KindCodex,
	"grok":     accounts.KindGrok,
	"agy":      accounts.KindAgy,
	"claude":   accounts.KindClaude,
}

// loginCommandFor 返回某 kind（+ provider）的登录命令。未知 kind / 缺 provider 的 apikey 返回
// ok=false（读面不给 relogin，建 HOME 已被 agent 白名单挡住）。
func loginCommandFor(kind, provider string) (loginCommand, bool) {
	if kind == accounts.KindAPIKey {
		p := strings.TrimSpace(provider)
		if p == "" {
			return loginCommand{}, false
		}
		return loginCommand{run: "opencode auth login -p " + p}, true
	}
	c, ok := loginCommands[kind]
	return c, ok
}

// loginRunCommand 给登录命令加 `HOME=<登录 HOME 绝对路径>` 前缀（plan R0-8：全部以 HOME 前缀运行）。
func loginRunCommand(homeAbs, run string) string {
	if strings.TrimSpace(homeAbs) == "" {
		return run
	}
	return "HOME=" + homeAbs + " " + run
}

// registerLoginRoutes 注册段 3 的路由（registerAccountRoutes 内调用一次）。
func (s *Server) registerLoginRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/logins/home", s.withAccounts(s.handleLoginHome))
	api.HandleFunc("POST /api/logins/check", s.withAccounts(s.handleLoginCheck))
	api.HandleFunc("POST /api/accounts/discover", s.withAccounts(s.handleAccountDiscover))
}

// handleLoginHome POST /api/logins/home?machine=（A11）：本机直接建；他机一跳转发后由入口机补
// ssh 行；目录非法 400、已存在 409、目标机不可达 502。
func (s *Server) handleLoginHome(w http.ResponseWriter, r *http.Request) {
	machine := strings.TrimSpace(r.URL.Query().Get("machine"))
	if isForwarded(r) {
		if machine != "" && !s.IsSelfMachine(machine) {
			s.log.Warn("新增登录：转发到达非目标机", "machine", machine, "self", s.conf().Name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "转发到达非目标机 " + s.conf().Name + "（指定的是 " + machine + "），拒绝建 HOME",
			})
			return
		}
		machine = ""
	}
	if machine != "" && !s.IsSelfMachine(machine) {
		s.forwardLoginHome(w, r, machine)
		return
	}
	s.createLoginHome(w, r)
}

// createLoginHome 是目标机侧的建 HOME：只在 ~/.handoff/home/ 下一层 mkdir 0700。
func (s *Server) createLoginHome(w http.ResponseWriter, r *http.Request) {
	var in proto.LoginHomeReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.log.Warn("新增登录：请求体解析失败", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	agent := strings.TrimSpace(in.Agent)
	kind, ok := loginAgentKinds[agent]
	if !ok {
		s.log.Warn("新增登录：未知 agent", "agent", agent)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "agent 只能是 opencode、codex、grok、agy、claude",
		})
		return
	}
	dir := strings.TrimSpace(in.Dir)
	if !loginDirPattern.MatchString(dir) {
		s.log.Warn("新增登录：目录名不合法", "agent", agent, "dir", dir)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "目录名只允许小写字母、数字与连字符（[a-z0-9-]）",
		})
		return
	}
	if accounts.LoginMainHomeOnly(kind, loginGOOS) {
		s.log.Warn("新增登录被拒：该 kind 在此平台只允许主 HOME", "agent", agent, "goos", loginGOOS)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "这台机器（" + loginGOOS + "）上 " + agent + " 的凭据在系统钥匙串里、同机所有 HOME 共用一份，" +
				"建新 HOME 登录也不会被识别；请直接在主 HOME 登录 " + agent + "（不建新目录）",
		})
		return
	}
	mainAbs, err := loginHomeDir()
	if err != nil || mainAbs == "" || !filepath.IsAbs(mainAbs) {
		s.log.Error("新增登录：取主 HOME 失败", "home", mainAbs, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("取主 HOME 失败: %v", err)})
		return
	}
	root := filepath.Join(filepath.Clean(mainAbs), ".handoff", "home")
	if err := os.MkdirAll(root, 0o700); err != nil {
		s.log.Error("新增登录：创建隔离 HOME 根失败", "root", root, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("创建 %s 失败: %v", root, err)})
		return
	}
	// 根必须是真实目录：软链会把「只在 ~/.handoff/home/ 下建」变成任意位置写入。
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		s.log.Warn("新增登录：隔离 HOME 根不是真实目录", "root", root, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": root + " 不是真实目录（可能是软链），拒绝在其下建登录 HOME",
		})
		return
	}
	abs := filepath.Join(root, dir)
	if err := os.Mkdir(abs, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			s.log.Warn("新增登录：目录已存在", "home", abs)
			writeJSON(w, http.StatusConflict, map[string]string{"error": "登录 HOME 已存在：" + abs})
			return
		}
		s.log.Error("新增登录：建目录失败", "home", abs, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("创建 %s 失败: %v", abs, err)})
		return
	}
	// umask 只会削权限，这里显式落到 0700（plan Q18）。
	if err := os.Chmod(abs, 0o700); err != nil {
		s.log.Warn("新增登录：设置目录权限失败", "home", abs, "cause", err)
	}
	cmd, ok := loginCommandFor(kind, "")
	if !ok {
		// agent 白名单内的 kind 都有命令；走不到这里，防呆仍给出可行动文案。
		s.log.Error("新增登录：命令表缺 kind", "kind", kind)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "登录命令表缺 kind " + kind})
		return
	}
	resp := proto.LoginHomeResp{Home: isolatedHomePrefix + dir, Command: proto.LoginCommand{
		Run: loginRunCommand(abs, cmd.run), Note: cmd.note}}
	s.log.Info("新增登录：已建 HOME", "agent", agent, "kind", kind, "dir", dir, "home", abs)
	writeJSON(w, http.StatusOK, resp)
}

// forwardLoginHome 把建 HOME 请求一跳转到目标机（保留 ?machine=），成功后按入口机设备表补 ssh 行。
func (s *Server) forwardLoginHome(w http.ResponseWriter, r *http.Request, machine string) {
	body, ok := s.readShortBody(w, r)
	if !ok {
		return
	}
	target, ok := s.remoteTarget(machine)
	if !ok {
		s.log.Warn("新增登录转发被拒：机器未登记", "machine", machine)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": unknownMachineMsg(machine)})
		return
	}
	cl, err := s.pool.For(machine)
	if err != nil {
		s.log.Error("新增登录转发失败：取目标客户端", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	status, headers, payload, err := s.forwardJSONKeepQuery(r, machine, cl, target.Token, body)
	if err != nil {
		s.log.Error("新增登录转发失败：目标机不可达", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		s.log.Warn("新增登录转发：目标机拒绝，原样回送", "machine", machine, "status", status)
		copyForwardHeaders(w, headers)
		w.WriteHeader(status)
		if _, err := w.Write(payload); err != nil {
			s.log.Warn("新增登录转发：回送目标机错误失败", "machine", machine, "cause", err)
		}
		return
	}
	var resp proto.LoginHomeResp
	if err := json.Unmarshal(payload, &resp); err != nil {
		s.log.Error("新增登录转发：目标响应无法解析", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "目标机新增登录响应无法解析: " + err.Error()})
		return
	}
	resp.Command.SSH = s.loginSSHLine(machine)
	s.log.Info("新增登录转发完成", "machine", machine, "home", resp.Home, "ssh", resp.Command.SSH != "")
	copyForwardHeaders(w, headers)
	writeJSON(w, http.StatusOK, resp)
}

// handleLoginCheck POST /api/logins/check?machine=（A12）：目标机对指定 HOME 跑单 HOME 发现并
// 写账本；一跳转发，转发到达非目标机 409。
func (s *Server) handleLoginCheck(w http.ResponseWriter, r *http.Request) {
	machine := strings.TrimSpace(r.URL.Query().Get("machine"))
	if isForwarded(r) {
		if machine != "" && !s.IsSelfMachine(machine) {
			s.log.Warn("单 HOME 检查：转发到达非目标机", "machine", machine, "self", s.conf().Name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "转发到达非目标机 " + s.conf().Name + "（指定的是 " + machine + "），拒绝检查",
			})
			return
		}
		machine = ""
	}
	if machine != "" && !s.IsSelfMachine(machine) {
		s.forwardLoginCheck(w, r, machine)
		return
	}
	s.checkLoginHome(w, r)
}

// forwardLoginCheck 把检查请求一跳转到目标机并原样回送结果（响应不含入口机相关信息）。
func (s *Server) forwardLoginCheck(w http.ResponseWriter, r *http.Request, machine string) {
	body, ok := s.readShortBody(w, r)
	if !ok {
		return
	}
	target, ok := s.remoteTarget(machine)
	if !ok {
		s.log.Warn("单 HOME 检查转发被拒：机器未登记", "machine", machine)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": unknownMachineMsg(machine)})
		return
	}
	cl, err := s.pool.For(machine)
	if err != nil {
		s.log.Error("单 HOME 检查转发失败：取目标客户端", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	status, headers, payload, err := s.forwardJSONKeepQuery(r, machine, cl, target.Token, body)
	if err != nil {
		s.log.Error("单 HOME 检查转发失败：目标机不可达", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	s.log.Info("单 HOME 检查转发完成", "machine", machine, "status", status)
	copyForwardHeaders(w, headers)
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		s.log.Warn("单 HOME 检查转发：回送结果失败", "machine", machine, "cause", err)
	}
}

// checkLoginHome 是目标机侧的单 HOME 检查：读请求 → 校验 HOME 形状 → 发现写账本 → 交回识别结果。
func (s *Server) checkLoginHome(w http.ResponseWriter, r *http.Request) {
	var in proto.LoginCheckReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.log.Warn("单 HOME 检查：请求体解析失败", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	home, ok := normalizeLoginHome(in.Home)
	if !ok {
		s.log.Warn("单 HOME 检查：HOME 形状不合法", "home", in.Home)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "home 只接受 ~ 或 " + isolatedHomePrefix + "<目录名>（目录名 [a-z0-9-]）",
		})
		return
	}
	if s.accountDiscoverer == nil {
		s.log.Warn("单 HOME 检查：本机发现器未运行", "home", home)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "本机账号发现器未运行（该机未挂共享账本？看 agentd 启动日志 dsn_configured）",
		})
		return
	}
	res, err := s.accountDiscoverer.CheckHome(r.Context(), home)
	if err != nil {
		s.log.Error("单 HOME 检查失败", "home", home, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]proto.LoginCheckItem, 0, len(res.Logins))
	for _, l := range res.Logins {
		if l.Home != home || l.State == accounts.LoginGone {
			continue
		}
		v, err := s.accounts.Get(l.Account)
		if err != nil {
			s.log.Error("单 HOME 检查：读识别到的账号失败", "home", home, "account", l.Account, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		items = append(items, proto.LoginCheckItem{
			Login: l.ID, Account: l.Account, AccountName: v.Account.Name,
			NewAccount: res.NewAccountLogins[l.ID], AccountLogins: len(v.Logins),
		})
	}
	s.log.Info("单 HOME 检查：识别结果", "home", home, "logins", len(items))
	writeJSON(w, http.StatusOK, proto.LoginCheckResp{Logins: items})
}

// handleAccountDiscover POST /api/accounts/discover[?machine=]（A10）：无 machine = 全队扇出；
// 带 machine = 只跑那一台；带防环头 = 只跑本机、不再外扇。单机失败不改变整体 200。
func (s *Server) handleAccountDiscover(w http.ResponseWriter, r *http.Request) {
	machine := strings.TrimSpace(r.URL.Query().Get("machine"))
	if isForwarded(r) {
		if machine != "" && !s.IsSelfMachine(machine) {
			s.log.Warn("立即发现：转发到达非目标机", "machine", machine, "self", s.conf().Name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "转发到达非目标机 " + s.conf().Name + "（指定的是 " + machine + "），拒绝发现",
			})
			return
		}
		res := s.discoverLocal(r.Context())
		writeJSON(w, http.StatusOK, proto.AccountDiscoverResp{Machines: []proto.MachineDiscoverResult{res}})
		return
	}
	if machine != "" {
		res := s.discoverOne(r.Context(), machine)
		writeJSON(w, http.StatusOK, proto.AccountDiscoverResp{Machines: []proto.MachineDiscoverResult{res}})
		return
	}
	names := s.discoverMachineNames()
	results := make([]proto.MachineDiscoverResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			results[i] = s.discoverOne(r.Context(), name)
		}(i, name)
	}
	wg.Wait()
	s.log.Info("立即发现扇出完成", "machines", len(results))
	writeJSON(w, http.StatusOK, proto.AccountDiscoverResp{Machines: results})
}

// discoverMachineNames 是扇出对象：本机自名（若有）在前，其余取池（有 DSN 时即设备表）。
func (s *Server) discoverMachineNames() []string {
	cfg := s.conf()
	names := make([]string, 0, len(s.pool.Names())+1)
	if cfg != nil && cfg.Name != "" {
		names = append(names, cfg.Name)
	}
	for _, n := range s.pool.Names() {
		if cfg != nil && n == cfg.Name {
			continue
		}
		names = append(names, n)
	}
	return names
}

// discoverOne 跑一台机器：本机直接跑发现器，他机经一跳转发（带防环头）。
func (s *Server) discoverOne(ctx context.Context, machine string) proto.MachineDiscoverResult {
	if s.IsSelfMachine(machine) {
		return s.discoverLocal(ctx)
	}
	return s.discoverRemote(ctx, machine)
}

// discoverLocal 跑本机一轮发现。发现器未装配 / 失败都只体现在这一行，不改变整体 HTTP 200。
func (s *Server) discoverLocal(ctx context.Context) proto.MachineDiscoverResult {
	self := ""
	if cfg := s.conf(); cfg != nil {
		self = cfg.Name
	}
	if s.accountDiscoverer == nil {
		s.log.Warn("立即发现：本机发现器未运行", "machine", self)
		return proto.MachineDiscoverResult{Machine: self, OK: false,
			Error: "本机账号发现器未运行（该机未挂共享账本）"}
	}
	runCtx, cancel := context.WithTimeout(ctx, discoverPerMachineTimeout)
	defer cancel()
	if _, err := s.accountDiscoverer.RunOnce(runCtx); err != nil {
		s.log.Warn("立即发现：本机失败", "machine", self, "cause", err)
		return proto.MachineDiscoverResult{Machine: self, OK: false, Error: err.Error()}
	}
	at := time.Now().UTC()
	s.log.Info("立即发现：本机完成", "machine", self)
	return proto.MachineDiscoverResult{Machine: self, OK: true, At: &at}
}

// discoverRemote 把「立即发现」一跳转发到目标机（目标机带防环头只跑本机）。
func (s *Server) discoverRemote(ctx context.Context, machine string) proto.MachineDiscoverResult {
	if _, ok := s.remoteTarget(machine); !ok {
		s.log.Warn("立即发现：机器未登记", "machine", machine)
		return proto.MachineDiscoverResult{Machine: machine, OK: false, Error: unknownMachineMsg(machine)}
	}
	cl, err := s.pool.For(machine)
	if err != nil {
		s.log.Warn("立即发现：取目标客户端失败", "machine", machine, "cause", err)
		return proto.MachineDiscoverResult{Machine: machine, OK: false, Error: "取目标客户端失败: " + err.Error()}
	}
	runCtx, cancel := context.WithTimeout(ctx, discoverPerMachineTimeout)
	defer cancel()
	// MarkForwarded：目标机看到防环头后只跑本机，不再外扇（plan W2）；token 与路由都取自池。
	out, err := cl.MarkForwarded().DiscoverAccounts(runCtx, machine)
	if err != nil {
		if code, ok := client.StatusCode(err); ok && code == http.StatusNotFound {
			s.log.Warn("立即发现：目标机版本过旧", "machine", machine)
			return proto.MachineDiscoverResult{Machine: machine, OK: false,
				Error: "该机 agentd 版本过旧（没有 /api/accounts/discover）"}
		}
		if code, ok := client.StatusCode(err); ok {
			msg, _ := client.ServerErrorText(err)
			s.log.Warn("立即发现：目标机拒绝", "machine", machine, "status", code)
			return proto.MachineDiscoverResult{Machine: machine, OK: false,
				Error: fmt.Sprintf("该机返回 HTTP %d: %s", code, strings.TrimSpace(msg))}
		}
		s.log.Warn("立即发现：目标机不可达", "machine", machine, "cause", err)
		return proto.MachineDiscoverResult{Machine: machine, OK: false, Error: "转发到 " + machine + " 失败: " + err.Error()}
	}
	if out == nil || len(out.Machines) == 0 {
		s.log.Warn("立即发现：目标响应为空", "machine", machine)
		return proto.MachineDiscoverResult{Machine: machine, OK: false, Error: "目标机发现响应为空"}
	}
	res := out.Machines[0]
	if res.Machine == "" {
		res.Machine = machine
	}
	if res.OK {
		s.log.Info("立即发现：远端完成", "machine", machine)
	} else {
		s.log.Warn("立即发现：远端失败", "machine", machine, "cause", res.Error)
	}
	return res
}

// loginSSHLine 是入口机给出的 ssh 一行（plan Q18）：本机与 relay 目标不给；直连目标按设备表
// 的 User@host 换算（与 pull 共用 config.SSHHost）。
func (s *Server) loginSSHLine(machine string) string {
	if machine == "" || s.IsSelfMachine(machine) {
		return ""
	}
	t, ok := s.remoteTarget(machine)
	if !ok {
		return ""
	}
	host := config.SSHHost(t)
	if host == "" {
		return ""
	}
	return "ssh " + host
}

// normalizeLoginHome 校验并规范化检查用的 HOME（A12）：`~` 或 `~/.handoff/home/<dir>`，
// 目录名 [a-z0-9-]；其他路径一律拒绝（防借发现读任意路径）。
func normalizeLoginHome(home string) (string, bool) {
	home = strings.TrimSpace(home)
	if home == accounts.MainHome {
		return home, true
	}
	dir, ok := strings.CutPrefix(home, isolatedHomePrefix)
	if !ok || !loginDirPattern.MatchString(dir) {
		return "", false
	}
	return isolatedHomePrefix + dir, true
}

// readShortBody 读转发请求体（上限与通用转发一致）；超限 413、读失败 400。
func (s *Server) readShortBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, forwardBodyLimit+1))
	if err != nil {
		s.log.Warn("转发请求体读取失败", "path", r.URL.Path, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取请求失败: " + err.Error()})
		return nil, false
	}
	if len(body) > forwardBodyLimit {
		s.log.Warn("转发请求体过大", "path", r.URL.Path, "bytes", len(body), "limit", forwardBodyLimit)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "请求体超过 1 MiB 上限"})
		return nil, false
	}
	return body, true
}
