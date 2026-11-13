import Combine
import Foundation

/// In-memory session for the menu bar popover. Secret values live only here
/// and are dropped by `forgetSecrets()` when the popover closes. They are
/// never written to disk or logged.
@MainActor
public final class MenuBarModel: ObservableObject {
    @Published public var items: [SecretItem] = []
    @Published public var query: String = ""
    @Published public var loadError: String?
    @Published public var isLoading: Bool = false
    @Published public var revealed: RevealedSecret?
    @Published public var status: ReplicaStatusPresentation = .notAReplica
    @Published public var binaryPath: String

    public struct RevealedSecret: Equatable {
        public var item: SecretItem
        public var value: String
    }

    public let cli: OmaSealCLI
    public let authenticator: Authenticator
    public let clipboard: ClipboardClearer
    public var statusURL: URL
    private let fileManager: FileManager

    public var visibleItems: [SecretItem] {
        filterItems(items, query: query)
    }

    public init(
        cli: OmaSealCLI,
        authenticator: Authenticator,
        clipboard: ClipboardClearer,
        binaryPath: String = "",
        statusURL: URL = ReplicaStatusFile.defaultPath(),
        fileManager: FileManager = .default
    ) {
        self.cli = cli
        self.authenticator = authenticator
        self.clipboard = clipboard
        self.binaryPath = binaryPath
        self.statusURL = statusURL
        self.fileManager = fileManager
    }

    public func refresh() async {
        isLoading = true
        loadError = nil
        defer { isLoading = false }
        refreshStatus()
        do {
            items = try await cli.list()
        } catch {
            items = []
            loadError = error.localizedDescription
        }
    }

    public func refreshStatus() {
        status = ReplicaStatusFile.load(from: statusURL, fileManager: fileManager)
    }

    /// Reveal is presence-gated. A denied or failed prompt does not call `get`.
    public func reveal(_ item: SecretItem) async {
        let allowed = await authenticator.authenticate(reason: "Reveal the \(item.service)/\(item.account) secret")
        guard allowed else { return }
        do {
            let value = try await cli.get(service: item.service, account: item.account)
            revealed = RevealedSecret(item: item, value: value)
        } catch {
            loadError = error.localizedDescription
        }
    }

    /// Copy is presence-gated unless this item is already revealed in memory.
    public func copy(_ item: SecretItem) async {
        if let revealed, revealed.item == item {
            clipboard.copy(revealed.value)
            return
        }
        let allowed = await authenticator.authenticate(reason: "Copy the \(item.service)/\(item.account) secret")
        guard allowed else { return }
        do {
            let value = try await cli.get(service: item.service, account: item.account)
            clipboard.copy(value)
        } catch {
            loadError = error.localizedDescription
        }
    }

    public func forgetSecrets() {
        revealed = nil
    }
}
