import Foundation
import AppKit
import CoreGraphics
import ApplicationServices

// TboxRclone Finder drag helper: 2026-09-20.2 (verified Finder copy; see sequential-001 evidence)

// Send one guarded drag between two exact Finder window titles.
// Usage: finder-drag.swift SOURCE_TITLE TARGET_TITLE FILENAME
//    or: finder-drag.swift SOURCE_TITLE TARGET_TITLE START_X START_Y END_X END_Y
// Run this from an authorized Terminal/Codex Computer Use process. A Swift
// child launched by a restricted shell may not receive Screen Recording or
// Accessibility visibility for Finder windows.
setbuf(stdout, nil)
let args = CommandLine.arguments
guard args.count == 4 || args.count == 7 else {
    fatalError("usage: finder-drag.swift SOURCE_TITLE TARGET_TITLE FILENAME [or START_X START_Y END_X END_Y]")
}
let sourceTitle = args[1]
let targetTitle = args[2]
let filename = args.count == 4 ? args[3] : nil
let itemLabel = filename ?? "coordinates"
print("finder_drag_start source=\(sourceTitle) target=\(targetTitle) item=\(itemLabel)")
guard !sourceTitle.isEmpty, !targetTitle.isEmpty, sourceTitle != targetTitle else {
    fatalError("source and target Finder titles must be distinct")
}

// Avoid AppleScript/System Events and NSRunningApplication here. Those services
// intermittently return -1728/Connection invalid, and TCC can hide Finder from
// process enumeration even while its windows are visible.

func axValue<T>(_ element: AXUIElement, _ attribute: CFString, _ type: AXValueType, _ value: inout T) -> Bool {
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute, &raw) == .success,
          let raw, CFGetTypeID(raw) == AXValueGetTypeID() else { return false }
    return AXValueGetValue(raw as! AXValue, type, &value)
}

func matchingWindow(_ title: String) -> (id: Int, bounds: CGRect, ax: AXUIElement)? {
    let windows = CGWindowListCopyWindowInfo(.optionAll, kCGNullWindowID) as? [[String: Any]] ?? []
    for window in windows {
        guard let ownerName = window[kCGWindowOwnerName as String] as? String,
              ownerName == "Finder" || ownerName == "访达",
              let finderPID = window[kCGWindowOwnerPID as String] as? Int,
              let candidateTitle = window[kCGWindowName as String] as? String, candidateTitle == title,
              let id = window[kCGWindowNumber as String] as? Int,
              let raw = window[kCGWindowBounds as String] as? [String: Any],
              let x = raw["X"] as? CGFloat, let y = raw["Y"] as? CGFloat,
              let width = raw["Width"] as? CGFloat, let height = raw["Height"] as? CGFloat else { continue }
        let axApp = AXUIElementCreateApplication(pid_t(finderPID))
        var axWindows: CFTypeRef?
        guard AXUIElementCopyAttributeValue(axApp, kAXWindowsAttribute as CFString, &axWindows) == .success,
              let windows = axWindows as? [AXUIElement] else { continue }
        for axWindow in windows {
            var axTitle: CFTypeRef?
            if AXUIElementCopyAttributeValue(axWindow, kAXTitleAttribute as CFString, &axTitle) == .success,
               (axTitle as? String) == title {
                return (id, CGRect(x: x, y: y, width: width, height: height), axWindow)
            }
        }
    }
    return nil
}

func findFileRow(_ element: AXUIElement, _ wanted: String) -> CGRect? {
    var role: CFTypeRef?
    _ = AXUIElementCopyAttributeValue(element, kAXRoleAttribute as CFString, &role)
    if (role as? String) == kAXRowRole as String {
        var descendants: [AXUIElement] = [element]
        while let current = descendants.popLast() {
            var value: CFTypeRef?
            _ = AXUIElementCopyAttributeValue(current, kAXValueAttribute as CFString, &value)
            if (value as? String) == wanted {
                var position = CGPoint.zero
                var size = CGSize.zero
                if axValue(current, kAXPositionAttribute as CFString, .cgPoint, &position),
                   axValue(current, kAXSizeAttribute as CFString, .cgSize, &size) {
                    return CGRect(origin: position, size: size)
                }
            }
            var children: CFTypeRef?
            if AXUIElementCopyAttributeValue(current, kAXChildrenAttribute as CFString, &children) == .success,
               let children = children as? [AXUIElement] { descendants.append(contentsOf: children) }
        }
    }
    var children: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &children) == .success,
          let children = children as? [AXUIElement] else { return nil }
    for child in children {
        if let result = findFileRow(child, wanted) { return result }
    }
    return nil
}

guard let source = matchingWindow(sourceTitle), let target = matchingWindow(targetTitle) else {
    let visible = (CGWindowListCopyWindowInfo(.optionAll, kCGNullWindowID) as? [[String: Any]] ?? [])
        .compactMap { window -> String? in
            guard let ownerName = window[kCGWindowOwnerName as String] as? String,
                  ownerName == "Finder" || ownerName == "访达" else { return nil }
            let owner = window[kCGWindowOwnerPID as String] as? Int ?? -1
            let title = window[kCGWindowName as String] as? String ?? "<untitled>"
            return "pid=\(owner) title=\(title)"
        }
        .joined(separator: "; ")
    fatalError("both exact Finder windows must be visible; visible Finder windows: \(visible)")
}
print("finder_windows_found")
guard source.bounds.width > 0, source.bounds.height > 0,
      target.bounds.width > 0, target.bounds.height > 0 else {
    fatalError("Finder window has invalid bounds")
}
print("source_window=\(source.id) target_window=\(target.id)")
var finderPID: pid_t = 0
guard AXUIElementGetPid(source.ax, &finderPID) == .success else {
    fatalError("cannot determine Finder process")
}
let finderApp = AXUIElementCreateApplication(finderPID)
guard AXUIElementSetAttributeValue(finderApp, kAXFrontmostAttribute as CFString, kCFBooleanTrue) == .success,
      AXUIElementPerformAction(target.ax, kAXRaiseAction as CFString) == .success,
      AXUIElementPerformAction(source.ax, kAXRaiseAction as CFString) == .success else {
    fatalError("cannot bring both Finder windows to the front")
}
Thread.sleep(forTimeInterval: 0.4)

func topWindow(at point: CGPoint) -> Int? {
    let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    for window in windows {
        guard (window[kCGWindowLayer as String] as? Int) == 0,
              (window[kCGWindowAlpha as String] as? Double ?? 1) > 0,
              let raw = window[kCGWindowBounds as String] as? [String: Any],
              let bounds = CGRect(dictionaryRepresentation: raw as CFDictionary),
              bounds.contains(point) else { continue }
        return window[kCGWindowNumber as String] as? Int
    }
    return nil
}


func directoryUIReady(_ window: AXUIElement) -> Bool {
    var queue = [window]
    var hasList = false
    while let element = queue.popLast() {
        var busy: CFTypeRef?
        if AXUIElementCopyAttributeValue(element, "AXElementBusy" as CFString, &busy) == .success,
           (busy as? Bool) == true { return false }
        var role: CFTypeRef?
        _ = AXUIElementCopyAttributeValue(element, kAXRoleAttribute as CFString, &role)
        if (role as? String) == kAXProgressIndicatorRole as String { return false }
        var identifier: CFTypeRef?
        _ = AXUIElementCopyAttributeValue(element, "AXIdentifier" as CFString, &identifier)
        if (identifier as? String) == "ListView" { hasList = true }
        var children: CFTypeRef?
        if AXUIElementCopyAttributeValue(element, kAXChildrenAttribute as CFString, &children) == .success,
           let children = children as? [AXUIElement] { queue.append(contentsOf: children) }
    }
    return hasList
}
guard directoryUIReady(source.ax), directoryUIReady(target.ax) else {
    fputs("finder_not_ready: list unavailable or loading indicator present; no drag sent\n", stderr)
    exit(2)
}
print("finder_lists_ready_no_busy_indicator")

let start: CGPoint
let end: CGPoint
if let filename {
    guard let row = findFileRow(source.ax, filename) else { fatalError("file row not found through Finder Accessibility") }
    print("finder_source_row_found")
    start = CGPoint(x: row.minX - 10, y: row.midY)
    end = CGPoint(x: target.bounds.midX + target.bounds.width * 0.15, y: target.bounds.midY + 40)
} else {
    guard let sx = Double(args[3]), let sy = Double(args[4]), let ex = Double(args[5]), let ey = Double(args[6]) else {
        fatalError("drag coordinates must be numeric")
    }
    start = CGPoint(x: sx, y: sy)
    end = CGPoint(x: ex, y: ey)
}
print("drag_coordinates start=\(start) end=\(end)")
let eventSource = CGEventSource(stateID: .hidSystemState)
eventSource?.localEventsSuppressionInterval = 0
var mouseNumber: Int64 = 1
func post(_ type: CGEventType, _ point: CGPoint) {
    guard let event = CGEvent(mouseEventSource: eventSource, mouseType: type, mouseCursorPosition: point, mouseButton: .left) else {
        fatalError("could not create mouse event")
    }
    event.setIntegerValueField(.mouseEventNumber, value: mouseNumber)
    event.setDoubleValueField(.mouseEventPressure, value: type == .leftMouseUp ? 0 : 1)
    if type == .leftMouseDown {
        event.setIntegerValueField(.mouseEventClickState, value: 1)
    } else if type == .leftMouseDragged {
        event.setIntegerValueField(.mouseEventClickState, value: 1)
    } else if type == .leftMouseUp {
        event.setIntegerValueField(.mouseEventClickState, value: 1)
    }
    event.post(tap: .cgSessionEventTap)
}
func topWindowMatches(_ title: String, at point: CGPoint) -> Bool {
    guard let id = topWindow(at: point),
          let windows = CGWindowListCopyWindowInfo(.optionIncludingWindow, CGWindowID(id)) as? [[String: Any]],
          let window = windows.first else { return false }
    return (window[kCGWindowName as String] as? String) == title
        && (window[kCGWindowOwnerPID as String] as? Int) == Int(finderPID)
}

func finderReceivesHit(at point: CGPoint) -> Bool {
    var element: AXUIElement?
    guard AXUIElementCopyElementAtPosition(AXUIElementCreateSystemWide(), Float(point.x), Float(point.y), &element) == .success,
          let element else { return false }
    var pid: pid_t = 0
    return AXUIElementGetPid(element, &pid) == .success && pid == finderPID
}
// Dock can expose a screen-sized layer-20 overlay. Check normal window order
// plus the actual accessibility hit target, rather than treating it as opaque.
print("visibility_check source_expected=\(source.id) source_top=\(topWindow(at: start) ?? -1) source_hit=\(finderReceivesHit(at: start)) target_expected=\(target.id) target_top=\(topWindow(at: end) ?? -1) target_hit=\(finderReceivesHit(at: end))")
guard finderReceivesHit(at: start), finderReceivesHit(at: end),
      source.bounds.contains(start), target.bounds.contains(end),
      topWindowMatches(sourceTitle, at: start), topWindowMatches(targetTitle, at: end) else {
    fatalError("drag endpoints are obscured or not on the active desktop; no mouse events sent")
}
print("finder_drag_endpoints_visible")
if ProcessInfo.processInfo.environment["TBOX_FINDER_CHECK_ONLY"] == "1" { exit(0) }
post(.mouseMoved, start)
print("finder_drag_events_begin")
Thread.sleep(forTimeInterval: 0.2)
post(.leftMouseDown, start)
post(.leftMouseUp, start)
mouseNumber += 1
Thread.sleep(forTimeInterval: 0.6)
post(.leftMouseDown, start)
Thread.sleep(forTimeInterval: 0.08)
for step in 1...40 {
    let fraction = Double(step) / 40.0
    post(.leftMouseDragged, CGPoint(x: start.x + (end.x - start.x) * fraction,
                                    y: start.y + (end.y - start.y) * fraction))
    Thread.sleep(forTimeInterval: 0.03)
}
post(.leftMouseUp, end)
print("drag_delivered")
