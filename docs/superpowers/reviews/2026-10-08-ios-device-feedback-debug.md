# iOS真机三屏缺陷

对象：用户确认安卓正常，iOS最后安装的设备版 `dev.handoff.HandoffMobile`，iPhone15 Pro。根工作树 `codex/mobile-entry-settings` / HEAD `3e52eb462b7e0213db4c4b8988db91dd3083e84c`，未提交的批准实现。原始反馈图按文件复制在 `docs/design/mobile-entry/verification/ios-device-issues/`，没有修改像素。

## 红回路与假设

- 选机屏：假设①Go未返回机器、②表格AutoLayout纵向自由度导致无高度、③系统深色让行文字不可见。真实window+生产VC+三机器回归显示 `table.height=0`、visibleRows仅1、dark trait，断言红；模型清单正确，②确证，③解释与浅色稿不一致。修复前模型测试绿不证明布局。
- 启动屏：假设①品牌bitmap像素尺寸错误、②无约束的UILaunchScreen UIImageName系统拉伸、③安装旧快照缓存。最终安装产物Info.plist是②形态，品牌资源被欢迎页正确缩放；改为系统原生LaunchScreen storyboard中居中64×64约束，保留同一logo bitmap。旧系统快照在真机覆盖安装后仍须观察，不把编译当渲染通过。
- 工作台：假设①手机壳反代错误目标、②目标agentd未embedweb、③WKWebView加载了缓存旧页。iPhone仅读取app-local当前机偏好为mac-02；该目标status `web_embedded=false`，独立assert true出现AssertionError，stub.go内容逐字匹配截图。②确证；不在手机添加自有HTTP/前端兜底。

## 分流与范围

已有协议/权威/职责不变，窄布局与组装可测缝修复即可。原gpt-6-luna worker完成初步修复后额度中断；10月9日协调者接手iOS源码、独立验证/安装与mac-02服务修复，安卓不改。测试构造也必须走完整AppComposition，禁止composition=nil的UI行为分叉。

用户明确“superdev对此不适用，不用管”，采用现有launchd/CLI服务生命周期；不重复索要绕过授权。只更新mac-02，原远端source repo保持不动、已有.prev不覆盖；旧二进制独立备份。

## 服务修复结果

`scripts/build-deploy.sh darwin/arm64` 全链构建+embedweb自检通过。传输后sha256一致（`09c68c6d7ab091457a5fcd51f4299f894382d1ff4f14286a61eb351be7da60af`）。重启前任务running/pending/waiting_answer/waiting_review均0；CLI stop/start记录2个遗留PTY会话收口，恢复launchd自动拉起。

实测新服务reachable=true/degraded=false/web_embedded=true，revision=当前设计树、version=3e52eb462b7e+dirty41。使用真实console票据/cookie的HTTP200页面title正常、stub不存在、实际脚本含新版native切机链接。凭据未输出。手机全链仍需在修复壳重装后验证。

## 尚待收尾

iOS完整依赖注入修正、Launch storyboard编译已完成；10月9日12:06独立全套48测试全部通过且无跳过，Keychain测试保持原断言。真实UIKit layer渲染附件已与批准稿、用户缺陷截图拼入同一比较输入，分别标注组件证据与用户真机证据。

同输入对照发现正常屏三台机器的列表被底部等式拉伸成大块空白（P2）。新增收紧断言真实红（505pt>217pt）；内容高度作为优先有限高度、底部空间作为上限，三机器卡片收紧、多机器可滚动到末行。4项布局测试与最终49项全套全部通过、0跳过；第二轮同输入截图无该空白卡片问题。不把“列表重新可见”外推成设计完整通过。

12:07已签名真机编译成功，但install返回CoreDeviceError1011：iPhone15 Pro当前unavailable。已请用户解锁并重新连接，尚未装上本轮修复版；原10月8日安装的数据不清理。重新安装后仍需手机工作台/设置切机故事与实际系统启动画面验收。

12:13稳定最终源码重新签名编译成功，设备短暂恢复available后install在开发者服务阶段立即断开（CoreDeviceError4000），再试1011。最新设备状态unavailable；最终修复包尚未安装。签名与LaunchScreen编译资源独立验证通过，待稳定连接后安装同一bundle，不重建或清理用户数据。

14:18用户重新连接后，最终修复版覆盖安装与启动均exit0，devicectl JSON均outcome=success、安装bundleID一致。原有数据/配对未清理；设备连接与安装阻碍解除，完整页面/交互故事仍待实际操作证据。
