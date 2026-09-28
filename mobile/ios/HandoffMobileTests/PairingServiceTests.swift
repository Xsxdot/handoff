import XCTest
@testable import HandoffMobile

final class PairingServiceTests: XCTestCase {
    func testPairTrimsAndPassesWholeStringThenStores() throws {
        let core = FakeConnectCore(); let store = FakeSecureStore()
        let svc = PairingService(core: core, store: store)
        try svc.pair(bundleJSON: "  {\"v\":1}\n")
        XCTAssertEqual(store.stored, "{\"v\":1}", "存的是 trim 后的原始整串（条 10/17）")
        XCTAssertEqual(store.stored?.utf8.count, "{\"v\":1}".utf8.count)
    }

    func testPairErrorDoesNotStoreNorList() {
        let core = FakeConnectCore(); core.pairError = NSError(domain: "x", code: 1)
        let store = FakeSecureStore()
        let svc = PairingService(core: core, store: store)
        XCTAssertThrowsError(try svc.pair(bundleJSON: "bad")) { err in
            guard let se = err as? ShellError, case .pairing = se else {
                return XCTFail("want pairing, got \(err)")
            }
        }
        XCTAssertNil(store.stored)   // 条 9：Pair error 不落存储
    }

    func testStartupNoStoredGoesPairing() throws {
        let svc = PairingService(core: FakeConnectCore(), store: FakeSecureStore())
        XCTAssertFalse(try svc.pairFromStoredBundle())   // 条 15
    }

    func testStartupStoredPairsSameBytes() throws {
        let core = FakeConnectCore(); let store = FakeSecureStore(); store.stored = "raw-bundle"
        let svc = PairingService(core: core, store: store)
        XCTAssertTrue(try svc.pairFromStoredBundle())    // 条 15/17
    }

    func testStartupStoredPairFailureThrowsPairing() {
        let core = FakeConnectCore(); core.pairError = NSError(domain: "x", code: 2)
        let store = FakeSecureStore(); store.stored = "raw-bundle"
        let svc = PairingService(core: core, store: store)
        XCTAssertThrowsError(try svc.pairFromStoredBundle()) { err in
            guard let se = err as? ShellError, case .pairing = se else { return XCTFail("want pairing") }
        }   // 条 16：不复用半态，调用方回配对屏
    }
}
