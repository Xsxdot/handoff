# 设置首页一致性修复

对象：882f工作树、codex/mobile-entry-settings、HEAD 3e52eb462b7e及用户已批准的未提交移动入口/设置实现。用户新提供iOS截图显示当前机器mac-02；现网原版3e52eb462b7e+dirty41。安卓与iOS壳源码本轮不修改。

## 截图审查与根因

1. 设置首页（修复前异常）：用户原图、393×748实际IAB页面均出现带副标题的行拥挤、单行入口只有24px、当前机器卡72px，视觉与触区层级失衡。真实getBoundingClientRect读取machines/work/display为40/39/38px，discipline/automation/env/update/about均24px；实际按钮断言低于44px，红回路明确失败。
2. 设置子页入口（行为正常）：工作方式、显示与可访问性按钮分别进入对应生产组件，原有设置内容仍在，未写偏好或机器选择。修复后截图保存在work-after.png/display-after.png。这不证明所有二级页设计已与生成稿逐像素一致。

假设：①iOS字体放大或WebView缩放、②生成CSS缺失任意尺寸、③共享触点基线选择器压过行尺寸、④图标与操作色沿用桌面主题。IAB无需iOS字体设置便复现，显式CSS min-height读为24px；TOUCH_BASELINE后代规则特异度高且只排除min-h-11，直接覆盖min-h-[68px]/[72px]。③是纵向压缩根因，④解释图标不统一与黑色操作/选中态。

## 修复与边界

- 把24px触点基线移至CSS components层，明确组件尺寸utilities优先；移除仅排除44px的类名特判。24px兜底继续存在，更高触区不再被覆盖。
- 设置首页单行52px、双行64px起（含内容/边框实际64–66px），内边距、说明间距、20px图标统一；顶部增加16px留白，标题24px。移动设置操作与移动底栏选中态共用靛蓝token，桌面设置主题/字号保持原分支。
- 没有修改路由、偏好键、原生机器选择权威或配对协议。既有设置组件继续复用，旧测试中对基线类字符串的断言更新为新的marker；不以这些结构断言代替本轮浏览器真实高度断言。
- 仅CSS/布局的改动以意图注释和运行CSS证据说明，不添加无意义的用户交互日志。

## 验证

- 根因红与最终绿的实际DOM尺寸见verification/settings-consistency/row-metrics-{red,green}.json。最终8行均≥52px、图标20px、返回按钮44px；更换链接和选中设置tab同为rgb(79,70,237)。
- 6个触及测试文件270测试通过；全Web 143文件/1777测试通过，无新增跳过。tsc+Vite构建及embedweb自检通过。git diff --check通过。
- 同输入对照settings-comparison.png包含批准稿、用户原图、修复前393×748与修复后393×900完整总览；另有同748视口browser-after.png。高度变化为完整列表总览，不改变宽度/字体缩放，不把浏览器栏/系统安全区差异当成产品缺陷。首轮CSS压缩/颜色问题已消除；机器名称及在线状态来源不同，未伪造原生桥没有提供的在线状态。

## 实际发布与限制

只更新用户截图所连mac-02。部署前再次确认4种活跃任务计数均0，SSH验证已有launchd与二进制身份，远端源码树不改，既有.prev不覆盖；旧版另存/Users/sycm/.local/bin/handoff.settings-style-prev-20261009-1431。

上传与安装二进制sha256一致：457a21720fc2daabc324d84953f905258afa1b85f1b4e8da36f0a3d06f995f40。CLI service stop/start/status完成，新版3e52eb462b7e+dirty48，reachable=true/degraded=false/web_embedded=true。真实console票据/cookie请求HTTP200；所返回CSS/JS哈希与本次构建完全一致，凭据未输出。

iPhone在线但锁屏；尝试重开App被系统Locked拒绝，没有绕过锁屏或声称启动成功。用户解锁后重开App加载新版Web，无需安装新壳。完整真机故事与全套设置设计QA仍保持未验收，不把本页浏览器修复与部署当成完整故事通过。

## 图覆盖债

codegraph sym SettingsHub、TOUCH_BASELINE均未命中，按规则回落源码定位；本轮前端符号尚无图覆盖。共享触点基线消费者覆盖CardsPage/CardDrawer/FileTree/SessionTab/项目页/SettingsHub，已完成Web全量回归。
