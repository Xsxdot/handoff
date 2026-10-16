// claudemod.go —— Claude Code 唤醒 mod（plugins/claude-desktop-wake）的安装与状态（B434）。
//
// 职责：
//   - InstallClaudeMod：把内嵌的 mod 文件逐字节原样写到 ~/.handoff/claude-mods/handoff-desktop-wake/，
//     并确保 ~/.claude/settings.json 的 env.CLAUDE_CODE_PLUGIN_DIRS 含该目录（契约 §3.13、条 128–133）；
//   - ClaudeModStatus：逐文件与内嵌原文比 sha256，并核对 env 是否含该目录（条 134–137）。
//
// 边界：
//   - ~/.claude 不存在就跳过，不代建（与 install.go / plugin.go 同一条纪律）；
//   - settings.json 只动 env.CLAUDE_CODE_PLUGIN_DIRS 一个值：其它键保序保值，原有目录项一个不删；
//     解析不了就整次跳过、一个字节都不写（宁可不装，不写坏用户配置）；
//   - 不做安装期替换：mod 自己用 $.env.get("HOME") 找信箱（契约 §10.8 已钉）；
//   - 状态只比内嵌里有的文件：引擎会往加载目录的 .claude-plugin/types/ 写类型声明，那不是漂移。
package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ClaudeModRelDir 是 mod 相对 $HOME 的安装目录（契约 §3.13）。
const ClaudeModRelDir = ".handoff/claude-mods/handoff-desktop-wake"

// claudeModManifest 是 mod 的清单文件；内嵌里缺它说明 embed 丢了点目录，拒绝安装（条 138 的运行时兜底）。
const claudeModManifest = ".claude-plugin/plugin.json"

// claudeHomeRel 是「Claude Code 已安装」的判据目录。
const claudeHomeRel = ".claude"

// claudeSettingsName 是用户级 settings 文件名；CLAUDE_CODE_PLUGIN_DIRS 只认这里或进程环境（契约 §10.6）。
const claudeSettingsName = "settings.json"

// claudePluginDirsEnv 是 Claude Code 读取额外插件目录的环境变量名。
const claudePluginDirsEnv = "CLAUDE_CODE_PLUGIN_DIRS"

// InstallClaudeMod 把内嵌的唤醒 mod 装到本机 Claude Code。
//
// 参数：
//   - mod: 根即插件目录的文件树（生产由 main 的 go:embed 经 fs.Sub 注入；测试给 fstest.MapFS）
//   - home: 用户 home 目录
//
// 返回：一个落点（Path = 安装目录）。任何失败都是 skipped + Note，不返回 error——
// 与 InstallPlugin 一样，一家失败不能吃掉其它落点。
//
// 注意：先把 settings.json 解析并算好新内容，再写 mod 文件，最后写 settings；
// settings 解析失败时 mod 文件也不写，保证「跳过 = 什么都没改」。
func InstallClaudeMod(mod fs.FS, home string) Site {
	log := slog.Default()
	target := filepath.Join(home, ClaudeModRelDir)
	files, err := claudeModFiles(mod)
	if err != nil {
		log.Warn("内嵌 Claude 唤醒 mod 不可用，跳过", "path", target, "cause", err)
		return Site{Path: target, State: StateSkipped, Note: "内嵌 mod 不可用: " + err.Error()}
	}
	claudeDir := filepath.Join(home, claudeHomeRel)
	if _, err := os.Stat(claudeDir); err != nil {
		note := claudeDir + " 不存在（Claude Code 未安装）"
		if !errors.Is(err, os.ErrNotExist) {
			note = "读取 Claude 配置目录失败: " + err.Error()
		}
		log.Warn("Claude 唤醒 mod 落点跳过", "path", target, "reason", note)
		return Site{Path: target, State: StateSkipped, Note: note}
	}
	settingsPath := filepath.Join(claudeDir, claudeSettingsName)
	edit, err := planPluginDirsEdit(settingsPath, target, home)
	if err != nil {
		log.Warn("settings.json 无法合并，Claude 唤醒 mod 整次跳过", "settings", settingsPath, "cause", err)
		return Site{Path: target, State: StateSkipped, Note: "未改动任何文件：" + settingsPath + " 无法合并: " + err.Error()}
	}
	log.Info("写入 Claude 唤醒 mod", "path", target, "files", len(files), "settingsChange", edit.changed)
	for _, rel := range files {
		if err := writeModFile(mod, rel, target); err != nil {
			log.Error("写 Claude 唤醒 mod 文件失败", "path", target, "file", rel, "cause", err)
			return Site{Path: target, State: StateSkipped, Note: "写 mod 文件失败: " + err.Error()}
		}
	}
	if edit.changed {
		if err := writeFileAtomic(edit.path, edit.data, edit.perm); err != nil {
			log.Error("写 settings.json 失败", "settings", edit.path, "cause", err)
			return Site{Path: target, State: StateSkipped, Note: "mod 已写入，但写 settings.json 失败（Claude 不会加载它）: " + err.Error()}
		}
		log.Info("已把 Claude 唤醒 mod 目录加入 CLAUDE_CODE_PLUGIN_DIRS", "settings", edit.path, "dir", target)
	}
	log.Info("Claude 唤醒 mod 已写入", "path", target)
	return Site{Path: target, State: StateInstalled}
}

// ClaudeModStatus 报告唤醒 mod 的安装一致性。
//
// 判定顺序（契约条 134–137）：~/.claude 不存在 → missing；任一内嵌文件在安装目录缺失 → missing；
// 任一文件哈希不一致 → stale；文件全对但 env 不含安装目录 → stale（Note 说明，InSync 会提示重装）；
// 都对 → in_sync。安装目录里多出的文件（引擎写的 .claude-plugin/types/）不参与比对。
func ClaudeModStatus(mod fs.FS, home string) Site {
	target := filepath.Join(home, ClaudeModRelDir)
	claudeDir := filepath.Join(home, claudeHomeRel)
	if _, err := os.Stat(claudeDir); err != nil {
		note := claudeDir + " 不存在（Claude Code 未安装）"
		if !errors.Is(err, os.ErrNotExist) {
			note = "读取 Claude 配置目录失败: " + err.Error()
		}
		return Site{Path: target, State: StateMissing, Note: note}
	}
	files, err := claudeModFiles(mod)
	if err != nil {
		return Site{Path: target, State: StateSkipped, Note: "内嵌 mod 不可用，跳过比对: " + err.Error()}
	}
	var missing, stale []string
	for _, rel := range files {
		want, err := fs.ReadFile(mod, rel)
		if err != nil {
			return Site{Path: target, State: StateSkipped, Note: "读内嵌 mod 失败: " + err.Error()}
		}
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			missing = append(missing, rel)
			continue
		}
		if sha256.Sum256(got) != sha256.Sum256(want) {
			stale = append(stale, rel)
		}
	}
	if len(missing) > 0 {
		return Site{Path: target, State: StateMissing, Note: "缺文件: " + strings.Join(missing, ", ")}
	}
	if len(stale) > 0 {
		return Site{Path: target, State: StateStale, Note: "与当前二进制不一致: " + strings.Join(stale, ", ")}
	}
	settingsPath := filepath.Join(claudeDir, claudeSettingsName)
	if ok, why := settingsHasPluginDir(settingsPath, target, home); !ok {
		return Site{Path: target, State: StateStale, Note: why}
	}
	return Site{Path: target, State: StateInSync}
}

// claudeModFiles 列出内嵌 mod 的全部普通文件（斜杠分隔的相对路径，升序）。
// mod 为 nil、为空或缺清单都返回错误：这三种都意味着构建漏了 embed，装出去的东西加载不了。
func claudeModFiles(mod fs.FS) ([]string, error) {
	if mod == nil {
		return nil, errors.New("未注入（main 漏了 SetClaudeModFS）")
	}
	var files []string
	err := fs.WalkDir(mod, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("内嵌目录为空")
	}
	if _, err := fs.Stat(mod, claudeModManifest); err != nil {
		return nil, fmt.Errorf("缺 %s（embed 丢了点目录？）", claudeModManifest)
	}
	sort.Strings(files)
	return files, nil
}

// writeModFile 把内嵌文件 rel 原样写到 target 下同一相对路径。
func writeModFile(mod fs.FS, rel, target string) error {
	b, err := fs.ReadFile(mod, rel)
	if err != nil {
		return err
	}
	dst := filepath.Join(target, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// pluginDirsEdit 是对 settings.json 的一次待写改动；changed=false 时什么都不写（幂等）。
type pluginDirsEdit struct {
	path    string      // 实际要写的文件（settings.json 是符号链接时为链接目标）
	data    []byte      // 新内容
	perm    fs.FileMode // 沿用原文件权限；新建为 0o644
	changed bool
}

// planPluginDirsEdit 读 settings.json，算出把 dir 追加进 env.CLAUDE_CODE_PLUGIN_DIRS 后的新内容。
//
// 规则（契约 §3.13、条 129–131）：按 filepath.SplitList 拆分；已含 dir（含 ~ 写法）就不改；
// 否则在原串末尾以 filepath.ListSeparator 追加，原串一个字节不动；其它键与 env 里其它变量保序保值。
// 文件不存在视为空对象；不是 JSON 对象、env 不是对象、变量不是字符串都返回错误（调用方整次跳过）。
func planPluginDirsEdit(settingsPath, dir, home string) (pluginDirsEdit, error) {
	edit := pluginDirsEdit{path: settingsPath, perm: 0o644}
	if real, err := filepath.EvalSymlinks(settingsPath); err == nil {
		// 用户常把 settings.json 链到 dotfiles 仓库；rename 到链接本身会把链接换成普通文件
		edit.path = real
	}
	var root []jsonMember
	b, err := os.ReadFile(edit.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return edit, err
	default:
		if st, err := os.Stat(edit.path); err == nil {
			edit.perm = st.Mode().Perm()
		}
		if len(bytes.TrimSpace(b)) == 0 {
			break // 空文件按空对象处理
		}
		if root, err = decodeJSONObject(b); err != nil {
			return edit, fmt.Errorf("不是合法的 JSON 对象: %w", err)
		}
	}
	envIdx, err := uniqueMember(root, "env")
	if err != nil {
		return edit, err
	}
	var env []jsonMember
	if envIdx >= 0 {
		if env, err = decodeJSONObject(root[envIdx].Value); err != nil {
			return edit, fmt.Errorf("env 不是 JSON 对象: %w", err)
		}
	}
	varIdx, err := uniqueMember(env, claudePluginDirsEnv)
	if err != nil {
		return edit, fmt.Errorf("env.%w", err)
	}
	next := dir
	if varIdx >= 0 {
		var cur string
		if err := json.Unmarshal(env[varIdx].Value, &cur); err != nil {
			return edit, fmt.Errorf("env.%s 不是字符串: %w", claudePluginDirsEnv, err)
		}
		if pluginDirsContain(cur, dir, home) {
			return edit, nil
		}
		if cur != "" {
			next = cur + string(filepath.ListSeparator) + dir
		}
	}
	val, err := marshalNoEscape(next)
	if err != nil {
		return edit, err
	}
	if varIdx >= 0 {
		env[varIdx].Value = val
	} else {
		env = append(env, jsonMember{Key: claudePluginDirsEnv, Value: val})
	}
	envRaw, err := encodeJSONObject(env)
	if err != nil {
		return edit, err
	}
	if envIdx >= 0 {
		root[envIdx].Value = envRaw
	} else {
		root = append(root, jsonMember{Key: "env", Value: envRaw})
	}
	out, err := encodeJSONObject(root)
	if err != nil {
		return edit, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out, "", "  "); err != nil {
		return edit, err
	}
	pretty.WriteByte('\n')
	edit.data = pretty.Bytes()
	edit.changed = true
	return edit, nil
}

// settingsHasPluginDir 判断 settings.json 的 env.CLAUDE_CODE_PLUGIN_DIRS 是否含 dir；不含时给出可行动的理由。
func settingsHasPluginDir(settingsPath, dir, home string) (bool, string) {
	why := settingsPath + " 的 env." + claudePluginDirsEnv + " 不含 " + dir + "（Claude 不会加载 mod）"
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return false, why + ": " + err.Error()
	}
	root, err := decodeJSONObject(b)
	if err != nil {
		return false, settingsPath + " 不是合法的 JSON 对象: " + err.Error()
	}
	i, err := uniqueMember(root, "env")
	if err != nil || i < 0 {
		return false, why
	}
	env, err := decodeJSONObject(root[i].Value)
	if err != nil {
		return false, why
	}
	j, err := uniqueMember(env, claudePluginDirsEnv)
	if err != nil || j < 0 {
		return false, why
	}
	var cur string
	if err := json.Unmarshal(env[j].Value, &cur); err != nil || !pluginDirsContain(cur, dir, home) {
		return false, why
	}
	return true, ""
}

// pluginDirsContain 判断路径列表串 list 是否已含 dir；`~/…` 按 home 展开后比较（Claude Code 允许 ~，契约 §10.6）。
func pluginDirsContain(list, dir, home string) bool {
	want := filepath.Clean(dir)
	for _, p := range filepath.SplitList(list) {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if p != "" && filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

// jsonMember 是 JSON 对象的一个成员；用切片而不是 map 保住键序。
type jsonMember struct {
	Key   string
	Value json.RawMessage
}

// decodeJSONObject 按出现顺序解出一个 JSON 对象的成员，值保持原始字节。
func decodeJSONObject(b []byte) ([]jsonMember, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("顶层不是对象")
	}
	members := []jsonMember{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("键不是字符串: %v", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		members = append(members, jsonMember{Key: key, Value: raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("对象之后还有多余内容")
	}
	return members, nil
}

// encodeJSONObject 按切片顺序编码成员（紧凑形式；调用方统一缩进）。
func encodeJSONObject(members []jsonMember) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := marshalNoEscape(m.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(m.Value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// uniqueMember 返回键 key 的下标（没有为 -1）；同名键出现多次时拒绝——改哪一个都是猜。
func uniqueMember(members []jsonMember, key string) (int, error) {
	idx := -1
	for i, m := range members {
		if m.Key != key {
			continue
		}
		if idx >= 0 {
			return -1, fmt.Errorf("%s 键重复出现", key)
		}
		idx = i
	}
	return idx, nil
}

// marshalNoEscape 编码一个字符串且不把 < > & 转成 \u003c 之类（路径里出现时保持可读）。
func marshalNoEscape(s string) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// writeFileAtomic 以同目录临时文件 + rename 写入，避免写到一半时 Claude 读到半个 settings.json。
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".handoff-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // rename 成功后是空操作
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
