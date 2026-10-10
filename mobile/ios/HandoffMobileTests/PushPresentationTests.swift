import XCTest
@testable import HandoffMobile

// B432 验收①：前台且用户已在控制台（对应面已打开）→ 不弹系统横幅；
// 其余情形交给系统弹。纯函数，三条分支逐条可判。
final class PushPresentationTests: XCTestCase {
    func testForegroundOnRelevantScreenSuppressesBanner() {
        XCTAssertFalse(PushPresentation.shouldPresent(isForeground: true, onRelevantScreen: true),
                       "前台且对应面已打开：不重复弹系统横幅（spec 验收①）")
    }

    func testForegroundElsewhereStillPresents() {
        XCTAssertTrue(PushPresentation.shouldPresent(isForeground: true, onRelevantScreen: false),
                      "前台但不在对应面：必须弹（否则用户看不到新待办）")
    }

    func testBackgroundAlwaysPresents() {
        XCTAssertTrue(PushPresentation.shouldPresent(isForeground: false, onRelevantScreen: true))
        XCTAssertTrue(PushPresentation.shouldPresent(isForeground: false, onRelevantScreen: false))
    }

    // MARK: - 修刀（2026-10-10）：onRelevantScreen 按路由判定，禁 currentMachine!=nil 近似

    // 对应卡：深链 /cards?card=X 且当前路由 card 参数一致 → 在对应面。
    func testRelevantRouteMatchingCard() {
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                       currentRoute: "/cards?card=B432"))
    }

    // 别的卡：当前在 card=Y，通知指向 card=X → 不在对应面（前台仍弹）。
    func testRelevantRouteDifferentCardIsNotRelevant() {
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                        currentRoute: "/cards?card=B999"))
    }

    // 工作台「需要你」入口（首页，MobileWorkspace 的需要你处理行所在面）：
    // 深链指向卡、但用户在工作台首页 → 也算在对应面（聚合入口在场）。
    func testRelevantRouteCardNotifOnWorkbenchHomeIsRelevant() {
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                       currentRoute: "/"))
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                       currentRoute: "/?tab=projects"),
                      "/?tab=projects 与 / 同面（缺省 tab=projects 即 MobileWorkspace）")
    }

    // 无深链通知（落工作台）：用户在工作台首页 → 对应面。
    func testRelevantRouteNoDeepLinkOnWorkbenchHome() {
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: nil, currentRoute: "/"))
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: nil, currentRoute: "/?tab=projects"))
    }

    // 无深链通知：用户在别的卡/别的 tab → 别处，前台仍弹。
    func testRelevantRouteNoDeepLinkElsewhereIsNotRelevant() {
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: nil,
                                                        currentRoute: "/cards?card=B432"))
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: nil,
                                                        currentRoute: "/?tab=settings"),
                       "设置页不是「需要你」入口")
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: nil,
                                                        currentRoute: "/?tab=sessions"),
                       "会话 tab 不是工作台「需要你」入口本身")
    }

    // 卡列表 /cards（无 card 参数）不是任何通知的对应卡，也不是工作台入口。
    func testRelevantRouteCardsListIsNotRelevant() {
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                        currentRoute: "/cards"))
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: nil,
                                                        currentRoute: "/cards"))
    }

    // 未进入/未加载：currentRoute 空串 → 一律别处（后台路径不受影响，此处只测路由判定）。
    func testRelevantRouteEmptyCurrentIsNotRelevant() {
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                        currentRoute: ""))
        XCTAssertFalse(PushPresentation.isRelevantRoute(notificationRoute: nil, currentRoute: ""))
    }

    // 首页带无关参数（card= 只在 /cards 宿主上有语汇）仍算工作台首页 → 在对应面。
    func testRelevantRouteIgnoresIrrelevantQueryOnHome() {
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: "/cards?card=B432",
                                                       currentRoute: "/?card=B432"),
                      "card 参数只在 /cards 宿主上是卡深链语汇；/?card=B432 的宿主仍是首页")
        XCTAssertTrue(PushPresentation.isRelevantRoute(notificationRoute: nil,
                                                       currentRoute: "/?tab=projects&archive=1"))
    }
}
