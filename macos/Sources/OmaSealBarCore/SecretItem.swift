import Foundation

/// Metadata for one stored secret. The secret value is never stored here;
/// callers must request it with `OmaSealCLI.get` after a presence check.
public struct SecretItem: Equatable, Sendable, Identifiable, Decodable {
    public var service: String
    public var account: String
    public var label: String

    public var id: String { "\(service)/\(account)" }

    public init(service: String, account: String, label: String = "") {
        self.service = service
        self.account = account
        self.label = label
    }

    enum CodingKeys: String, CodingKey {
        case service, account, label
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        service = try c.decode(String.self, forKey: .service)
        account = try c.decode(String.self, forKey: .account)
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? ""
    }

    /// Parse `omaseal list --json` (a single-line JSON array of Item objects).
    public static func parseListJSON(_ data: Data) throws -> [SecretItem] {
        let trimmed = data.trimmingASCIINewlines()
        if trimmed.isEmpty {
            return []
        }
        return try JSONDecoder().decode([SecretItem].self, from: trimmed)
    }

    /// Case-insensitive match against service, account, or label.
    public func matches(_ query: String) -> Bool {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        if q.isEmpty { return true }
        return service.localizedCaseInsensitiveContains(q)
            || account.localizedCaseInsensitiveContains(q)
            || label.localizedCaseInsensitiveContains(q)
    }
}

public func filterItems(_ items: [SecretItem], query: String) -> [SecretItem] {
    let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
    if q.isEmpty { return items }
    return items.filter { $0.matches(q) }
}

extension Data {
    fileprivate func trimmingASCIINewlines() -> Data {
        var start = startIndex
        var end = endIndex
        while start < end, self[start] == 0x0A || self[start] == 0x0D || self[start] == 0x20 {
            start += 1
        }
        while end > start, self[end - 1] == 0x0A || self[end - 1] == 0x0D || self[end - 1] == 0x20 {
            end -= 1
        }
        return self[start..<end]
    }
}
