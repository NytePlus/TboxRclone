// Open and arrange two test folders without resolving unrelated Finder windows.
import Foundation
import AppKit
import ApplicationServices

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(2)
}
func attribute(_ element: AXUIElement, _ name: CFString) -> AnyObject? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, name, &value) == .success else { return nil }
    return value
}
let args = CommandLine.arguments
if args.count != 3 { fail("usage: open.swift SOURCE_DIRECTORY TARGET_DIRECTORY") }
let urls = args.dropFirst().map { URL(fileURLWithPath: $0).standardizedFileURL }
if urls[0] == urls[1] || urls[0].lastPathComponent == urls[1].lastPathComponent {
    fail("test folders must have distinct paths and window titles")
}
guard let finder = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.finder").first,
      let screen = NSScreen.main else { fail("Finder or display unavailable") }
let app = AXUIElementCreateApplication(finder.processIdentifier)
AXUIElementSetMessagingTimeout(app, 3)
for (index, url) in urls.enumerated() {
    guard NSWorkspace.shared.open(url) else { fail("Finder refused test folder") }
    // Reveal the exact fixture through Finder so rows outside the current
    // viewport become available to the accessibility drag helper.
    if index == 0, let name = ProcessInfo.processInfo.environment["TBOX_FINDER_SELECT_FILENAME"] {
        guard !name.isEmpty, name != ".", name != "..",
              URL(fileURLWithPath: name).lastPathComponent == name else { fail("invalid fixture basename") }
        NSWorkspace.shared.activateFileViewerSelecting([url.appendingPathComponent(name)])
    }
    let deadline = Date().addingTimeInterval(10)
    var found: AXUIElement?
    while Date() < deadline {
        let windows = attribute(app, kAXWindowsAttribute as CFString) as? [AXUIElement] ?? []
        let matches = windows.filter { (attribute($0, kAXTitleAttribute as CFString) as? String) == url.lastPathComponent }
        // Previous interrupted setup may have opened duplicates of these
        // explicitly supplied test folders. Close only duplicate test titles.
        for duplicate in matches.dropFirst() {
            guard let close = attribute(duplicate, kAXCloseButtonAttribute as CFString),
                  AXUIElementPerformAction(close as! AXUIElement, kAXPressAction as CFString) == .success else {
                fail("cannot close duplicate test window")
            }
        }
        if let window = matches.first { found = window; break }
        Thread.sleep(forTimeInterval: 0.1)
    }
    guard let window = found else { fail("test window did not appear") }
    var position = CGPoint(x: screen.frame.minX + CGFloat(index) * screen.frame.width/2, y: 60)
    var size = CGSize(width: screen.frame.width/2, height: screen.frame.height-120)
    guard let p = AXValueCreate(.cgPoint, &position), let s = AXValueCreate(.cgSize, &size),
          AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, p) == .success,
          AXUIElementSetAttributeValue(window, kAXSizeAttribute as CFString, s) == .success,
          AXUIElementPerformAction(window, kAXRaiseAction as CFString) == .success else { fail("cannot arrange test window") }
    // Cmd+2 selects Finder list view for the drag helper's row lookup.
    _ = AXUIElementSetAttributeValue(app, kAXFrontmostAttribute as CFString, kCFBooleanTrue)
    for down in [true, false] {
        guard let event = CGEvent(keyboardEventSource: nil, virtualKey: 19, keyDown: down) else { fail("cannot set Finder view") }
        event.flags = down ? .maskCommand : []
        event.post(tap: .cgSessionEventTap)
    }
}
print("finder_windows_opened via=workspace-and-accessibility")
