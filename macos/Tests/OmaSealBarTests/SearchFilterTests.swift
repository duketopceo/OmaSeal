import XCTest
@testable import OmaSealBarCore

final class SearchFilterTests: XCTestCase {
    let items = [
        SecretItem(service: "openrouter", account: "default", label: "OpenRouter key"),
        SecretItem(service: "gh", account: "token", label: "GitHub"),
        SecretItem(service: "aws", account: "work", label: ""),
    ]

    func testEmptyQueryReturnsAll() {
        XCTAssertEqual(filterItems(items, query: ""), items)
        XCTAssertEqual(filterItems(items, query: "   "), items)
    }

    func testFiltersByServiceCaseInsensitive() {
        XCTAssertEqual(filterItems(items, query: "OPEN").map(\.service), ["openrouter"])
    }

    func testFiltersByAccount() {
        XCTAssertEqual(filterItems(items, query: "tok").map(\.account), ["token"])
    }

    func testFiltersByLabel() {
        XCTAssertEqual(filterItems(items, query: "github").map(\.service), ["gh"])
    }

    func testNoMatches() {
        XCTAssertEqual(filterItems(items, query: "zzzz"), [])
    }
}
