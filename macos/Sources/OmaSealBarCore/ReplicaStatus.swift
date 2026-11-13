import Foundation

/// Status sidecar written by `contrib/sync/omaseal-replica-check` (#75).
/// Default path: `$XDG_STATE_HOME/omaseal/replica-status.json`, or
/// `$HOME/.local/state/omaseal/replica-status.json` when XDG_STATE_HOME is unset.
public struct ReplicaStatus: Equatable, Sendable, Decodable {
    public var ok: Bool
    public var checkedAt: String
    public var lastSync: String?
    public var checksum: String?
    public var itemCount: Int?
    public var path: String?
    public var error: String?

    public init(ok: Bool, checkedAt: String = "", lastSync: String? = nil, checksum: String? = nil, itemCount: Int? = nil, path: String? = nil, error: String? = nil) {
        self.ok = ok
        self.checkedAt = checkedAt
        self.lastSync = lastSync
        self.checksum = checksum
        self.itemCount = itemCount
        self.path = path
        self.error = error
    }

    enum CodingKeys: String, CodingKey {
        case ok
        case checkedAt = "checked_at"
        case lastSync = "last_sync"
        case checksum
        case itemCount = "item_count"
        case path
        case error
    }
}

public enum ReplicaStatusPresentation: Equatable, Sendable {
    /// No status file — this machine is not running the replica checker.
    case notAReplica
    case lastSync(Date)
    case failed(String)
    case unreadable

    public var line: String {
        switch self {
        case .notAReplica:
            return "not a replica"
        case .lastSync(let date):
            let formatted = ReplicaStatusPresentation.displayFormatter.string(from: date)
            return "Last sync: \(formatted)"
        case .failed(let message):
            if message.isEmpty {
                return "Sync failed"
            }
            return "Sync failed: \(message)"
        case .unreadable:
            return "Replica status unreadable"
        }
    }

    private static let displayFormatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateStyle = .medium
        f.timeStyle = .short
        return f
    }()
}

public enum ReplicaStatusFile {
    /// Same resolution as `omaseal-replica-check` in contrib/sync (#75).
    public static func defaultPath(environment: [String: String] = ProcessInfo.processInfo.environment) -> URL {
        if let override = environment["OMASEAL_REPLICA_STATUS"], !override.isEmpty {
            return URL(fileURLWithPath: override)
        }
        let base: String
        if let xdg = environment["XDG_STATE_HOME"], !xdg.isEmpty {
            base = xdg
        } else {
            let home = environment["HOME"] ?? NSHomeDirectory()
            base = (home as NSString).appendingPathComponent(".local/state")
        }
        return URL(fileURLWithPath: base)
            .appendingPathComponent("omaseal")
            .appendingPathComponent("replica-status.json")
    }

    public static func parse(_ data: Data) throws -> ReplicaStatus {
        try JSONDecoder().decode(ReplicaStatus.self, from: data)
    }

    public static func load(from url: URL, fileManager: FileManager = .default) -> ReplicaStatusPresentation {
        guard fileManager.fileExists(atPath: url.path) else {
            return .notAReplica
        }
        guard let data = fileManager.contents(atPath: url.path) else {
            return .unreadable
        }
        return present(data)
    }

    public static func present(_ data: Data) -> ReplicaStatusPresentation {
        guard let status = try? parse(data) else {
            return .unreadable
        }
        if !status.ok {
            return .failed(status.error ?? "")
        }
        if let raw = status.lastSync, let date = parseUTC(raw) {
            return .lastSync(date)
        }
        return .unreadable
    }

    static func parseUTC(_ raw: String) -> Date? {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime]
        if let date = iso.date(from: raw) {
            return date
        }
        // #75 writes `YYYY-MM-DDTHH:MM:SSZ` (no fractional seconds).
        let fallback = DateFormatter()
        fallback.locale = Locale(identifier: "en_US_POSIX")
        fallback.timeZone = TimeZone(secondsFromGMT: 0)
        fallback.dateFormat = "yyyy-MM-dd'T'HH:mm:ss'Z'"
        return fallback.date(from: raw)
    }
}
