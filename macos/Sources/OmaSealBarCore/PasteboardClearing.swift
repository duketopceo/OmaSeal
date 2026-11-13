import Foundation
#if canImport(AppKit)
import AppKit
#endif

public protocol Pasteboard: AnyObject {
    var string: String? { get set }
}

public protocol Scheduler: Sendable {
    func schedule(after interval: TimeInterval, _ work: @escaping @Sendable () -> Void)
}

#if canImport(AppKit)
public final class SystemPasteboard: Pasteboard {
    public init() {}
    public var string: String? {
        get { NSPasteboard.general.string(forType: .string) }
        set {
            let board = NSPasteboard.general
            board.clearContents()
            if let newValue {
                board.setString(newValue, forType: .string)
            }
        }
    }
}
#endif

public struct DispatchScheduler: Scheduler, Sendable {
    public init() {}
    public func schedule(after interval: TimeInterval, _ work: @escaping @Sendable () -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + interval, execute: work)
    }
}

/// Copies a secret to the pasteboard and clears it after `interval` if the
/// pasteboard still holds that exact value. The secret is not written to disk
/// and is not logged.
public final class ClipboardClearer: @unchecked Sendable {
    public static let defaultInterval: TimeInterval = 30

    private let pasteboard: Pasteboard
    private let scheduler: Scheduler
    private let interval: TimeInterval
    private var generation: UInt64 = 0

    public init(pasteboard: Pasteboard, scheduler: Scheduler, interval: TimeInterval = ClipboardClearer.defaultInterval) {
        self.pasteboard = pasteboard
        self.scheduler = scheduler
        self.interval = interval
    }

    public func copy(_ value: String) {
        pasteboard.string = value
        generation += 1
        let token = generation
        // Capture only a hash for the later comparison input? We must compare
        // the actual string to know whether to clear. Keep it in the scheduled
        // closure only; do not log it.
        scheduler.schedule(after: interval) { [weak self] in
            guard let self else { return }
            // A newer copy resets the timer; ignore stale callbacks.
            guard token == self.generation else { return }
            if self.pasteboard.string == value {
                self.pasteboard.string = nil
            }
        }
    }
}
