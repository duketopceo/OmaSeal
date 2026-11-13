import XCTest
@testable import OmaSealBarCore

final class StatusFileTests: XCTestCase {
    func testDefaultPathUsesXDGStateHome() {
        let url = ReplicaStatusFile.defaultPath(environment: [
            "XDG_STATE_HOME": "/tmp/xdg-state",
            "HOME": "/tmp/home",
        ])
        XCTAssertEqual(url.path, "/tmp/xdg-state/omaseal/replica-status.json")
    }

    func testDefaultPathFallsBackToHomeLocalState() {
        let url = ReplicaStatusFile.defaultPath(environment: [
            "HOME": "/tmp/home",
        ])
        XCTAssertEqual(url.path, "/tmp/home/.local/state/omaseal/replica-status.json")
    }

    func testOverrideEnvWins() {
        let url = ReplicaStatusFile.defaultPath(environment: [
            "OMASEAL_REPLICA_STATUS": "/tmp/custom-status.json",
            "XDG_STATE_HOME": "/tmp/xdg-state",
        ])
        XCTAssertEqual(url.path, "/tmp/custom-status.json")
    }

    func testMissingFileIsNotAReplica() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("missing-replica-status.json")
        try? FileManager.default.removeItem(at: url)
        XCTAssertEqual(ReplicaStatusFile.load(from: url), .notAReplica)
        XCTAssertEqual(ReplicaStatusPresentation.notAReplica.line, "not a replica")
    }

    func testParsesSuccessfulStatus() throws {
        let json = """
        {
          "checked_at": "2026-11-13T06:45:00Z",
          "checksum": "abc",
          "error": null,
          "item_count": 2,
          "last_sync": "2026-11-13T06:32:01Z",
          "ok": true,
          "path": "/tmp/store.json"
        }
        """
        let status = try ReplicaStatusFile.parse(Data(json.utf8))
        XCTAssertTrue(status.ok)
        XCTAssertEqual(status.lastSync, "2026-11-13T06:32:01Z")
        XCTAssertEqual(status.itemCount, 2)
        XCTAssertNil(status.error)

        let presented = ReplicaStatusFile.present(Data(json.utf8))
        guard case .lastSync(let date) = presented else {
            return XCTFail("expected lastSync, got \(presented)")
        }
        XCTAssertEqual(date.timeIntervalSince1970, 1_794_551_521, accuracy: 0.5)
        XCTAssertTrue(presented.line.hasPrefix("Last sync:"))
    }

    func testFailedStatus() {
        let json = """
        {"ok": false, "checked_at": "2026-11-13T06:45:00Z", "error": "checksum mismatch", "last_sync": null, "checksum": null, "item_count": null, "path": "/tmp/store.json"}
        """
        let presented = ReplicaStatusFile.present(Data(json.utf8))
        XCTAssertEqual(presented, .failed("checksum mismatch"))
        XCTAssertEqual(presented.line, "Sync failed: checksum mismatch")
    }

    func testMalformedStatusIsUnreadable() {
        XCTAssertEqual(ReplicaStatusFile.present(Data("not-json".utf8)), .unreadable)
    }
}
