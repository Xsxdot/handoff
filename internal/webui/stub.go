//go:build !embedweb

package webui

import (
	"io/fs"
	"testing/fstest"
)

// stubIndex 是未嵌入前端时伺服的说明页。
//
// 它必须**诚实**：既不能是空白页（用户会以为服务坏了），也不能假装成正常
// 控制台（用户会以为前端有 bug）。把真实原因和两条出路直接写在页面上，
// 是这里唯一不会把人引向错误排查方向的做法。
const stubIndex = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>handoff：前端未嵌入</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.7">
<h1>此二进制未嵌入前端构建产物</h1>
<p>agentd 本身工作正常，只是这份二进制是用默认标签构建的，不含 Web 控制台。</p>
<h2>两条出路</h2>
<ul>
<li>用 Release 版二进制（release 流水线以 <code>-tags embedweb</code> 构建，含前端）。</li>
<li>开发时在 <code>web/</code> 下跑 <code>npm run dev</code>，用 Vite dev server 访问控制台。</li>
</ul>
</body></html>
`

// stubFS 只含一个 index.html。用 fstest.MapFS 而不是自己实现 fs.FS：
// 它是标准库里现成的只读内存实现，语义与 embed.FS 一致。
var stubFS fs.FS = fstest.MapFS{
	"index.html": &fstest.MapFile{Data: []byte(stubIndex)},
}

// FS 返回控制台静态资源的根文件系统。永不返回 nil。
//
// 默认构建下返回只含一页说明的 stub，见 stubIndex。
func FS() fs.FS { return stubFS }

// Embedded 报告当前二进制是否嵌入了真实的前端构建产物。
//
// 默认构建下恒为 false。调用方（如 agentd 启动日志）应据此告诉运维
// 「这份二进制有没有前端」——否则前端打不开时无从判断是构建问题还是运行问题。
func Embedded() bool { return false }
