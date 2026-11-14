// Native Finder deletion of the explicitly disposable ST-017 fixture only.
import Foundation
import AppKit
import ApplicationServices

func fail(_ text: String) -> Never { fputs(text + "\n", stderr); exit(2) }
func attr(_ element: AXUIElement, _ key: String) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, key as CFString, &value) == .success else { return nil }
    return value
}
func children(_ element: AXUIElement) -> [AXUIElement] {
    return attr(element, "AXChildren") as? [AXUIElement] ?? []
}
func descendants(_ element: AXUIElement) -> [AXUIElement] {
    return [element] + children(element).flatMap { descendants($0) }
}
let target = URL(fileURLWithPath: "/private/tmp/tboxrclone-webdav-501/DeleteCases/delete-me.txt")
guard FileManager.default.fileExists(atPath: target.path),
      let app = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.finder").first else { fail("fixture or Finder unavailable") }
NSWorkspace.shared.activateFileViewerSelecting([target])
let ax = AXUIElementCreateApplication(app.processIdentifier)
AXUIElementSetMessagingTimeout(ax, 3)
let deadline = Date().addingTimeInterval(10)
var selectedWindow: AXUIElement?
while Date() < deadline {
    for window in attr(ax, "AXWindows") as? [AXUIElement] ?? [] {
        guard (attr(window, "AXTitle") as? String) == "DeleteCases" else { continue }
        for list in descendants(window) where (attr(list, "AXIdentifier") as? String) == "ListView" {
            guard let rows = attr(list, "AXSelectedRows") as? [AXUIElement], rows.count == 1 else { continue }
            let values = descendants(rows[0])
            let correctURL = values.contains { element in
                if let url = attr(element, "AXURL") as? URL { return (url as NSURL).filePathURL?.resolvingSymlinksInPath() == target.resolvingSymlinksInPath() }
                if let text = attr(element, "AXURL") as? String, let url = URL(string: text) { return (url as NSURL).filePathURL?.resolvingSymlinksInPath() == target.resolvingSymlinksInPath() }
                return false
            }
            if correctURL && values.contains(where: { (attr($0, "AXValue") as? String) == "delete-me.txt" }) {
                selectedWindow = window
            }
        }
    }
    if selectedWindow != nil { break }
    Thread.sleep(forTimeInterval: 0.1)
}
guard let window = selectedWindow,
      AXUIElementPerformAction(window, kAXRaiseAction as CFString) == .success,
      AXUIElementSetAttributeValue(ax, kAXFrontmostAttribute as CFString, kCFBooleanTrue) == .success else {
    for window in attr(ax, "AXWindows") as? [AXUIElement] ?? [] where (attr(window, "AXTitle") as? String) == "DeleteCases" {
        for element in descendants(window) {
            if let value = attr(element, "AXValue") as? String, value == "delete-me.txt" {
                print("diagnostic role=\(String(describing: attr(element, "AXRole"))) url=\(String(describing: attr(element, "AXURL")))")
                if let url = attr(element, "AXURL") as? URL { print("resolved=\(String(describing: (url as NSURL).filePathURL)) expected=\(target)") }
            }
        }
    }
    fail("exact mounted fixture not uniquely selected; no deletion sent")
}
guard (attr(window, "AXMain") as? Bool) == true else { fail("target window is not main") }
for down in [true, false] {
    guard let event = CGEvent(keyboardEventSource: nil, virtualKey: 51, keyDown: down) else { fail("cannot create key event") }
    event.flags = down ? .maskCommand : []
    event.post(tap: .cgSessionEventTap)
}
print("Finder Command-Delete sent after exact selected-row URL check: \(target.path)")
print("Confirmation and independent deletion verification still required")
