import Foundation
import LocalAuthentication
import Security

/// Result of one user-presence check. Maps 1:1 to the helper's exit code.
public enum PresenceOutcome: Equatable, Sendable {
    /// The user authenticated (Touch ID or device password).
    case authenticated
    /// The user cancelled, failed authentication, or the system cancelled.
    case denied(String)
    /// No prompt could be shown: no GUI session, policy unevaluable, not interactive.
    case unavailable(String)

    /// 0 = authenticated, 1 = denied, 2 = unavailable. The Go side relies on this.
    public var exitCode: Int32 {
        switch self {
        case .authenticated: return 0
        case .denied: return 1
        case .unavailable: return 2
        }
    }

    /// One-line, non-secret stderr text, or nil on success.
    public var message: String? {
        switch self {
        case .authenticated: return nil
        case .denied(let m): return "denied: \(m)"
        case .unavailable(let m): return "unavailable: \(m)"
        }
    }
}

/// Seam over LocalAuthentication so the exit-code mapping is testable without a prompt.
public protocol PresenceEvaluator: Sendable {
    /// Whether a GUI session exists that can show a prompt.
    func hasGUISession() -> Bool
    /// LAContext.canEvaluatePolicy(.deviceOwnerAuthentication). nil = yes, else the LAError code.
    func canEvaluate() -> Int?
    /// Runs the prompt. nil = success, else the LAError code (or another non-nil failure code).
    func evaluate(reason: String) async -> Int?
}

public enum PresenceCheck {
    public static let maxReasonLength = 200

    /// Strips control characters and truncates to 200 characters.
    public static func sanitizeReason(_ raw: String) -> String {
        let kept = raw.unicodeScalars.filter { !CharacterSet.controlCharacters.contains($0) }
        var view = String.UnicodeScalarView()
        view.append(contentsOf: kept)
        let trimmed = String(view).trimmingCharacters(in: .whitespaces)
        return String(trimmed.prefix(maxReasonLength))
    }

    /// Parses argv (without argv[0]). Only `--reason <text>` is accepted.
    public static func parseReason(_ args: [String]) -> String? {
        guard args.count == 2, args[0] == "--reason" else { return nil }
        let r = sanitizeReason(args[1])
        return r.isEmpty ? nil : r
    }

    /// Maps an LAError code to denied vs unavailable.
    public static func classify(errorCode code: Int) -> PresenceOutcome {
        switch code {
        case LAError.userCancel.rawValue, LAError.userFallback.rawValue,
             LAError.authenticationFailed.rawValue, LAError.appCancel.rawValue,
             LAError.systemCancel.rawValue, LAError.biometryLockout.rawValue:
            return .denied("not confirmed (LAError \(code))")
        case LAError.notInteractive.rawValue, LAError.passcodeNotSet.rawValue,
             LAError.biometryNotAvailable.rawValue, LAError.biometryNotEnrolled.rawValue:
            return .unavailable("no usable authentication (LAError \(code))")
        default:
            // Unknown failure after a prompt may have been shown: fail closed as denied.
            return .denied("authentication failed (LAError \(code))")
        }
    }

    public static func run(reason: String, evaluator: PresenceEvaluator) async -> PresenceOutcome {
        guard evaluator.hasGUISession() else {
            return .unavailable("no GUI session")
        }
        if let code = evaluator.canEvaluate() {
            return .unavailable("cannot evaluate policy (LAError \(code))")
        }
        guard let code = await evaluator.evaluate(reason: reason) else {
            return .authenticated
        }
        return classify(errorCode: code)
    }
}

public struct LocalPresenceEvaluator: PresenceEvaluator {
    public init() {}

    public func hasGUISession() -> Bool {
        var sid: SecuritySessionId = 0
        var attrs: SessionAttributeBits = []
        guard SessionGetInfo(callerSecuritySession, &sid, &attrs) == errSessionSuccess else {
            return false
        }
        return attrs.contains(.sessionHasGraphicAccess)
    }

    public func canEvaluate() -> Int? {
        var error: NSError?
        if LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: &error) { return nil }
        return error?.code ?? LAError.notInteractive.rawValue
    }

    public func evaluate(reason: String) async -> Int? {
        let context = LAContext()
        return await withCheckedContinuation { continuation in
            context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { success, error in
                if success {
                    continuation.resume(returning: nil)
                } else {
                    continuation.resume(returning: (error as NSError?)?.code ?? LAError.authenticationFailed.rawValue)
                }
            }
        }
    }
}
