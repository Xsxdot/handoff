# handoff.gosuper.dev 安装入口重定向

一行安装那两句背后就是这个 Cloudflare Worker：

- `curl -fsSL https://handoff.gosuper.dev/install | bash` → 302 到 `install.sh`
- `irm https://handoff.gosuper.dev/install.ps1 | iex` → 302 到 `install.ps1`

两条路径分开而不是按 User-Agent 猜：用户敲的命令本身已经说清了要哪一个，猜只会在
WSL / Git Bash 上挑错。

## 为什么不直接让用户 curl raw.githubusercontent.com

- 那个地址又长又像是临时的，抄进文档和聊天记录里没人信得过；
- 将来要换分发方式（换 CDN、改成指向某个 tag 的固定版本、加地区分流），
  只需要改这个 Worker，已经流传出去的那行命令不用动。

**脚本内容不在这里。** 唯一权威是仓库根目录的 `install.sh` 与 `install.ps1`——改脚本
推 main 即可生效，不需要重新部署 Worker。

## 部署

```bash
cd deploy/install-redirect
npx wrangler deploy
```

首次部署时 `wrangler.toml` 里的 `custom_domain = true` 会让 Cloudflare 自动创建
`handoff.gosuper.dev` 的 DNS 记录并签发证书。

选 Custom Domain 而不是普通 route，是因为普通 route 要求先存在一条指向该主机名的
DNS 记录；而 OAuth 登录拿到的令牌通常只有 `zone (read)`，没有 DNS 写权限，自己补
不了那条记录。Custom Domain 由 Cloudflare 代建，绕开了这个权限缺口。

刚部署完的头一分钟可能返回 500——证书还在签发，等一会儿自己就好。

## 验证

```bash
curl -sI https://handoff.gosuper.dev/install | head -3
curl -sI https://handoff.gosuper.dev/install.ps1 | head -3
```

期望：都是 `HTTP/2 302`，`location` 分别指向 main 分支上的 `install.sh` 与 `install.ps1`。

其余路径（除 `/` 外）应当是 404——这个域名只有安装这一个用途，把别的路径也重定向
到脚本会让人拿到意料之外的东西。
