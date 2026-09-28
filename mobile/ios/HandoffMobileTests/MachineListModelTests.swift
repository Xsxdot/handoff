import XCTest
@testable import HandoffMobile

final class MachineListModelTests: XCTestCase {
    private func make(_ core: FakeConnectCore, _ rec: CallRecorder) -> MachineListModel {
        let bridge = CookieBridge(core: core, jar: FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec))
        return MachineListModel(core: core, bridge: bridge)
    }

    func testIteratesAndSkipsNilIndex() {
        let rec = CallRecorder(); let core = FakeConnectCore(recorder: rec)
        core.machines = [MachineView(name: "A", origin: "o", online: true),
                         MachineView(name: "B", origin: "", online: false)]
        XCTAssertEqual(make(core, rec).machines().map(\.name), ["A", "B"])   // 条 6
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
}
