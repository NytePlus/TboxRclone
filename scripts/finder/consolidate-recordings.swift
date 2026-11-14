// Preserve selected acceptance clips in one passthrough movie per scenario.
import Foundation
import AVFoundation

func run() async throws {
    let args = CommandLine.arguments
    guard args.count >= 4 else { throw NSError(domain: "usage: OUTPUT.mov INPUT...", code: 1) }
    let output = URL(fileURLWithPath: args[1])
    guard !FileManager.default.fileExists(atPath: output.path) else { throw NSError(domain: "output exists", code: 1) }
    let composition = AVMutableComposition()
    guard let track = composition.addMutableTrack(withMediaType: .video, preferredTrackID: kCMPersistentTrackID_Invalid) else { throw NSError(domain: "track creation", code: 1) }
    var cursor = CMTime.zero
    var segments: [[String: Any]] = []
    struct Clip: Decodable {
        let file: String
        let source: String?
        let offset: Double?
        let duration: Double?
    }
    let clips: [Clip]
    if args[2] == "--plan" {
        guard args.count == 4 else { throw NSError(domain: "plan requires one JSON file", code: 1) }
        clips = try JSONDecoder().decode([Clip].self, from: Data(contentsOf: URL(fileURLWithPath: args[3])))
    } else {
        clips = args.dropFirst(2).map { Clip(file: $0, source: nil, offset: nil, duration: nil) }
    }
    guard !clips.isEmpty else { throw NSError(domain: "empty clip plan", code: 1) }
    for clip in clips {
        let asset = AVURLAsset(url: URL(fileURLWithPath: clip.file))
        let assetDuration = try await asset.load(.duration)
        let offset = clip.offset ?? 0
        let length = clip.duration ?? assetDuration.seconds
        guard offset.isFinite && length.isFinite && offset >= 0 && length > 0 && offset + length <= assetDuration.seconds + 0.01 else { throw NSError(domain: "invalid clip range", code: 1) }
        let duration = CMTime(seconds: length, preferredTimescale: 60000)
        guard duration.seconds.isFinite && duration.seconds > 0,
              let source = try await asset.loadTracks(withMediaType: .video).first else { throw NSError(domain: "invalid input", code: 1) }
        try track.insertTimeRange(CMTimeRange(start: CMTime(seconds: offset, preferredTimescale: 60000), duration: duration), of: source, at: cursor)
        segments.append(["source": clip.source ?? clip.file, "start_seconds": cursor.seconds, "duration_seconds": duration.seconds, "copied_from_file": clip.file, "copied_from_start_seconds": offset])
        cursor = CMTimeAdd(cursor, duration)
    }
    guard let exporter = AVAssetExportSession(asset: composition, presetName: AVAssetExportPresetPassthrough) else { throw NSError(domain: "export unavailable", code: 1) }
    try await exporter.export(to: output, as: .mov)
    let saved = AVURLAsset(url: output)
    let duration = try await saved.load(.duration).seconds
    guard abs(duration - cursor.seconds) < 0.2 else { throw NSError(domain: "duration mismatch", code: 1) }
    let generator = AVAssetImageGenerator(asset: saved)
    generator.maximumSize = CGSize(width: 320, height: 240)
    for segment in segments {
        let start = segment["start_seconds"] as! Double
        let length = segment["duration_seconds"] as! Double
        for offset in [min(0.5, length / 2), max(0, length - 0.5)] {
            _ = try await generator.image(at: CMTime(seconds: start + offset, preferredTimescale: 600))
        }
    }
    let index: [String: Any] = ["mode": "passthrough concatenation; segments are separate historical runs, not continuous capture", "duration_seconds": duration, "segments": segments, "validation": "duration and first/last frame decode of each segment"]
    try JSONSerialization.data(withJSONObject: index, options: [.prettyPrinted, .sortedKeys]).write(to: URL(fileURLWithPath: output.path + ".index.json"))
    print("validated \(output.path) segments=\(segments.count) duration=\(duration)")
}
do { try await run() } catch {
    fputs("Consolidation failed: \(error)\n", stderr)
    exit(1)
}
