import Foundation
import AppKit
import CoreGraphics
import ApplicationServices

struct BrowseCase: Decodable {
    let path: String
    let expectedCount: Int
    let expectedLast: String?
    enum CodingKeys: String, CodingKey {
        case path
        case expectedCount = "expected_count"
        case expectedLast = "expected_last"
    }
}

struct BrowseSpec: Decodable {
    let timeoutSeconds: Double
    let cases: [BrowseCase]
    enum CodingKeys: String, CodingKey {
        case timeoutSeconds = "timeout_seconds"
        case cases
    }
}

struct CaseResult: Codable {
    let path: String
    let expectedCount: Int
    let observedCount: Int?
    let expectedLast: String?
    let lastVisible: Bool?
    let elapsedSeconds: Double
    let status: String
    var busyAtEnd: Bool? = nil
    var statusTextAtEnd: String? = nil
}

struct RunResult: Codable {
    let kind: String
    let windowID: Int
    let startedAt: String
    let elapsedSeconds: Double
    let cases: [CaseResult]
    let pass: Bool
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(2)
}

func loadSpec(_ path: String) -> BrowseSpec {
    guard let data = FileManager.default.contents(atPath: path),
          let spec = try? JSONDecoder().decode(BrowseSpec.self, from: data),
          !spec.cases.isEmpty, spec.cases.count <= 20,
          spec.timeoutSeconds >= 2, spec.timeoutSeconds <= 60 else {
        fail("invalid browse specification")
    }
    for item in spec.cases {
        guard item.path.hasPrefix("/"), item.expectedCount >= 0,
              FileManager.default.fileExists(atPath: item.path) else {
            fail("browse case path must be an existing absolute path")
        }
    }
    return spec
}

func cgWindow(_ id: Int) -> [String: Any]? {
    let rows = CGWindowListCopyWindowInfo(.optionIncludingWindow, CGWindowID(id)) as? [[String: Any]] ?? []
    return rows.first
}

func axValue<T>(_ element: AXUIElement, _ attribute: CFString, _ type: AXValueType, _ value: inout T) -> Bool {
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute, &raw) == .success,
          let raw, CFGetTypeID(raw) == AXValueGetTypeID() else { return false }
    return AXValueGetValue(raw as! AXValue, type, &value)
}

func axWindows(pid: pid_t) -> [AXUIElement] {
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(AXUIElementCreateApplication(pid), kAXWindowsAttribute as CFString, &raw) == .success else { return [] }
    return raw as? [AXUIElement] ?? []
}

func stringAttribute(_ element: AXUIElement, _ attribute: CFString) -> String? {
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute, &raw) == .success else { return nil }
    return raw as? String
}

func finderWindow(id: Int) -> (pid: pid_t, title: String, element: AXUIElement)? {
    guard let row = cgWindow(id),
          let owner = row[kCGWindowOwnerName as String] as? String,
          owner == "Finder" || owner == "访达",
          let rawPID = row[kCGWindowOwnerPID as String] as? Int,
          let title = row[kCGWindowName as String] as? String,
          let rawBounds = row[kCGWindowBounds as String] as? [String: Any],
          let bounds = CGRect(dictionaryRepresentation: rawBounds as CFDictionary) else { return nil }
    let pid = pid_t(rawPID)
    for window in axWindows(pid: pid) {
        guard stringAttribute(window, kAXTitleAttribute as CFString) == title else { continue }
        var position = CGPoint.zero
        var size = CGSize.zero
        guard axValue(window, kAXPositionAttribute as CFString, .cgPoint, &position),
              axValue(window, kAXSizeAttribute as CFString, .cgSize, &size) else { continue }
        let candidate = CGRect(origin: position, size: size)
        if abs(candidate.minX - bounds.minX) <= 2 && abs(candidate.minY - bounds.minY) <= 2 &&
           abs(candidate.width - bounds.width) <= 2 && abs(candidate.height - bounds.height) <= 2 {
            return (pid, title, window)
        }
    }
    return nil
}

func finderWindow(title: String) -> (id: Int, pid: pid_t, element: AXUIElement)? {
    let rows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    for row in rows {
        guard (row[kCGWindowLayer as String] as? Int) == 0,
              (row[kCGWindowName as String] as? String) == title,
              let id = row[kCGWindowNumber as String] as? Int,
              let found = finderWindow(id: id) else { continue }
        return (id, found.pid, found.element)
    }
    return nil
}

func descendants(_ root: AXUIElement) -> [AXUIElement] {
    var queue = [root]
    var output: [AXUIElement] = []
    while !queue.isEmpty && output.count < 20_000 {
        let current = queue.removeLast()
        output.append(current)
        var raw: CFTypeRef?
        if AXUIElementCopyAttributeValue(current, kAXChildrenAttribute as CFString, &raw) == .success,
           let children = raw as? [AXUIElement] { queue.append(contentsOf: children) }
    }
    return output
}

func identifier(_ element: AXUIElement) -> String? {
    stringAttribute(element, "AXIdentifier" as CFString)
}

func findIdentifier(_ root: AXUIElement, _ wanted: String) -> AXUIElement? {
    descendants(root).first { identifier($0) == wanted }
}

func hasBusyIndicator(_ root: AXUIElement) -> Bool {
    for element in descendants(root) {
        if stringAttribute(element, kAXRoleAttribute as CFString) == kAXProgressIndicatorRole as String { return true }
        var raw: CFTypeRef?
        if AXUIElementCopyAttributeValue(element, "AXElementBusy" as CFString, &raw) == .success,
           (raw as? Bool) == true { return true }
    }
    return false
}

func parseCount(_ text: String) -> Int? {
    guard let boundary = text.range(of: "个项目") else { return nil }
    let prefix = text[..<boundary.lowerBound]
    let digits = prefix.filter { $0.isNumber }
    return Int(digits)
}

func observedCount(_ window: AXUIElement) -> Int? {
    guard !hasBusyIndicator(window), let status = findIdentifier(window, "statusBarText"),
          let value = stringAttribute(status, kAXValueAttribute as CFString) else { return nil }
    return parseCount(value)
}

func containsValue(_ window: AXUIElement, _ wanted: String) -> Bool {
    descendants(window).contains { stringAttribute($0, kAXValueAttribute as CFString) == wanted }
}

func waitUntil(deadline: Date, _ predicate: () -> Bool) -> Bool {
    while Date() < deadline {
        if predicate() { return true }
        Thread.sleep(forTimeInterval: 0.08)
    }
    return predicate()
}

func postKey(_ key: CGKeyCode, flags: CGEventFlags = []) {
    guard let down = CGEvent(keyboardEventSource: nil, virtualKey: key, keyDown: true),
          let up = CGEvent(keyboardEventSource: nil, virtualKey: key, keyDown: false) else { fail("cannot create key event") }
    down.flags = flags
    up.flags = []
    down.post(tap: .cgSessionEventTap)
    up.post(tap: .cgSessionEventTap)
}

func raise(_ found: (pid: pid_t, title: String, element: AXUIElement)) {
    let app = AXUIElementCreateApplication(found.pid)
    guard AXUIElementSetAttributeValue(app, kAXFrontmostAttribute as CFString, kCFBooleanTrue) == .success,
          AXUIElementPerformAction(found.element, kAXRaiseAction as CFString) == .success else {
        fail("cannot raise exact Finder window")
    }
}

func navigate(windowID: Int, path: String, deadline: Date) -> Bool {
    guard let current = finderWindow(id: windowID) else { return false }
    raise(current)
    var field: AXUIElement?
    func locateField() -> Bool {
        for window in axWindows(pid: current.pid) {
            if let candidate = findIdentifier(window, "PathTextField") {
                field = candidate
                return true
            }
        }
        return false
    }
    // macOS can expose Go to Folder as a sheet beneath the Finder window,
    // rather than a top-level window with the historical GoToWindow ID.
    if !locateField() { postKey(5, flags: [.maskCommand, .maskShift]) }
    let appeared = waitUntil(deadline: deadline, locateField)
    guard appeared, let field else { return false }
    guard AXUIElementSetAttributeValue(field, kAXValueAttribute as CFString, path as CFString) == .success else { return false }
    postKey(36) // Return accepts completion.
    Thread.sleep(forTimeInterval: 0.12)
    postKey(36) // Return opens the exact path.
    let title = URL(fileURLWithPath: path).lastPathComponent
    return waitUntil(deadline: deadline) { finderWindow(id: windowID)?.title == title }
}

let args = CommandLine.arguments
guard args.count >= 2 else { fail("usage: browse.swift prepare SPEC INFO | run SPEC WINDOW_ID OUTPUT") }
let mode = args[1]
if mode == "inspect" {
    guard args.count == 3, let windowID = Int(args[2]), let found = finderWindow(id: windowID) else {
        fail("usage: browse.swift inspect WINDOW_ID (must identify a live Finder window)")
    }
    let rows: [[String: String]] = descendants(found.element).map { element in
        var row: [String: String] = [:]
        for key in ["AXRole", "AXIdentifier", "AXTitle", "AXValue", "AXDescription"] {
            if let value = stringAttribute(element, key as CFString) { row[key] = value }
        }
        return row
    }
    let result: [String: Any] = ["title": found.title, "busy": hasBusyIndicator(found.element),
        "elements": rows, "observed_at": ISO8601DateFormatter().string(from: Date())]
    FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys]))
    exit(0)
}
if mode == "prepare" {
    guard args.count == 4 else { fail("usage: browse.swift prepare SPEC INFO") }
    let spec = loadSpec(args[2])
    let first = spec.cases[0].path
    guard NSWorkspace.shared.open(URL(fileURLWithPath: first)) else { fail("Finder refused the first path") }
    let title = URL(fileURLWithPath: first).lastPathComponent
    var found: (id: Int, pid: pid_t, element: AXUIElement)?
    let ready = waitUntil(deadline: Date().addingTimeInterval(min(8, spec.timeoutSeconds))) {
        found = finderWindow(title: title)
        return found != nil
    }
    guard ready, let found else { fail("exact Finder window did not appear") }
    let info: [String: Any] = ["window_id": found.id, "title": title, "first_path": first]
    let data = try JSONSerialization.data(withJSONObject: info, options: [.prettyPrinted, .sortedKeys])
    try data.write(to: URL(fileURLWithPath: args[3]), options: .withoutOverwriting)
    print("finder_browse_prepared window=\(found.id) title=\(title)")
    exit(0)
}

guard mode == "run", args.count == 5, let windowID = Int(args[3]) else {
    fail("usage: browse.swift run SPEC WINDOW_ID OUTPUT")
}
let spec = loadSpec(args[2])
let started = Date()
let absoluteDeadline = started.addingTimeInterval(spec.timeoutSeconds)
var results: [CaseResult] = []
for (index, item) in spec.cases.enumerated() {
    let caseStart = Date()
    let caseDeadline = min(absoluteDeadline, caseStart.addingTimeInterval(8))
    let expectedTitle = URL(fileURLWithPath: item.path).lastPathComponent
    let atExpectedPath = index == 0 ? finderWindow(id: windowID)?.title == expectedTitle
                                    : navigate(windowID: windowID, path: item.path, deadline: caseDeadline)
    if !atExpectedPath {
        results.append(CaseResult(path: item.path, expectedCount: item.expectedCount, observedCount: nil,
                                  expectedLast: item.expectedLast, lastVisible: nil,
                                  elapsedSeconds: Date().timeIntervalSince(caseStart), status: "NAVIGATION_TIMEOUT"))
        break
    }
    var count: Int?
    let stable = waitUntil(deadline: caseDeadline) {
        guard let current = finderWindow(id: windowID) else { return false }
        count = observedCount(current.element)
        return count == item.expectedCount
    }
    var lastVisible: Bool? = nil
    if stable, let expectedLast = item.expectedLast, let current = finderWindow(id: windowID) {
        if let list = findIdentifier(current.element, "ListView") {
            _ = AXUIElementSetAttributeValue(list, kAXFocusedAttribute as CFString, kCFBooleanTrue)
        }
        postKey(119) // End
        lastVisible = waitUntil(deadline: caseDeadline) {
            guard let refreshed = finderWindow(id: windowID) else { return false }
            return containsValue(refreshed.element, expectedLast)
        }
    }
    let passed = stable && (lastVisible ?? true)
    results.append(CaseResult(path: item.path, expectedCount: item.expectedCount, observedCount: count,
                              expectedLast: item.expectedLast, lastVisible: lastVisible,
                              elapsedSeconds: Date().timeIntervalSince(caseStart), status: passed ? "PASS" : "FAIL"))
    if let current = finderWindow(id: windowID) {
        results[results.count - 1].busyAtEnd = hasBusyIndicator(current.element)
        if let status = findIdentifier(current.element, "statusBarText") {
            results[results.count - 1].statusTextAtEnd = stringAttribute(status, kAXValueAttribute as CFString)
        }
    }
    if !passed || Date() >= absoluteDeadline { break }
}
let pass = results.count == spec.cases.count && results.allSatisfy { $0.status == "PASS" }
let result = RunResult(kind: "real-finder-accessibility-browse", windowID: windowID,
                       startedAt: ISO8601DateFormatter().string(from: started),
                       elapsedSeconds: Date().timeIntervalSince(started), cases: results, pass: pass)
let encoder = JSONEncoder()
encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
try encoder.encode(result).write(to: URL(fileURLWithPath: args[4]), options: .withoutOverwriting)
print("finder_browse_finished pass=\(pass) cases=\(results.count) seconds=\(result.elapsedSeconds)")
exit(pass ? 0 : 1)
