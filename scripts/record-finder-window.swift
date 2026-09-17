// Record explicitly identified Finder windows. Never capture an unfiltered display.
// Usage: swift -module-cache-path /private/tmp/tboxrclone-swift-cache scripts/record-finder-window.swift ID[,ID] TITLE[|TITLE] OUTPUT.mp4 STOPFILE SECONDS
import Foundation
import AppKit
import ScreenCaptureKit
import AVFoundation

final class RecordingDelegate: NSObject, SCRecordingOutputDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var done = false
    private var failure: Error?
    func recordingOutputDidStartRecording(_ recordingOutput: SCRecordingOutput) {
        print("RECORDING_STARTED")
        fflush(stdout)
    }
    func recordingOutput(_ recordingOutput: SCRecordingOutput, didFailWithError error: Error) {
        lock.lock(); failure = error; done = true; lock.unlock()
    }
    func recordingOutputDidFinishRecording(_ recordingOutput: SCRecordingOutput) {
        lock.lock(); done = true; lock.unlock()
    }
    func status() -> (Bool, Error?) {
        lock.lock(); defer { lock.unlock() }; return (done, failure)
    }
}

// Initialize the window-server connection before ScreenCaptureKit filtering.
_ = NSApplication.shared
_ = NSScreen.screens
let args = CommandLine.arguments
guard args.count == 6, !args[1].isEmpty, let seconds = Double(args[5]), seconds > 0, seconds <= 300 else {
    fatalError("Expected Finder window IDs, exact titles, output movie, stop file, and duration (1...300 seconds)")
}
let outputURL = URL(fileURLWithPath: args[3])
guard !FileManager.default.fileExists(atPath: outputURL.path), !FileManager.default.fileExists(atPath: args[4]) else {
    fatalError("Output and stop file must not already exist")
}
let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
let ids = args[1].split(separator: ",").compactMap { UInt32($0) }
let titles = args[2].components(separatedBy: "|")
guard !ids.isEmpty, ids.count <= 2, ids.count == titles.count else { fatalError("One or two Finder window identities required") }
var selected: [SCWindow] = []
for (index, id) in ids.enumerated() {
    guard let window = content.windows.first(where: { $0.windowID == id }),
          window.owningApplication?.bundleIdentifier == "com.apple.finder", window.title == titles[index] else {
        fatalError("Window identity does not match the requested Finder window")
    }
    selected.append(window)
}
let filter: SCContentFilter
let frame: CGRect
if selected.count == 1 {
    filter = SCContentFilter(desktopIndependentWindow: selected[0])
    frame = selected[0].frame
} else {
    guard let display = content.displays.first(where: { d in selected.allSatisfy { d.frame.contains($0.frame) } }) else {
        fatalError("Both Finder windows must fit on the same display")
    }
    // Only these two windows contribute pixels; other apps and desktop are excluded.
    filter = SCContentFilter(display: display, including: selected)
    frame = display.frame
}
let config = SCStreamConfiguration()
config.width = Int(frame.width * 2)
config.height = Int(frame.height * 2)
config.minimumFrameInterval = CMTime(value: 1, timescale: 15)
config.capturesAudio = false
config.captureMicrophone = false
config.showsCursor = true
config.showMouseClicks = true
let recordingConfig = SCRecordingOutputConfiguration()
recordingConfig.outputURL = outputURL
recordingConfig.outputFileType = .mp4
recordingConfig.videoCodecType = .h264
let delegate = RecordingDelegate()
let recording = SCRecordingOutput(configuration: recordingConfig, delegate: delegate)
let stream = SCStream(filter: filter, configuration: config, delegate: nil)
try stream.addRecordingOutput(recording)
try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
    stream.startCapture { error in
        if let error = error { continuation.resume(throwing: error) } else { continuation.resume() }
    }
}
let end = Date().addingTimeInterval(seconds)
while Date() < end && !FileManager.default.fileExists(atPath: args[4]) {
    if let error = delegate.status().1 { throw error }
    try await Task.sleep(for: .milliseconds(200))
}
try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
    stream.stopCapture { error in
        if let error = error { continuation.resume(throwing: error) } else { continuation.resume() }
    }
}
let finishDeadline = Date().addingTimeInterval(15)
while !delegate.status().0 && Date() < finishDeadline { try await Task.sleep(for: .milliseconds(100)) }
let status = delegate.status()
if let error = status.1 { throw error }
guard status.0 else { fatalError("Recording did not finish") }
let asset = AVURLAsset(url: outputURL)
let tracks = try await asset.loadTracks(withMediaType: .video)
guard let track = tracks.first else { fatalError("No recorded video track") }
let size = try await track.load(.naturalSize)
let duration = try await asset.load(.duration)
print("RECORDING_FINISHED width=\(size.width) height=\(size.height) seconds=\(duration.seconds)")
