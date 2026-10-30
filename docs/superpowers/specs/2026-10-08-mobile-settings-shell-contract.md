# 移动设置与原生壳的窄接缝 v1

状态：按已批准的设置更换机器故事冻结（2026-10-08）；消费者为现有 Web SettingsHub 和 Android/iOS WebView 导航器。零改变 mobile/bind 与配对 wire。

## 原生到页面：非敏感显示上下文

壳在当前回环 origin 的主文档完成导航后设置 `document.documentElement.dataset.handoffNativeShell = "1"` 及 `dataset.handoffNativeMachine = <当前机器名>`，然后向 window 派发普通事件 `handoff-native-context`。设置值必须通过 JSON 编码正确转义，不能插入未转义用户文本。Web 初次读取属性，并监听该事件刷新展示；属性缺失时不展示原生机器区或更换动作。

这两个字符串只表示当前壳能力与非敏感显示名，不接受来自 Web 的写值作为 native 状态来源；不放 token/cookie/bundle/origin 参数，不注入可调用的原生对象或函数，不添加 addJavascriptInterface / WKScriptMessageHandler。只对已进入机器的精确 origin 主文档提供上下文，外部页面不获得注入。壳原生偏好是唯一当前机器权威。

## 页面到原生：标准导航

设置里的更换机器使用普通 `<a href="/_handoff/native/switch-machine">` 导航，不是 SPA router 子路由，不传目标机/任意 URL/命令参数。壳仅在当前绑定返回的精确 origin（scheme + host + port）下，主框导航、原始路径精确等于上述常量、无 query/fragment/编码歧义时取消 Web 加载并打开原生选择器；其他路径不得触发原生动作。

更换动作只打开 UI，由用户在 native 列表选择；Web 不可指定切到哪台。导航动作不依赖 generic JS bridge。两端 API 分支必须同样拦截（Android 21–23 String 与 24+ request，iOS navigationAction）。无 native 能力的普通浏览器不提供该动作。

Android 21–23 的 String 回调不提供 frame 元数据，不能默认当成主框；旧分支在 `onPageStarted` 的主框回调确认 URL 后停止加载并开选择器，24+ 在 request 分支检查 isForMainFrame。iOS 检查 navigationAction 的 targetFrame.isMainFrame。见 [Android 官方 WebViewClient 文档](https://developer.android.com/reference/android/webkit/WebViewClient)；不得让 helper 中手工传入的 true 冒充平台提供的 frame 证据。

## 成功与失败

选择器展示已保存当前机的选中状态；离线可见但不可进入。切机继续走既有 binder/CookieBridge 的七函数绑定和清罐顺序。只有认证主框完成有效加载才保存新选择并发布相应上下文；取消、离线、兑换错误、401/403、主框失败均不得覆盖原选择。启动恢复只尝试保存的机器，失败显示重试/更换，不自动挑其他在线机。

401沿既有契约归为会话过期；403和其他非成功主文档归为加载失败，不扩大既有过期分类。主动更换产生的明确导航取消不显示网络失败。

## 手动重试与平台观测边界

核的online为Pair登记结果，普通读取列表或Activate不会让离线快照重新上线。用户在当前机器不可用页主动重试时，壳读取原安全存储中的bundle并调用既有Pair重新校验，再只定位保存的机器A。校验失败、A仍离线或A不存在时保留A与不可用屏并显示明确反馈；其他机器在线不触发自动进入。该动作不重写bundle、不清当前选择、不另建HTTP客户端，不增加自动轮询；自动启动只Pair一次的约束不变。

Android最低版本暂沿既有API21保持不变，用户尚未决定提高支持下限。API21–22的WebViewClient没有HTTP错误状态回调，无法仅靠既有导航回调可靠识别服务器错误文档；当前成功加载门控在这些版本存在已知可观测限制，不能声称满足与API23+相同的状态保证。API23+主框HTTP错误与连接失败均不得落选择；本轮运行证据是API35模拟器，不是API21–22或真机验收。状态回调版本依据[Android WebViewClient文档](https://developer.android.com/reference/android/webkit/WebViewClient#onReceivedHttpError(android.webkit.WebView,%20android.webkit.WebResourceRequest,%20android.webkit.WebResourceResponse))。
