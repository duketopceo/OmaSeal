import Foundation
import OmaSealBarCore

// omaseal-presence --reason <text>
// Exit 0 = user authenticated, 1 = denied/cancelled/failed, 2 = unavailable.
// Prints nothing to stdout; at most one non-secret line to stderr.

func finish(_ outcome: PresenceOutcome) -> Never {
    if let m = outcome.message {
        FileHandle.standardError.write(Data(("omaseal-presence: " + m + "\n").utf8))
    }
    exit(outcome.exitCode)
}

guard let reason = PresenceCheck.parseReason(Array(CommandLine.arguments.dropFirst())) else {
    finish(.unavailable("usage: omaseal-presence --reason <text>"))
}

let semaphore = DispatchSemaphore(value: 0)
nonisolated(unsafe) var outcome: PresenceOutcome = .unavailable("no result")
Task {
    outcome = await PresenceCheck.run(reason: reason, evaluator: LocalPresenceEvaluator())
    semaphore.signal()
}
semaphore.wait()
finish(outcome)
