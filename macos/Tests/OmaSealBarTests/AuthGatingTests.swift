import XCTest
@testable import OmaSealBarCore

@MainActor
final class AuthGatingTests: XCTestCase {
    let item = SecretItem(service: "gh", account: "token", label: "GitHub")

    func makeModel(allow: Bool, value: String = "s3cret") -> (MenuBarModel, FakeCLI, FakeAuthenticator, FakePasteboard, ManualScheduler) {
        let cli = FakeCLI()
        cli.items = [item]
        cli.secrets = ["gh/token": value]
        let auth = FakeAuthenticator()
        auth.allow = allow
        let board = FakePasteboard()
        let scheduler = ManualScheduler()
        let clipboard = ClipboardClearer(pasteboard: board, scheduler: scheduler, interval: 30)
        let model = MenuBarModel(
            cli: cli,
            authenticator: auth,
            clipboard: clipboard,
            statusURL: URL(fileURLWithPath: "/tmp/does-not-exist-replica-status.json")
        )
        return (model, cli, auth, board, scheduler)
    }

    func testDeniedRevealDoesNotCallGet() async {
        let (model, cli, auth, _, _) = makeModel(allow: false)
        await model.reveal(item)
        XCTAssertEqual(cli.getCalls.count, 0)
        XCTAssertEqual(auth.calls.count, 1)
        XCTAssertNil(model.revealed)
    }

    func testAllowedRevealCallsGetOnceAndStoresValue() async {
        let (model, cli, _, _, _) = makeModel(allow: true)
        await model.reveal(item)
        XCTAssertEqual(cli.getCalls.count, 1)
        XCTAssertEqual(cli.getCalls.first?.0, "gh")
        XCTAssertEqual(cli.getCalls.first?.1, "token")
        XCTAssertEqual(model.revealed?.value, "s3cret")
        XCTAssertEqual(model.revealed?.item, item)
    }

    func testDeniedCopyDoesNotCallGetOrSetPasteboard() async {
        let (model, cli, _, board, _) = makeModel(allow: false)
        await model.copy(item)
        XCTAssertEqual(cli.getCalls.count, 0)
        XCTAssertNil(board.string)
        XCTAssertNil(model.revealed)
    }

    func testAllowedCopyFetchesAndSetsPasteboard() async {
        let (model, cli, _, board, scheduler) = makeModel(allow: true)
        await model.copy(item)
        XCTAssertEqual(cli.getCalls.count, 1)
        XCTAssertEqual(board.string, "s3cret")
        scheduler.runAll()
        XCTAssertNil(board.string)
    }

    func testCopyOfAlreadyRevealedItemSkipsSecondGet() async {
        let (model, cli, _, board, _) = makeModel(allow: true)
        await model.reveal(item)
        XCTAssertEqual(cli.getCalls.count, 1)
        await model.copy(item)
        XCTAssertEqual(cli.getCalls.count, 1)
        XCTAssertEqual(board.string, "s3cret")
    }

    func testForgetSecretsDropsInMemoryValue() async {
        let (model, _, _, _, _) = makeModel(allow: true)
        await model.reveal(item)
        XCTAssertNotNil(model.revealed)
        model.forgetSecrets()
        XCTAssertNil(model.revealed)
    }

    func testListErrorDoesNotInventItems() async {
        let (model, cli, _, _, _) = makeModel(allow: true)
        cli.listError = OmaSealCLIError.binaryNotFound
        await model.refresh()
        XCTAssertEqual(model.items, [])
        XCTAssertNotNil(model.loadError)
        XCTAssertEqual(model.status, .notAReplica)
    }
}
