import LocalAuthentication
import XCTest
@testable import OmaSealBarCore

private struct FakeEvaluator: PresenceEvaluator {
    var gui = true
    var canEvalError: Int?
    var evalError: Int?
    func hasGUISession() -> Bool { gui }
    func canEvaluate() -> Int? { canEvalError }
    func evaluate(reason: String) async -> Int? { evalError }
}

final class PresenceTests: XCTestCase {
    private func run(_ e: FakeEvaluator) async -> PresenceOutcome {
        await PresenceCheck.run(reason: "test", evaluator: e)
    }

    func testSuccessExitsZero() async {
        let o = await run(FakeEvaluator())
        XCTAssertEqual(o, .authenticated)
        XCTAssertEqual(o.exitCode, 0)
        XCTAssertNil(o.message)
    }

    func testCancelAndFailureExitOne() async {
        for code in [LAError.userCancel, .authenticationFailed, .systemCancel, .appCancel, .userFallback] {
            let o = await run(FakeEvaluator(evalError: code.rawValue))
            XCTAssertEqual(o.exitCode, 1, "\(code)")
        }
    }

    func testUnknownErrorFailsClosedAsDenied() async {
        let o = await run(FakeEvaluator(evalError: 99999))
        XCTAssertEqual(o.exitCode, 1)
    }

    func testNoGUISessionExitsTwo() async {
        let o = await run(FakeEvaluator(gui: false))
        XCTAssertEqual(o.exitCode, 2)
    }

    func testCannotEvaluateExitsTwo() async {
        let o = await run(FakeEvaluator(canEvalError: LAError.passcodeNotSet.rawValue))
        XCTAssertEqual(o.exitCode, 2)
    }

    func testNotInteractiveDuringPromptExitsTwo() async {
        let o = await run(FakeEvaluator(evalError: LAError.notInteractive.rawValue))
        XCTAssertEqual(o.exitCode, 2)
    }

    func testReasonSanitizing() {
        XCTAssertEqual(PresenceCheck.sanitizeReason("a\u{0}b\nc\u{7}d"), "abcd")
        XCTAssertEqual(PresenceCheck.sanitizeReason(String(repeating: "x", count: 500)).count, 200)
    }

    func testArgParsing() {
        XCTAssertEqual(PresenceCheck.parseReason(["--reason", "unlock"]), "unlock")
        XCTAssertNil(PresenceCheck.parseReason([]))
        XCTAssertNil(PresenceCheck.parseReason(["--reason"]))
        XCTAssertNil(PresenceCheck.parseReason(["--reason", "a", "b"]))
        XCTAssertNil(PresenceCheck.parseReason(["--other", "a"]))
        XCTAssertNil(PresenceCheck.parseReason(["--reason", "\n\u{1}"]))
    }
}
