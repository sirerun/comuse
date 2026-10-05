import AppKit
import SwiftUI
import FixtureCore

@main
struct ComuseFixtureApp: App {
    private let configuration: FixtureConfiguration

    init() {
        do {
            configuration = try FixtureConfiguration.parse(arguments: Array(CommandLine.arguments.dropFirst()))
            NSApplication.shared.setActivationPolicy(.regular)
        } catch {
            fputs("ComuseFixture: launch requires --fixture-nonce with 1-64 ASCII letters, digits, dot, underscore, or hyphen\n", stderr)
            exit(64)
        }
    }

    var body: some Scene {
        Window(configuration.windowTitle, id: "comuse-fixture-window") {
            FixtureView()
                .background(WindowIdentity(title: configuration.windowTitle).frame(width: 0, height: 0))
        }
        .defaultSize(width: 560, height: 540)
        .windowResizability(.contentSize)
    }
}

private struct FixtureView: View {
    @State private var state = FixtureState()
    @State private var textFocusToken = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Synthetic accessibility fixture")
                .accessibilityIdentifier("label")
                .accessibilityLabel("Comuse synthetic fixture")

            HStack(spacing: 8) {
                Text("Counter: \(state.counter)")
                    .accessibilityIdentifier("counter-value")
                Button("Increment counter") { state.incrementCounter() }
                    .accessibilityIdentifier("buttoncounter")
            }

            FixtureField(
                value: Binding(get: { state.text }, set: { state.replaceText($0) }),
                identifier: "textfield",
                label: "Synthetic text field",
                placeholder: "Synthetic text",
                focusToken: textFocusToken,
                secure: false
            )
            .frame(height: 24)

            FixtureField(
                value: Binding(get: { state.secureText }, set: { state.replaceSecureText($0) }),
                identifier: "securefield",
                label: "Synthetic secure field",
                placeholder: "Synthetic secret",
                focusToken: 0,
                secure: true
            )
            .frame(height: 24)

            Button("Focus and externally edit text") {
                state.interfereWithEdit()
                textFocusToken += 1
            }
            .accessibilityIdentifier("focuseditinterference")

            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Scroll sentinel — synthetic row 0")
                    ForEach(1...30, id: \.self) { index in
                        Text("Synthetic scroll row \(index)")
                    }
                    Text("Scroll sentinel — synthetic row 31")
                        .accessibilityIdentifier("scrollsentinel")
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
            }
            .frame(height: 100)
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(.secondary))
            .accessibilityIdentifier("scrollcontainer")

            Text(state.delayedText)
                .accessibilityIdentifier("delayedlabel")
                .task {
                    guard state.delayedText != "Delayed state ready" else { return }
                    try? await Task.sleep(for: .milliseconds(500))
                    state.publishDelayedState()
                }

            if state.childVisible {
                HStack {
                    Text("Removable synthetic child")
                        .accessibilityIdentifier("removechild-target")
                    Button("Remove child") { state.removeChild() }
                        .accessibilityIdentifier("removechild")
                }
            } else {
                Text("Synthetic child removed")
                    .accessibilityIdentifier("removechild-result")
            }

            Spacer(minLength: 0)
        }
        .padding(18)
        .frame(minWidth: 520, minHeight: 500)
    }
}

private struct FixtureField: NSViewRepresentable {
    @Binding var value: String
    let identifier: String
    let label: String
    let placeholder: String
    let focusToken: Int
    let secure: Bool

    func makeCoordinator() -> Coordinator {
        Coordinator(value: $value)
    }

    func makeNSView(context: Context) -> NSTextField {
        let field: NSTextField = secure ? NSSecureTextField() : NSTextField()
        field.isEditable = true
        field.isSelectable = true
        field.isBordered = true
        field.isBezeled = true
        field.drawsBackground = true
        field.placeholderString = placeholder
        field.identifier = NSUserInterfaceItemIdentifier(identifier)
        field.setAccessibilityIdentifier(identifier)
        field.setAccessibilityLabel(label)
        field.delegate = context.coordinator
        field.stringValue = value
        context.coordinator.lastFocusToken = focusToken
        return field
    }

    func updateNSView(_ field: NSTextField, context: Context) {
        context.coordinator.value = $value
        if field.stringValue != value {
            field.stringValue = value
        }
        if context.coordinator.lastFocusToken != focusToken {
            context.coordinator.lastFocusToken = focusToken
            DispatchQueue.main.async {
                field.window?.makeFirstResponder(field)
            }
        }
    }

    final class Coordinator: NSObject, NSTextFieldDelegate {
        var value: Binding<String>
        var lastFocusToken: Int

        init(value: Binding<String>) {
            self.value = value
            self.lastFocusToken = 0
        }

        func controlTextDidChange(_ notification: Notification) {
            guard let field = notification.object as? NSTextField else { return }
            value.wrappedValue = field.stringValue
        }
    }
}

private struct WindowIdentity: NSViewRepresentable {
    let title: String

    func makeNSView(context: Context) -> IdentityView { IdentityView(title: title) }
    func updateNSView(_ view: IdentityView, context: Context) { view.applyIdentity() }

    final class IdentityView: NSView {
        let title: String
        init(title: String) {
            self.title = title
            super.init(frame: .zero)
        }
        required init?(coder: NSCoder) { nil }
        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            applyIdentity()
        }
        func applyIdentity() {
            window?.title = title
            window?.identifier = NSUserInterfaceItemIdentifier("comuse-fixture-window")
        }
    }
}
