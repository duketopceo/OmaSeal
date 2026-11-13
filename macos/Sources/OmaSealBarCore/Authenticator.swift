import Foundation
import LocalAuthentication

public protocol Authenticator: Sendable {
    /// Touch ID, falling back to the device password when biometrics are unavailable.
    func authenticate(reason: String) async -> Bool
}

public struct LocalAuthenticator: Authenticator, Sendable {
    public init() {}

    public func authenticate(reason: String) async -> Bool {
        let context = LAContext()
        var error: NSError?
        // deviceOwnerAuthentication: Touch ID or the device password.
        guard context.canEvaluatePolicy(.deviceOwnerAuthentication, error: &error) else {
            return false
        }
        return await withCheckedContinuation { continuation in
            context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { success, _ in
                continuation.resume(returning: success)
            }
        }
    }
}
