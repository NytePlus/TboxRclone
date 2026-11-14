// Inspect dialogs matching a test marker; optionally raise them for recording.
import Foundation
import AppKit
import ApplicationServices

func attribute(_ element: AXUIElement, _ key: CFString) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, key, &value) == .success else { return nil }
    return value
}
func descendants(_ element: AXUIElement) -> [AXUIElement] {
    var result = [element]
    for child in attribute(element, kAXChildrenAttribute as CFString) as? [AXUIElement] ?? [] {
        result += descendants(child)
    }
    return result
}
func inspect(marker: String, raise: Bool) -> [[String: Any]] {
    var dialogs = [[String: Any]]()
    for bundle in ["com.apple.finder", "com.apple.SecurityAgent"] {
        guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: bundle).first else { continue }
        let ax = AXUIElementCreateApplication(app.processIdentifier)
        AXUIElementSetMessagingTimeout(ax, 3)
        for window in attribute(ax, kAXWindowsAttribute as CFString) as? [AXUIElement] ?? [] {
            let elements = descendants(window)
            let texts = elements.filter {
                (attribute($0, kAXRoleAttribute as CFString) as? String) == kAXStaticTextRole as String
            }.compactMap { attribute($0, kAXValueAttribute as CFString) as? String }
            guard texts.contains(where: { $0.contains(marker) }) else { continue }
            let buttons = elements.filter {
                (attribute($0, kAXRoleAttribute as CFString) as? String) == kAXButtonRole as String
            }.compactMap { attribute($0, kAXTitleAttribute as CFString) as? String }
            guard buttons.contains(where: { ["好", "取消", "OK", "Cancel"].contains($0) }) else { continue }
            if raise {
                app.activate(options: [.activateAllWindows])
                guard AXUIElementPerformAction(window, kAXRaiseAction as CFString) == .success else { exit(2) }
            }
            dialogs.append(["app": bundle, "texts": texts, "buttons": buttons])
        }
    }

    return dialogs
}

var args = Array(CommandLine.arguments.dropFirst())
if args.first == "--watch" {
    args.removeFirst()
    guard args.count == 5, !args[0].isEmpty, !args[1].isEmpty,
          let limit = Double(args[4]), limit > 0, limit <= 600 else { exit(2) }
    let started = Date()
    while Date().timeIntervalSince(started) < limit {
        let matches = inspect(marker: args[0], raise: false).filter { dialog in
            (dialog["texts"] as? [String] ?? []).contains { $0.contains(args[0]) && $0.contains(args[1]) }
        }
        if !matches.isEmpty {
            let result: [String: Any] = ["observed_at": ISO8601DateFormatter().string(from: Date()),
                "marker": args[0], "expected_text": args[1], "dialogs": matches,
                "detection_seconds": Date().timeIntervalSince(started), "outcome": "expected_error_detected",
                "ui_verdict": "REQUIRES_SEPARATE_REVIEW"]
            let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys])
            try data.write(to: URL(fileURLWithPath: args[2]), options: .atomic)
            try Data("expected_error_detected\n".utf8).write(to: URL(fileURLWithPath: args[3]), options: .atomic)
            exit(0)
        }
        Thread.sleep(forTimeInterval: 0.5)
    }
    exit(3)
}
let raise = args.first == "--raise"
if raise { args.removeFirst() }
guard args.count == 1, !args[0].isEmpty else { exit(2) }
let marker = args[0]
let dialogs = inspect(marker: marker, raise: raise)
let result: [String: Any] = ["observed_at": ISO8601DateFormatter().string(from: Date()), "marker": marker, "dialogs": dialogs]
let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys])
FileHandle.standardOutput.write(data)
print("")
