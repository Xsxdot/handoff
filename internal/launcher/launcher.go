// Package launcher 管理一台机器上的「工作台自定义启动项」。
//
// 职责：
//   - 启动项列表的落盘与读取（<DataDir>/launchers.json）
//   - 列表校验：名字非空且唯一、env 与命令至少填一个、文件名形状合法
//
// 边界：
//   - **不解析 env 文件、不起终端**：前者归 internal/envfile，后者归 ptyhost
//   - 不做 HTTP 编解码，不认识 machine 参数（跨机由 agentd 的转发层处理）
//   - 不打命令原文：Command 可能含凭据（`API_KEY=xxx some-cmd` 是常见写法），
//     日志只记条数与是否带命令
//
// 为什么单独一个文件而不是落进 config.yaml：config 以 KnownFields(true) 严格
// 解析，新键会让**旧版 agentd 读到新版写的配置直接启动失败**——这是 PathDirs
// 与 Proxy 的注释里逐字记着的坑。启动项是一张会长的列表，单独一个文件没有
// 这个换版风险。
package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName 是启动项配置在 DataDir 下的固定文件名。
const FileName = "launchers.json"

// ErrInvalid 是校验失败的哨兵；错误文本是中文原文，可直接作为 400 的响应体。
var ErrInvalid = errors.New("启动项配置不合法")

// Dir 返回启动项配置文件的绝对路径。
//
// 路径知识只此一处：agentd 与将来的任何读取方都调它，各拼各的路径必然漏改。
func Dir(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Item 是一条启动项的落盘形态。
//
// 刻意与 proto.Launcher **分开**：proto 那个带 EnvMissing（一个每次读盘现算的
// 派生字段），落盘一份派生值就会有两个真相——文件里说 false、磁盘上文件早没了。
type Item struct {
	Name    string `json:"name"`
	EnvFile string `json:"env_file,omitempty"`
	Command string `json:"command,omitempty"`
}

// Load 读该机的启动项列表。
//
// 参数：dataDir 为 agentd 的数据目录。
// 返回：
//   - 文件不存在时返回 (nil, nil)——那是**正常起点**，不是错误
//   - 文件存在但内容坏了时返回错误，错误文本带完整路径
//
// 注意：Load **不做校验**。磁盘上可能存着上一版写下的、按今天规则不合法的
// 数据；把它读出来交给调用方展示，好过让整个配置面因为一条坏数据打不开。
func Load(dataDir string) ([]Item, error) {
	path := Dir(dataDir)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取启动项配置 %s: %w", path, err)
	}
	var list []Item
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("解析启动项配置 %s: %w", path, err)
	}
	return list, nil
}

// Save 整段替换该机的启动项列表。
//
// 参数：dataDir 为数据目录；list 为完整列表（空列表 = 一条都没有，合法）。
// 返回：校验失败或落盘失败的错误。
//
// 注意：
//   - **先校验后落盘**——写坏的配置不该进磁盘（与 envfile.Write 的「调用方须
//     先跑 Parse」同源纪律，只是这里把校验收进来了，因为它不依赖任何外部状态）
//   - 权限 0600 / 目录 0700，与 envfile 一致：启动项本身不含凭据，但它指名了
//     哪份 env 文件，权限基线不该松于同目录其余内容
func Save(dataDir string, list []Item) error {
	if err := Validate(list); err != nil {
		return err
	}
	if list == nil {
		list = []Item{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化启动项配置: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", dataDir, err)
	}
	path := Dir(dataDir)
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("写入启动项配置 %s: %w", path, err)
	}
	return nil
}

// Validate 校验一份启动项列表。
//
// 参数：list 为完整列表（nil 或空列表合法）。
// 返回：第一条不合法项的错误（包 ErrInvalid），文本可直接作为 400 响应体。
//
// 四条规则：
//  1. 名字非空——它是启动项的身份
//  2. 名字机器内唯一——身份不能重复
//  3. EnvFile 与 Command 至少一个非空——两者都空与「新终端」完全等价，
//     存在本身就是一次误操作
//  4. EnvFile 不含路径分隔符——与 envfile 的纯文件名约束同款，杜绝路径穿越，
//     并保证 env 文件只有一个家
//
// **不校验 EnvFile 是否真的存在**：那需要读盘，属于调用方（agentd）在保存时
// 做的一次性校验；本函数是纯函数，可以在任何地方穷举测试。
func Validate(list []Item) error {
	seen := make(map[string]bool, len(list))
	for i, it := range list {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			return fmt.Errorf("%w: 第 %d 条启动项的名字不能为空", ErrInvalid, i+1)
		}
		if seen[name] {
			return fmt.Errorf("%w: 启动项名字 %q 重复，名字在同一台机器内必须唯一", ErrInvalid, name)
		}
		seen[name] = true
		if strings.TrimSpace(it.EnvFile) == "" && strings.TrimSpace(it.Command) == "" {
			return fmt.Errorf("%w: 启动项 %q 的 Env 文件与执行命令至少填一个", ErrInvalid, name)
		}
		if f := it.EnvFile; f != "" {
			if strings.ContainsRune(f, filepath.Separator) || strings.ContainsRune(f, '/') {
				return fmt.Errorf("%w: 启动项 %q 的 Env 文件名 %q 不能含路径分隔符：只支持 env 目录下的纯文件名",
					ErrInvalid, name, f)
			}
		}
	}
	return nil
}
