# B369 contract 台账(移动端 App · 契约落地与冻结)

节点:contract(上游 spec `docs/superpowers/specs/2026-09-13-mobile-app-design.md`,状态行已回写「已批准」——contract 开工核对通过)。
卡:B369(contract 列,spec 附件已挂,base_branch=cards/B233.1-charter-7)。接替 B368——首派未设基线,冻结在 main 起点(缺 charter-7 线与 spec),机制内无解(SetCardBaseBranch 首派后冻结),已取消并留说明于其事件流。

## 工具链 gate:gomobile spike(spec 测试决定:contract 启动前出结果)

环境读数(2026-09-14 本机实测):

- go 1.26.1 darwin/arm64;Xcode 26.6 (Build 17F113);gomobile v0.0.0-20260908204917-8b95e45f8d3e(`go install golang.org/x/mobile/cmd/gomobile@latest`)。
- Android SDK 在 `~/Library/Android/sdk`(cmdline-tools/latest 可用),NDK 初始缺失 → sdkmanager 装 `ndk;30.0.16248370`(稳定版)。

spike 形态:临时包 `mobile-spike/`(gate 后删除,不入库)——module `github.com/Xsxdot/handoff/mobilespike` + `replace github.com/Xsxdot/handoff => ../`(desktop/ 先例,internal 可见性靠同模块路径前缀);`bind/bind.go` 导入 `internal/relay` + `internal/client`,导出 `SpikeTouch` 占位。

过程与结果:

1. 宿主闭包编译:`go build ./bind` → BUILD_OK(go 1.26.1 下 relay/client 传递闭包可编译)。
2. 首发失败(程序性,非不兼容):新版 gomobile 要求 `golang.org/x/mobile` 在本模块依赖图内——`go get -tool golang.org/x/mobile/cmd/gobind`(go 1.24+ tool 指令)后过。此条写进交棒:mobile/ 模块的 go.mod 必须带 tool 指令。
3. **iOS:PASS**。`gomobile bind -target=ios -o spike.xcframework ./bind` → 产出 `spike.xcframework`(ios-arm64 真机 + ios-arm64_x86_64-simulator 两 slice);`file` = Mach-O arm64 current ar archive(9.6MB);`nm -gU` 见导出符号 `_BindSpikeTouch`/`_proxybind__SpikeTouch`;Headers 含 Bind.objc.h/Spike.h/Universe.objc.h/ref.h。
4. **Android:PASS**。`ANDROID_NDK_HOME=~/Library/Android/sdk/ndk/30.0.16248370 gomobile bind -target=android -androidapi 21 -o spike.aar ./bind` → 产出 `spike.aar`(12MB):`classes.jar`(9.9KB,含 Bind 类)+ `jni/{armeabi-v7a,arm64-v8a,x86,x86_64}/libgojni.so` 四 ABI slice + AndroidManifest.xml/proguard.txt。
   - 过程坑(写进交棒):NDK 30 移除了 API<21 的 platform,而 gomobile 缺省 minsdk=16 → 首发报 `unsupported API version 16 (not in 21..37)`;加 `-androidapi 21` 即过。mobile/ 正式模块的构建脚本/文档必须显式带 `-androidapi 21`(或将 minSdk 定为 21)。

gate 结论:**双端 PASS**——go 1.26.1 + gomobile@2026-09-08 + Xcode 26.6 + NDK 30.0.16248370(androidapi 21)工具链可用,relay/client 闭包可绑。spike 产物与 `mobile-spike/` 目录已按预定删除,不入库。
