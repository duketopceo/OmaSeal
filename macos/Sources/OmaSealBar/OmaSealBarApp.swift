import AppKit
import SwiftUI
import OmaSealBarCore

@main
struct OmaSealBarApp: App {
    @StateObject private var model: MenuBarModel

    init() {
        let saved = UserDefaults.standard.string(forKey: SettingsKey.binaryPath) ?? ""
        let path = saved.isEmpty ? nil : saved
        let pasteboard = SystemPasteboard()
        let clipboard = ClipboardClearer(pasteboard: pasteboard, scheduler: DispatchScheduler())
        let model = MenuBarModel(
            cli: ProcessOmaSealCLI(binaryPath: path),
            authenticator: LocalAuthenticator(),
            clipboard: clipboard,
            binaryPath: path ?? resolveOmaSealBinary(explicit: path) ?? ""
        )
        _model = StateObject(wrappedValue: model)
    }

    var body: some Scene {
        MenuBarExtra("OmaSeal", systemImage: "key.fill") {
            MenuBarView(model: model)
                .frame(minWidth: 320, idealWidth: 360, minHeight: 280, idealHeight: 420)
        }
        .menuBarExtraStyle(.window)
    }
}

enum SettingsKey {
    static let binaryPath = "OmaSealBar.binaryPath"
}
