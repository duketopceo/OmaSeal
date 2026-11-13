import XCTest
@testable import OmaSealBarCore

final class ClipboardClearTests: XCTestCase {
    func testClearsAfterIntervalIfValueUnchanged() {
        let board = FakePasteboard()
        let scheduler = ManualScheduler()
        let clearer = ClipboardClearer(pasteboard: board, scheduler: scheduler, interval: 30)

        clearer.copy("secret-one")
        XCTAssertEqual(board.string, "secret-one")
        XCTAssertEqual(scheduler.pending.count, 1)
        XCTAssertEqual(scheduler.pending[0].interval, 30)

        scheduler.runNext()
        XCTAssertNil(board.string)
    }

    func testDoesNotClearIfPasteboardChanged() {
        let board = FakePasteboard()
        let scheduler = ManualScheduler()
        let clearer = ClipboardClearer(pasteboard: board, scheduler: scheduler, interval: 30)

        clearer.copy("secret-one")
        board.string = "something-else"
        scheduler.runNext()
        XCTAssertEqual(board.string, "something-else")
    }

    func testSecondCopyRestartsTimer() {
        let board = FakePasteboard()
        let scheduler = ManualScheduler()
        let clearer = ClipboardClearer(pasteboard: board, scheduler: scheduler, interval: 30)

        clearer.copy("first")
        clearer.copy("second")
        XCTAssertEqual(board.string, "second")
        XCTAssertEqual(scheduler.pending.count, 2)

        scheduler.runNext() // stale first callback
        XCTAssertEqual(board.string, "second")

        scheduler.runNext()
        XCTAssertNil(board.string)
    }
}
