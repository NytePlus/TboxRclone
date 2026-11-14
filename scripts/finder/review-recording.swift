// Review at ten-second intervals, plus a separately required final frame.
import Foundation
import AVFoundation
import AppKit
let started = Date()
guard CommandLine.arguments.count == 3 else {
    fputs("usage: review-recording.swift MOVIE NEW_PREFIX\n", stderr); exit(2)
}
let prefix = CommandLine.arguments[2]
let asset = AVURLAsset(url: URL(fileURLWithPath: CommandLine.arguments[1]))
let duration = try await asset.load(.duration).seconds
guard duration.isFinite, duration > 0 else { exit(2) }
let generator = AVAssetImageGenerator(asset: asset)
generator.appliesPreferredTrackTransform = true
generator.requestedTimeToleranceBefore = .zero
generator.requestedTimeToleranceAfter = .zero
generator.maximumSize = CGSize(width: 640, height: 420)
let times = Array(stride(from: 0.0, to: duration, by: 10.0))
var sheets: [[String: Any]] = []
for start in stride(from: 0, to: times.count, by: 10) {
    let end = min(times.count, start + 10)
    let height = ((end - start + 1) / 2) * 450
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 1280,
        pixelsHigh: height, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
        isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)!
    let size = NSSize(width: 1280, height: height)
    NSColor.white.setFill()
    NSRect(origin: .zero, size: size).fill()
    for i in start..<end {
        let (frame, _) = try await generator.image(at: CMTime(seconds: times[i], preferredTimescale: 600))
        let local = i - start
        let x = (local % 2) * 640, y = Int(size.height) - (local / 2 + 1) * 450
        NSImage(cgImage: frame,size: .zero).draw(in: NSRect(x:x,y:y+25,width:640,height:420))
        ("t=\(Int(times[i]))s" as NSString).draw(at:NSPoint(x:x+5,y:y+5),
            withAttributes:[.foregroundColor:NSColor.black,.font:NSFont.systemFont(ofSize:16)])
    }
    NSGraphicsContext.restoreGraphicsState()
    let file = prefix + ".\(Int(times[start])).png"
    try bitmap.representation(using:.png,properties:[:])!.write(to:URL(fileURLWithPath:file), options:.withoutOverwriting)
    sheets.append(["file":URL(fileURLWithPath:file).lastPathComponent,
                   "seconds":Array(times[start..<end])])
}
// The last decoded frame is mandatory to inspect. An early-ended movie's
// final frame is NOT the final task state; capture that state separately.
generator.maximumSize = CGSize(width: 1920, height: 1200)
let finalTime = max(0, duration - 0.1)
let (lastFrame, actualTime) = try await generator.image(at: CMTime(seconds:finalTime,preferredTimescale:600))
let finalFile = prefix + ".final.png"
try NSBitmapImageRep(cgImage:lastFrame).representation(using:.png,properties:[:])!
    .write(to:URL(fileURLWithPath:finalFile),options:.withoutOverwriting)
let index: [String: Any] = ["duration_seconds":duration,"sample_interval_seconds":10,
    "frames":times.count+1,"generation_seconds":Date().timeIntervalSince(started),
    "sheets":sheets,"final_frame":["file":URL(fileURLWithPath:finalFile).lastPathComponent,
        "actual_second":actualTime.seconds,"required_review":true],
    "ui_verdict":"REQUIRES_10_SECOND_SAMPLES_AND_FINAL_TASK_STATE_REVIEW"]
try JSONSerialization.data(withJSONObject:index,options:[.prettyPrinted,.sortedKeys])
    .write(to:URL(fileURLWithPath:prefix+".index.json"),options:.withoutOverwriting)
print("duration=\(duration), frames=\(times.count+1), sample_interval=10s; inspect final task state separately if recording ended early")
