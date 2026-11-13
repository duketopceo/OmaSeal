import Foundation

public enum OmaSealCLIError: Error, LocalizedError, Equatable {
    case binaryNotFound
    case nonZeroExit(code: Int32, stderr: String)
    case unreadableOutput

    public var errorDescription: String? {
        switch self {
        case .binaryNotFound:
            return "omaseal binary not found on PATH (including /opt/homebrew/bin)"
        case .nonZeroExit(let code, let stderr):
            let detail = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            if detail.isEmpty {
                return "omaseal exited \(code)"
            }
            return "omaseal exited \(code): \(detail)"
        case .unreadableOutput:
            return "omaseal produced output that could not be parsed"
        }
    }
}

/// Talks to OmaSeal only by shelling out to the `omaseal` CLI.
public protocol OmaSealCLI: Sendable {
    func list() async throws -> [SecretItem]
    func get(service: String, account: String) async throws -> String
}

/// Resolves the `omaseal` binary: an explicit path, then PATH, then
/// `/opt/homebrew/bin` and `/usr/local/bin`.
public func resolveOmaSealBinary(explicit: String? = nil, environment: [String: String] = ProcessInfo.processInfo.environment, fileManager: FileManager = .default) -> String? {
    if let explicit, !explicit.isEmpty, fileManager.isExecutableFile(atPath: explicit) {
        return explicit
    }
    var parts = (environment["PATH"] ?? "").split(separator: ":").map(String.init)
    for extra in ["/opt/homebrew/bin", "/usr/local/bin"] {
        if !parts.contains(extra) {
            parts.append(extra)
        }
    }
    for dir in parts where !dir.isEmpty {
        let candidate = URL(fileURLWithPath: dir).appendingPathComponent("omaseal").path
        if fileManager.isExecutableFile(atPath: candidate) {
            return candidate
        }
    }
    return nil
}

public struct ProcessOmaSealCLI: OmaSealCLI, Sendable {
    public var binaryPath: String?

    public init(binaryPath: String? = nil) {
        self.binaryPath = binaryPath
    }

    public func list() async throws -> [SecretItem] {
        let data = try await run(arguments: ["list", "--json"], captureSecret: false)
        do {
            return try SecretItem.parseListJSON(data)
        } catch {
            throw OmaSealCLIError.unreadableOutput
        }
    }

    public func get(service: String, account: String) async throws -> String {
        // stdout is the raw secret (`fmt.Print(secret)`). Never log it.
        let data = try await run(arguments: ["get", service, account], captureSecret: true)
        guard let value = String(data: data, encoding: .utf8) else {
            throw OmaSealCLIError.unreadableOutput
        }
        return value
    }

    private func run(arguments: [String], captureSecret: Bool) async throws -> Data {
        guard let path = resolveOmaSealBinary(explicit: binaryPath) else {
            throw OmaSealCLIError.binaryNotFound
        }
        return try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                do {
                    let result = try Self.launch(path: path, arguments: arguments, captureSecret: captureSecret)
                    continuation.resume(returning: result)
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    private static func launch(path: String, arguments: [String], captureSecret: Bool) throws -> Data {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = arguments
        let stdout = Pipe()
        let stderr = Pipe()
        process.standardOutput = stdout
        process.standardError = stderr
        process.standardInput = FileHandle.nullDevice
        try process.run()
        process.waitUntilExit()
        let errData = stderr.fileHandleForReading.readDataToEndOfFile()
        let outData = stdout.fileHandleForReading.readDataToEndOfFile()
        if process.terminationStatus != 0 {
            // Include stderr only. Never include stdout — for `get` it is the secret.
            let errText = String(data: errData, encoding: .utf8) ?? ""
            _ = captureSecret // stdout is intentionally discarded on failure
            throw OmaSealCLIError.nonZeroExit(code: process.terminationStatus, stderr: errText)
        }
        return outData
    }
}
