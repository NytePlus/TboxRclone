// Read progress, matching dialogs and target-window readiness in one AX traversal.
import Foundation
import AppKit
import ApplicationServices
let args = CommandLine.arguments
guard (args.count == 3 || args.count == 4), !args[1].isEmpty else { exit(2) }
let expectedName = args.count == 4 ? args[3] : nil
let marker = args[1]
let targetTitle = URL(fileURLWithPath: args[2]).lastPathComponent
let start = Date()
let keys = [kAXRoleAttribute, kAXTitleAttribute, kAXValueAttribute,
            kAXDescriptionAttribute, kAXChildrenAttribute] as CFArray
var transfers = [[String: Any]]()
var dialogs = [[String: Any]]()
var targetVisible = false
var incomplete = false
var foundFinder = false
for bundle in ["com.apple.finder", "com.apple.SecurityAgent"] {
    guard let app = NSRunningApplication.runningApplications(withBundleIdentifier:bundle).first else { continue }
    if bundle == "com.apple.finder" { foundFinder = true }
    let root = AXUIElementCreateApplication(app.processIdentifier)
    AXUIElementSetMessagingTimeout(root, 2)
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(root,kAXWindowsAttribute as CFString,&raw) == .success,
          let windows = raw as? [AXUIElement] else { incomplete = true; continue }
    for window in windows {
        var title: CFTypeRef?
        var mainWindow: CFTypeRef?
        let isMain = AXUIElementCopyAttributeValue(window,kAXMainAttribute as CFString,&mainWindow) == .success && (mainWindow as? Bool == true)
        var isTarget = false
        if AXUIElementCopyAttributeValue(window,kAXTitleAttribute as CFString,&title) == .success,
           bundle == "com.apple.finder", title as? String == targetTitle, isMain { isTarget = true }
        var queue = [window]
        var texts = [String](), staticTexts = [String](), buttons = [String]()
        var progress = 0, nodes = 0
        while let element = queue.popLast() {
            nodes += 1
            if nodes > 20000 { incomplete = true; break }
            var values: CFArray?
            guard AXUIElementCopyMultipleAttributeValues(element,keys,[],&values) == .success,
                  let fields = values as? [Any], fields.count == 5 else { incomplete = true; continue }
            let role = fields[0] as? String ?? ""
            let strings = fields[1...3].compactMap { $0 as? String }
            texts.append(contentsOf:strings)
            if role == kAXProgressIndicatorRole as String { progress += 1 }
            if role == kAXStaticTextRole as String, let text = fields[2] as? String { staticTexts.append(text) }
            if role == kAXButtonRole as String, let text = fields[1] as? String { buttons.append(text) }
            if let children = fields[4] as? [AXUIElement] { queue.append(contentsOf:children) }
        }
        if isTarget && (expectedName == nil || texts.contains(expectedName!)) {
            targetVisible = true
        }
        if progress > 0 && (texts.contains("停止进程") || texts.contains("Stop process")) {
            transfers.append(["texts":texts,"progress_indicators":progress])
        }
        if staticTexts.contains(where: { $0.contains(marker) }),
           buttons.contains(where: { ["好","取消","OK","Cancel"].contains($0) }) {
            dialogs.append(["app":bundle,"texts":staticTexts,"buttons":buttons])
        }
    }
}
let end = Date()
let result: [String: Any] = ["scan_started_at":ISO8601DateFormatter().string(from:start),
    "observed_at":ISO8601DateFormatter().string(from:end),"scan_seconds":end.timeIntervalSince(start),
    "transfer_windows":transfers,"dialogs":dialogs,"incomplete":incomplete || !foundFinder,
    "view_ready":targetVisible && NSWorkspace.shared.frontmostApplication?.bundleIdentifier == "com.apple.finder",
    "visual_review_required":true]
FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject:result,options:[.prettyPrinted,.sortedKeys]))
print("")
exit(incomplete || !foundFinder ? 2 : 0)
