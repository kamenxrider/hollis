// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import CoreGraphics
import Darwin
import Foundation
import ImageIO

// Decode bounded image attachments without a model session or OCR.
enum ImageInput {
    enum Failure: Error, Equatable {
        case unsupportedMedia
        case invalidImage
    }

    static let maxFileBytes = 64 << 20
    static let maxPixels = 64_000_000
    static let maxSidePixels = 4096

    static func load(path: String) throws -> CGImage {
        let data = try readRegularFile(path)
        let isPNG = data.starts(with: [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
        let isJPEG = data.starts(with: [0xff, 0xd8, 0xff])
        guard isPNG || isJPEG else { throw Failure.unsupportedMedia }
        guard let source = CGImageSourceCreateWithData(data as CFData, [
            kCGImageSourceShouldCache: false,
        ] as CFDictionary),
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = properties[kCGImagePropertyPixelWidth] as? Int,
              let height = properties[kCGImagePropertyPixelHeight] as? Int,
              width > 0, height > 0,
              width <= maxPixels / height else {
            throw Failure.invalidImage
        }
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxSidePixels,
            kCGImageSourceShouldCacheImmediately: true,
        ] as CFDictionary),
              image.width <= maxSidePixels, image.height <= maxSidePixels else {
            throw Failure.invalidImage
        }
        return image
    }

    private static func readRegularFile(_ path: String) throws -> Data {
        guard path.hasPrefix("/") else { throw Failure.invalidImage }
        let descriptor = open(path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK)
        guard descriptor >= 0 else { throw Failure.invalidImage }
        defer { close(descriptor) }
        var status = stat()
        guard fstat(descriptor, &status) == 0,
              (status.st_mode & S_IFMT) == S_IFREG,
              status.st_size >= 0, status.st_size <= maxFileBytes else {
            throw Failure.invalidImage
        }
        var data = Data()
        data.reserveCapacity(Int(status.st_size))
        var buffer = [UInt8](repeating: 0, count: 1 << 16)
        while true {
            let count = buffer.withUnsafeMutableBytes {
                pread(descriptor, $0.baseAddress, $0.count, off_t(data.count))
            }
            guard count >= 0 else { throw Failure.invalidImage }
            if count == 0 { return data }
            guard data.count <= maxFileBytes - count else { throw Failure.invalidImage }
            data.append(contentsOf: buffer[..<count])
        }
    }
}
