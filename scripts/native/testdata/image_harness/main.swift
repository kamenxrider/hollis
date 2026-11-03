// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import CoreGraphics
import Foundation
import ImageIO

func emit(_ fields: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: fields, options: [.sortedKeys])
    FileHandle.standardOutput.write(data + Data([10]))
}

let args = CommandLine.arguments
if args[1] == "make" {
    let width = Int(args[4])!, height = Int(args[5])!
    let context = CGContext(data: nil, width: width, height: height,
                            bitsPerComponent: 8, bytesPerRow: 0,
                            space: CGColorSpaceCreateDeviceRGB(),
                            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    context.setFillColor(CGColor(red: 1, green: 0, blue: 0, alpha: 1))
    context.fill(CGRect(x: 0, y: 0, width: width, height: height))
    let destination = CGImageDestinationCreateWithURL(
        URL(fileURLWithPath: args[2]) as CFURL,
        (args[3] == "png" ? "public.png" : "public.jpeg") as CFString, 1, nil)!
    CGImageDestinationAddImage(destination, context.makeImage()!, [
        kCGImagePropertyOrientation: Int(args[6])!,
    ] as CFDictionary)
    emit(["ok": CGImageDestinationFinalize(destination)])
} else {
    do {
        let image = try ImageInput.load(path: args[2])
        emit(["width": image.width, "height": image.height])
    } catch ImageInput.Failure.unsupportedMedia {
        emit(["kind": "unsupported_media"])
    } catch {
        emit(["kind": "invalid_image"])
    }
}
