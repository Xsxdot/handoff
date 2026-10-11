import XCTest
import UIKit
@testable import HandoffMobile

final class MachineListModelTests: XCTestCase {
    private func make(_ core: FakeConnectCore, _ rec: CallRecorder) -> MachineListModel {
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec),
                                  onMain: { $0() })
        return MachineListModel(core: core, bridge: bridge)
    }

    // 条 6：machineCount 报 3，但界内索引 1 的 machineAt 返回 nil → 必须跳过且不崩。
    func testIteratesAndSkipsNilIndex() {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "A", origin: "o", online: true),
                         MachineView(name: "B", origin: "", online: false),
                         MachineView(name: "C", origin: "o", online: true)]
        core.nilIndices = [1]                       // 界内返回 nil
        XCTAssertEqual(make(core, rec).machines().map(\.name), ["A", "C"])   // 条 6
    }

    func testOfflineEnterDoesNotCallCoreNorLoad() {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "B", origin: "", online: false)]
        let exp = expectation(description: "enter")
        make(core, rec).enter(index: 0) {
            if case .failure(.offline) = $0 {} else { XCTFail("want offline") }
            exp.fulfill()
        }
        wait(for: [exp], timeout: 2)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("switchMachine") })   // 条 32
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("load") })
    }

    func testColdStartKeepsSavedOfflineMachineInsteadOfFallingBackToOnlineMachine() {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "A", origin: "", online: false),
                         MachineView(name: "B", origin: "http://127.0.0.1:50000", online: true)]
        let selection = FakeCurrentMachineStore("A")
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec), onMain: { $0() })
        let model = MachineListModel(core: core, bridge: bridge, selection: selection)
        XCTAssertNil(model.resumeIndex())
        XCTAssertEqual(model.currentMachine, "A")
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("switchMachine") })
    }

    func testColdStartTargetsOnlySavedOnlineMachine() {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "A", origin: "http://127.0.0.1:50000", online: true),
                         MachineView(name: "B", origin: "http://127.0.0.1:50000", online: true)]
        let selection = FakeCurrentMachineStore("A")
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec), onMain: { $0() })
        XCTAssertEqual(MachineListModel(core: core, bridge: bridge, selection: selection).resumeIndex(), 0)
    }

    func testRestoreByMachineNameUsesFreshIndexAfterListReorders() {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "B", origin: "http://127.0.0.1:50001", online: true),
                         MachineView(name: "A", origin: "http://127.0.0.1:50000", online: true)]
        let model = make(core, rec)
        let exp = expectation(description: "restore saved A")
        model.enter(machine: "A") { result in
            if case .failure(let error) = result { XCTFail("restore failed: \(error)") }
            exp.fulfill()
        }
        wait(for: [exp], timeout: 2)
        XCTAssertTrue(rec.calls.contains("switchMachine:A"), "恢复必须按保存的机器名匹配新列表")
        XCTAssertFalse(rec.calls.contains("switchMachine:B"))
    }

    func testManualRetryRevalidatesStoredPairingThenEntersOnlySavedA() throws {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "A", origin: "http://127.0.0.1:50000", online: false),
                         MachineView(name: "B", origin: "http://127.0.0.1:50001", online: true)]
        core.onPair = {
            core.machines[0] = MachineView(name: "A", origin: "http://127.0.0.1:50000", online: true)
        }
        let selection = FakeCurrentMachineStore("A")
        let savedBundle = FakeSecureStore(); savedBundle.stored = "opaque-bundle"
        let pairing = PairingService(core: core, store: savedBundle, selection: selection)
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec), onMain: { $0() })
        let model = MachineListModel(core: core, bridge: bridge, selection: selection)
        let refreshed = model.refreshSavedMachine(revalidatePairing: { try pairing.pairFromStoredBundle() })
        if case .failure(let error) = refreshed { XCTFail("retry failed: \(error)") }
        let entered = expectation(description: "enter refreshed A")
        model.enter(machine: try refreshed.get().name) { result in
            if case .failure(let error) = result { XCTFail("saved A entry failed: \(error)") }
            entered.fulfill()
        }
        wait(for: [entered], timeout: 2)
        XCTAssertEqual(rec.calls.filter { $0 == "pair:opaque-bundle" }.count, 1)
        XCTAssertEqual(try refreshed.get().name, "A")
        XCTAssertTrue(rec.calls.contains("switchMachine:A"))
        XCTAssertFalse(rec.calls.contains("switchMachine:B"))
        XCTAssertEqual(selection.read(), "A")
    }

    func testManualRetryStillOfflineKeepsAAndNeverFallsBackToB() throws {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "A", origin: "", online: false),
                         MachineView(name: "B", origin: "http://127.0.0.1:50001", online: true)]
        let selection = FakeCurrentMachineStore("A")
        let savedBundle = FakeSecureStore(); savedBundle.stored = "opaque-bundle"
        let pairing = PairingService(core: core, store: savedBundle, selection: selection)
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec), onMain: { $0() })
        let model = MachineListModel(core: core, bridge: bridge, selection: selection)
        let refreshed = model.refreshSavedMachine(revalidatePairing: { try pairing.pairFromStoredBundle() })
        if case .success = refreshed { XCTFail("offline A must remain unavailable") }
        XCTAssertEqual(rec.calls.filter { $0 == "pair:opaque-bundle" }.count, 1)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("switchMachine:") })
        XCTAssertEqual(selection.read(), "A")
    }
}

final class MachineListLayoutTests: XCTestCase {
    func testMachineRowsAndEnterActionFitInSmallLightEntryOnDarkSystem() {
        assertMachineListLayout(size: CGSize(width: 320, height: 568), style: .dark,
                                attachmentName: "Machine chooser small screen")
    }

    func testMachineRowsAndEnterActionFitInPhoneEntry() {
        assertMachineListLayout(size: CGSize(width: 393, height: 852), style: .light,
                                attachmentName: "Machine chooser phone")
    }

    func testLongMachineListScrollsWithoutCoveringEnterAction() {
        assertMachineListLayout(size: CGSize(width: 320, height: 568), style: .light,
                                attachmentName: "Machine chooser long list", machineCount: 12)
    }

    func testLaunchStoryboardConstrainsExistingLogoToCentered64PointView() {
        let storyboard = UIStoryboard(name: "LaunchScreen", bundle: Bundle(for: MachineListViewController.self))
        guard let launch = storyboard.instantiateInitialViewController() else {
            XCTFail("LaunchScreen storyboard should instantiate its launch view")
            return
        }
        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 393, height: 852))
        window.rootViewController = launch
        window.makeKeyAndVisible()
        launch.view.frame = window.bounds
        window.layoutIfNeeded()
        launch.view.layoutIfNeeded()

        guard let logo = findView("launch-logo", in: launch.view) else {
            XCTFail("Launch storyboard should contain the existing HandoffLogo image")
            return
        }
        XCTAssertEqual(logo.frame.size.width, 64, accuracy: 0.5)
        XCTAssertEqual(logo.frame.size.height, 64, accuracy: 0.5)
        XCTAssertEqual(logo.center.x, window.bounds.midX, accuracy: 0.5)
        XCTAssertEqual(logo.center.y, window.bounds.midY, accuracy: 0.5)
        attach(window, named: "Launch screen constrained logo")
        window.isHidden = true
    }

    private func assertMachineListLayout(size: CGSize, style: UIUserInterfaceStyle, attachmentName: String,
                                        machineCount: Int = 3) {
        let recorder = CallRecorder()
        let core = FakeConnectCore(recorder: recorder)
        core.machines = [
            MachineView(name: "A", origin: "http://127.0.0.1:50000", online: true),
            MachineView(name: "B", origin: "http://127.0.0.1:50001", online: true),
            MachineView(name: "C", origin: "http://127.0.0.1:50002", online: true),
        ]
        if machineCount > 3 {
            core.machines += (3..<machineCount).map {
                MachineView(name: "Machine-\($0)", origin: "http://127.0.0.1:\(50000 + $0)", online: true)
            }
        }
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: recorder),
                                  loader: FakeOriginLoader(recorder: recorder), onMain: { $0() })
        let composition = AppComposition(core: core, store: FakeSecureStore(),
                                         currentMachine: FakeCurrentMachineStore(), cookieBridge: bridge)
        let controller = MachineListViewController(composition: composition)
        let navigation = UINavigationController(rootViewController: controller)
        let window = UIWindow(frame: CGRect(origin: .zero, size: size))
        window.overrideUserInterfaceStyle = style
        window.rootViewController = navigation
        window.makeKeyAndVisible()
        defer { window.isHidden = true }

        window.layoutIfNeeded()
        navigation.view.layoutIfNeeded()
        controller.view.layoutIfNeeded()

        let table = findView("machine-list.table", in: controller.view) as? UITableView
        let enter = findView("machine-list.enter", in: controller.view) as? UIButton
        XCTAssertNotNil(table)
        XCTAssertNotNil(enter)
        guard let table, let enter else { return }
        XCTAssertGreaterThanOrEqual(table.frame.height, 3 * 72, "三台机器需要有可见的列表高度")
        XCTAssertNotNil(table.cellForRow(at: IndexPath(row: 0, section: 0)))
        XCTAssertNotNil(table.cellForRow(at: IndexPath(row: 1, section: 0)))
        if machineCount == 3 {
            XCTAssertEqual(table.indexPathsForVisibleRows?.count, 3, "三行机器必须实际进入可见窗口")
            XCTAssertLessThanOrEqual(table.frame.height, table.contentSize.height + 1,
                                     "少量机器的卡片不应在末行后扩张成大块空白")
        } else {
            XCTAssertGreaterThan(table.contentSize.height, table.frame.height, "多台机器必须能滚动")
            table.scrollToRow(at: IndexPath(row: machineCount - 1, section: 0), at: .bottom, animated: false)
            table.layoutIfNeeded()
            XCTAssertNotNil(table.cellForRow(at: IndexPath(row: machineCount - 1, section: 0)),
                            "滚动后最后一台机器必须可见")
        }
        XCTAssertTrue(enter.isEnabled, "至少有一台在线机器时进入按钮可用")
        XCTAssertFalse(table.frame.intersects(enter.frame), "列表不能压住底部 CTA")
        XCTAssertEqual(controller.traitCollection.userInterfaceStyle, .light, "入口需保持批准的浅色可读样式")
        attach(window, named: attachmentName)
        window.isHidden = true
    }

    private func attach(_ window: UIWindow, named name: String) {
        // Layout tests use an offscreen window; drawHierarchy can return an empty image
        // without a connected scene. Render its actual UIKit layers as component evidence.
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { context in
            window.layer.render(in: context.cgContext)
        }
        let attachment = XCTAttachment(image: image)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func findView(_ identifier: String, in root: UIView) -> UIView? {
        if root.accessibilityIdentifier == identifier { return root }
        for child in root.subviews {
            if let found = findView(identifier, in: child) { return found }
        }
        return nil
    }
}
