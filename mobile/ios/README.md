# mobile/ios —— handoff iOS 原生壳（B417.2）

薄壳：WKWebView 容器 + 配对入口（粘贴/扫码）+ 原始 bundle 入 Keychain + webview cookie 桥接。
零协议逻辑，全部经 `Handoff-Mobile.framework` 的七个 `Bind*` C 函数。

## 构建（先核心，后壳；无 GUI）

    export PATH="$HOME/go/bin:$PATH"
    ./mobile/build.sh ios
    cd mobile/ios
    xcodebuild -project HandoffMobile.xcodeproj -scheme HandoffMobile \
      -sdk iphonesimulator -configuration Debug -derivedDataPath build \
      CODE_SIGNING_ALLOWED=NO build

产物：`build/Build/Products/Debug-iphonesimulator/HandoffMobile.app`（内含 `Frameworks/Handoff-Mobile.framework`）。

## 测试

    xcodebuild -project HandoffMobile.xcodeproj -scheme HandoffMobile \
      -destination 'platform=iOS Simulator,name=iPhone 17' \
      -derivedDataPath build CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- test

## 工程生成说明
仓内无 XcodeGen/Tuist；`.xcodeproj/project.pbxproj` 为**手写并提交**（objectVersion 77，
同步文件夹 `PBXFileSystemSynchronizedRootGroup`），故在本目录新增 `.swift` 无需改工程文件。
`Support/Info.plist` 必须在同步文件夹之外，否则与 `INFOPLIST_FILE` 冲突。

## 关键约束
- Swift 经 **bridging header** 消费框架（模块名 `Handoff-Mobile` 含连字符，无法 `import`）。
- 必须 `SWIFT_ENABLE_EXPLICIT_MODULES = NO`。
- 注入 cookie **不传 `.secure` 键**（传任何值都会变 Secure）；HttpOnly 客户端不可设。
- ATS 只放行 `NSAllowsLocalNetworking`（明文回环），绝不 `NSAllowsArbitraryLoads`。
