import Foundation
@testable import OmaSealBarCore

final class FakeCLI: OmaSealCLI, @unchecked Sendable {
    var items: [SecretItem] = []
    var secrets: [String: String] = [:]
    var listError: Error?
    var getError: Error?
    var listCalls = 0
    var getCalls: [(String, String)] = []

    func list() async throws -> [SecretItem] {
        listCalls += 1
        if let listError { throw listError }
        return items
    }

    func get(service: String, account: String) async throws -> String {
        getCalls.append((service, account))
        if let getError { throw getError }
        let key = "\(service)/\(account)"
        guard let value = secrets[key] else {
            throw OmaSealCLIError.nonZeroExit(code: 1, stderr: "not found")
        }
        return value
    }
}

final class FakeAuthenticator: Authenticator, @unchecked Sendable {
    var allow = true
    var calls: [String] = []

    func authenticate(reason: String) async -> Bool {
        calls.append(reason)
        return allow
    }
}

final class FakePasteboard: Pasteboard {
    var string: String?
}

final class ManualScheduler: Scheduler, @unchecked Sendable {
    struct Work {
        var interval: TimeInterval
        var block: @Sendable () -> Void
    }

    var pending: [Work] = []

    func schedule(after interval: TimeInterval, _ work: @escaping @Sendable () -> Void) {
        pending.append(Work(interval: interval, block: work))
    }

    func runNext() {
        guard !pending.isEmpty else { return }
        let work = pending.removeFirst()
        work.block()
    }

    func runAll() {
        while !pending.isEmpty {
            runNext()
        }
    }
}
