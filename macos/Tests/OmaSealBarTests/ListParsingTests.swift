import XCTest
@testable import OmaSealBarCore

final class ListParsingTests: XCTestCase {
    func testParsesListJSONArray() throws {
        let json = """
        [{"service":"openrouter","account":"default","label":"OpenRouter key","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","owned":true},{"service":"gh","account":"token","label":"GitHub","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","owned":true}]
        """
        let items = try SecretItem.parseListJSON(Data(json.utf8))
        XCTAssertEqual(items.count, 2)
        XCTAssertEqual(items[0].service, "openrouter")
        XCTAssertEqual(items[0].account, "default")
        XCTAssertEqual(items[0].label, "OpenRouter key")
        XCTAssertEqual(items[1].service, "gh")
        XCTAssertEqual(items[1].account, "token")
        XCTAssertEqual(items[0].id, "openrouter/default")
    }

    func testParsesEmptyArray() throws {
        let items = try SecretItem.parseListJSON(Data("[]\n".utf8))
        XCTAssertEqual(items, [])
    }

    func testMissingLabelDefaultsToEmpty() throws {
        let json = #"[{"service":"svc","account":"acct"}]"#
        let items = try SecretItem.parseListJSON(Data(json.utf8))
        XCTAssertEqual(items, [SecretItem(service: "svc", account: "acct", label: "")])
    }

    func testInvalidJSONThrows() {
        XCTAssertThrowsError(try SecretItem.parseListJSON(Data("not-json".utf8)))
    }

    func testIgnoresUnknownFields() throws {
        let json = #"[{"service":"s","account":"a","label":"L","rotation_flagged":true,"owned":false}]"#
        let items = try SecretItem.parseListJSON(Data(json.utf8))
        XCTAssertEqual(items.first?.service, "s")
        XCTAssertEqual(items.first?.label, "L")
    }
}
