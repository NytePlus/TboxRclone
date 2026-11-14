// Bounded display recording with explicit stream/recording failure diagnostics.
// Usage: record-display.swift DISPLAY_INDEX OUTPUT.mov SECONDS
import Foundation
import AppKit
import ScreenCaptureKit
import AVFoundation

final class RecordingDelegate: NSObject, SCRecordingOutputDelegate, SCStreamDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var done = false
    private var failure: Error?
    func stream(_ stream: SCStream, didStopWithError error: Error) {
        print("STREAM_STOPPED domain=\((error as NSError).domain) code=\((error as NSError).code) description=\(error.localizedDescription)")
        fflush(stdout)
        lock.lock(); failure = error; done = true; lock.unlock()
    }
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

_ = NSApplication.shared
let args = CommandLine.arguments
guard (args.count == 4 || args.count == 5), let index = Int(args[1]), index > 0,
      let seconds = Double(args[3]), seconds >= 1, seconds <= 300 else {
    fatalError("Expected display index, new output file, and duration 1...300")
}
let outputURL = URL(fileURLWithPath: args[2])
guard !FileManager.default.fileExists(atPath: outputURL.path) else { fatalError("Output already exists") }
var count: UInt32 = 0
guard CGGetActiveDisplayList(0, nil, &count) == .success, index <= count else { fatalError("Display unavailable") }
var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
guard CGGetActiveDisplayList(count, &ids, &count) == .success else { fatalError("Display enumeration failed") }
let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
guard let display = content.displays.first(where: { $0.displayID == ids[index - 1] }) else { fatalError("Display no longer available") }
let filter = SCContentFilter(display: display, excludingWindows: [])
let config = SCStreamConfiguration()
config.width = display.width
config.height = display.height
config.minimumFrameInterval = CMTime(value: 1, timescale: 15)
config.capturesAudio = false
config.captureMicrophone = false
config.showsCursor = true
config.showMouseClicks = true
let recordingConfig = SCRecordingOutputConfiguration()
recordingConfig.outputURL = outputURL
recordingConfig.outputFileType = .mov
recordingConfig.videoCodecType = .h264
let delegate = RecordingDelegate()
let recording = SCRecordingOutput(configuration: recordingConfig, delegate: delegate)
let stream = SCStream(filter: filter, configuration: config, delegate: delegate)
try stream.addRecordingOutput(recording)
try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
    stream.startCapture { error in
        if let error = error { continuation.resume(throwing: error) } else { continuation.resume() }
    }
}
let started = Date()
let stopFile = args.count == 5 ? args[4] : nil
var stopDetected: Date?
while Date().timeIntervalSince(started) < seconds {
    let status = delegate.status()
    if let error = status.1 {
        fputs("RECORDING_INTERRUPTED: \(error.localizedDescription); verify final task state\n", stderr)
        exit(1)
    }
    if status.0 {
        fputs("Recording finished before requested duration\n", stderr)
        exit(1)
    }
    if let stopFile, FileManager.default.fileExists(atPath: stopFile) {
        if stopDetected == nil { stopDetected = Date() }
        if Date().timeIntervalSince(stopDetected!) >= 3 { break }
    }
    try await Task.sleep(for: .milliseconds(100))
}
let expectedDuration = Date().timeIntervalSince(started)
try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
    stream.stopCapture { error in
        if let error = error { continuation.resume(throwing: error) } else { continuation.resume() }
    }
}
let deadline = Date().addingTimeInterval(15)
while !delegate.status().0 && Date() < deadline { try await Task.sleep(for: .milliseconds(100)) }
let status = delegate.status()
if let error = status.1 { throw error }
guard status.0 else { fatalError("Recording finish callback timed out") }
let duration = try await AVURLAsset(url: outputURL).load(.duration).seconds
print("RECORDING_FINISHED requested=\(seconds) duration=\(duration) display=\(display.displayID)")
guard duration >= expectedDuration - 2 else { fatalError("Recorded duration is incomplete") }
if stopDetected != nil { print("EXPECTED_ERROR_CAPTURE_FINISHED") }
