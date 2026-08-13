// handoff 安装入口：把 handoff.gosuper.dev 的 /install 与 /install.ps1 分别 302 到仓库里的 install.sh 与 install.ps1。
//
// 职责：
//   - 只做重定向，**不托管脚本内容**：脚本的唯一权威是仓库里的 install.sh，
//     这样改脚本不需要动这个 Worker，也不会出现两份内容不一致
//
// 边界：
//   - 不做版本协商、不改写脚本、不注入参数——install.sh 自己按平台挑资产
//   - 除 /install、/install.ps1 与 / 之外一律 404：这个域名只有这一个用途，
//     悄悄把别的路径也重定向到脚本会让人拿到意料之外的东西
const TARGET_SH = "https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh";
const TARGET_PS1 = "https://raw.githubusercontent.com/Xsxdot/handoff/main/install.ps1";

export default {
  fetch(request) {
    const { pathname } = new URL(request.url);
    // 302 而不是 301：301 会被浏览器与部分代理长期缓存，
    // 将来想换目标（比如改成打 tag 的固定版本）就撤不回来了
    if (pathname === "/install" || pathname === "/install/") {
      return Response.redirect(TARGET_SH, 302);
    }
    // Windows 入口。两条路径分开而不是按 User-Agent 猜：
    // 用户敲的命令本身已经说清了要哪一个，猜只会在 WSL / Git Bash 上出错
    if (pathname === "/install.ps1" || pathname === "/install.ps1/") {
      return Response.redirect(TARGET_PS1, 302);
    }
    if (pathname === "/") {
      return Response.redirect("https://github.com/Xsxdot/handoff", 302);
    }
    return new Response("not found\n", { status: 404 });
  },
};
