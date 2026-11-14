// Respond only to an explicitly matched Finder dialog and exact button title.
import Foundation
import AppKit
import ApplicationServices
func attr(_ e: AXUIElement, _ key: CFString) -> CFTypeRef? {
 var value: CFTypeRef?
 guard AXUIElementCopyAttributeValue(e,key,&value) == .success else { return nil }
 return value
}
func descendants(_ e: AXUIElement) -> [AXUIElement] {
 var found = [e]
 for child in attr(e,kAXChildrenAttribute as CFString) as? [AXUIElement] ?? [] { found += descendants(child) }
 return found
}
var args = CommandLine.arguments
var bundleID = "com.apple.finder"
if args.count == 4 && args[1] == "--security-agent" {
 bundleID = "com.apple.SecurityAgent"
 args.remove(at: 1)
}
if args.count != 3 { exit(2) }
guard let app = NSRunningApplication.runningApplications(withBundleIdentifier:bundleID).first else { exit(2) }
let ax = AXUIElementCreateApplication(app.processIdentifier)
AXUIElementSetMessagingTimeout(ax, 3)
for window in attr(ax,kAXWindowsAttribute as CFString) as? [AXUIElement] ?? [] {
 let elements = descendants(window)
 let texts = elements.flatMap { element in
  [kAXValueAttribute, kAXTitleAttribute, kAXDescriptionAttribute].compactMap {
   attr(element, $0 as CFString) as? String
  }
 }
 guard texts.contains(where: { $0.contains(args[1]) }) else { continue }
 let buttons = elements.filter {
  (attr($0,kAXRoleAttribute as CFString) as? String) == kAXButtonRole as String &&
  (attr($0,kAXTitleAttribute as CFString) as? String) == args[2]
 }
 guard buttons.count == 1 else { continue }
 guard AXUIElementPerformAction(buttons[0],kAXPressAction as CFString) == .success else { exit(2) }
 print("matched_dialog_action=\(args[2]) expected_text=\(args[1])")
 exit(0)
}
fputs("matching Finder dialog/button not found; no action\n",stderr)
exit(2)
