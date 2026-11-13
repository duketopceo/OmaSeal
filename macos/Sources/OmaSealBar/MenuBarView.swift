import SwiftUI
import OmaSealBarCore

struct MenuBarView: View {
    @ObservedObject var model: MenuBarModel

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("OmaSeal")
                .font(.headline)
                .padding(.top, 4)

            TextField("Search", text: $model.query)
                .textFieldStyle(.roundedBorder)
                .accessibilityLabel("Search secrets")

            statusLine

            if let loadError = model.loadError {
                Text(loadError)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .textSelection(.disabled)
            }

            if model.isLoading && model.items.isEmpty {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if model.visibleItems.isEmpty {
                Text(model.items.isEmpty ? "No secrets" : "No matches")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(model.visibleItems) { item in
                    SecretRow(item: item, model: model)
                }
                .listStyle(.plain)
            }

            Divider()
            HStack {
                Text(model.binaryPath.isEmpty ? "omaseal not found on PATH" : model.binaryPath)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                Button("Refresh") {
                    Task { await model.refresh() }
                }
                .controlSize(.small)
            }
        }
        .padding(12)
        .task { await model.refresh() }
        .onDisappear { model.forgetSecrets() }
    }

    private var statusLine: some View {
        Text(model.status.line)
            .font(.caption)
            .foregroundStyle(.secondary)
            .accessibilityLabel("Replica status")
    }
}

struct SecretRow: View {
    let item: SecretItem
    @ObservedObject var model: MenuBarModel

    private var isRevealed: Bool {
        model.revealed?.item == item
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                VStack(alignment: .leading, spacing: 1) {
                    Text(item.service)
                        .font(.body)
                    Text(item.account + (item.label.isEmpty ? "" : " · \(item.label)"))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("Reveal") {
                    Task { await model.reveal(item) }
                }
                .controlSize(.small)
                .disabled(isRevealed)
                Button("Copy") {
                    Task { await model.copy(item) }
                }
                .controlSize(.small)
            }
            if isRevealed, let value = model.revealed?.value {
                Text(value)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(3)
            }
        }
        .padding(.vertical, 2)
    }
}
