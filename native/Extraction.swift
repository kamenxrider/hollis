// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import CoreGraphics
import Darwin
import Foundation
import ImageIO
import PDFKit
import Vision

/// On-device document extraction: the PDF text layer, and Vision OCR for pages
/// that have none. It is a pure library: no model, no session, no protocol
/// knowledge. The helper owns the wire surface and the Go runner owns the
/// prompt budget; this file owns one document and its caps.
///
/// Rules that outlive any caller:
/// - Media is detected from content, never from an extension.
/// - Input is opened without following a symlink, without blocking on a FIFO,
///   and as a regular file only.
/// - Nothing is truncated. A document whose text would cross the ceiling fails
///   with the limit and the bytes actually produced.
/// - No path, no page text and no framework diagnostic leaves this file: a
///   failure is a typed case, and the caller decides what to say.
enum Extraction {
    /// The media this release can read. Text formats are decoded by the Go
    /// side, which already owns UTF-8 validation and the prompt budget.
    enum Media: String {
        case pdf
    }

    /// How one page's text was obtained.
    enum Method: String {
        case text
        case ocr
    }

    struct Page {
        let number: Int  // 1-based
        let text: String
        let method: Method
    }

    struct Document {
        let media: Media
        let pageCount: Int
        let pages: [Page]
        let textBytes: Int
    }

    enum Failure: Error, Equatable {
        /// The content is not a format this release reads.
        case unsupportedMedia
        /// Unreadable document, or more pages than the whole-document ceiling.
        /// `limit` and `actual` are present when a ceiling was crossed.
        case invalidDocument(limit: Int?, actual: Int?)
        /// The caller's media hint contradicts the detected content.
        case mediaMismatch(detected: Media)
        /// A requested page does not exist in this document.
        case pageOutOfRange(page: Int, pageCount: Int)
        /// No selected page produced non-whitespace text.
        case noText
        /// Extracted text crossed the ceiling. `actual` is the byte count that
        /// crossed it, which is a real measurement of what was produced, not a
        /// claim about the rest of the document.
        case textLimit(limit: Int, actual: Int, page: Int)
    }

    static let maxPages = 2000
    static let maxTextBytes = 131_072
    static let maxFileBytes = 64 << 20
    /// Longest side of a page bitmap handed to Vision. OCR accuracy stops
    /// improving well below this, and memory stays bounded for a large page.
    static let maxOCRPageSidePixels = 3000
    private static let magicWindow = 1024

    /// Reads one document. `pages` is an optional 1-based selection in
    /// ascending order; nil means every page. `mediaHint` is checked against
    /// the detected content and never substitutes for it.
    static func extract(path: String, pages: [Int]? = nil, mediaHint: Media? = nil) throws -> Document {
        let data = try readRegularFile(path)
        let media = try detect(data)
        if let hint = mediaHint, hint != media {
            throw Failure.mediaMismatch(detected: media)
        }
        switch media {
        case .pdf:
            return try extractPDF(data, pages: pages)
        }
    }

    /// Detects the media of already-read bytes. Text is not a failure here: it
    /// is simply not this library's job, and the caller treats it as a
    /// document only if its own UTF-8 rules accept it.
    static func detect(_ data: Data) throws -> Media {
        let window = data.prefix(magicWindow)
        if window.range(of: Data("%PDF-".utf8)) != nil {
            return .pdf
        }
        throw Failure.unsupportedMedia
    }

    // MARK: - Safe input

    private static func readRegularFile(_ path: String) throws -> Data {
        // O_NONBLOCK keeps a FIFO from blocking before the type check; a
        // regular file ignores it and it is cleared once the type is known.
        let descriptor = open(path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK)
        guard descriptor >= 0 else { throw Failure.invalidDocument(limit: nil, actual: nil) }
        defer { close(descriptor) }
        var status = stat()
        guard fstat(descriptor, &status) == 0,
              (status.st_mode & S_IFMT) == S_IFREG else {
            throw Failure.invalidDocument(limit: nil, actual: nil)
        }
        _ = fcntl(descriptor, F_SETFL, 0)
        let size = Int(status.st_size)
        guard size >= 0 else { throw Failure.invalidDocument(limit: nil, actual: nil) }
        guard size <= maxFileBytes else {
            throw Failure.invalidDocument(limit: maxFileBytes, actual: size)
        }
        var data = Data()
        data.reserveCapacity(size)
        var buffer = [UInt8](repeating: 0, count: 1 << 16)
        while true {
            let count = buffer.withUnsafeMutableBytes { pointer in
                pread(descriptor, pointer.baseAddress, pointer.count, off_t(data.count))
            }
            if count < 0 { throw Failure.invalidDocument(limit: nil, actual: nil) }
            if count == 0 { break }
            data.append(contentsOf: buffer[0..<count])
            // A file that grew past the ceiling after the stat is still
            // refused, never partially read.
            guard data.count <= maxFileBytes else {
                throw Failure.invalidDocument(limit: maxFileBytes, actual: data.count)
            }
        }
        return data
    }

    // MARK: - PDF

    private static func extractPDF(_ data: Data, pages selection: [Int]?) throws -> Document {
        guard let document = PDFDocument(data: data) else {
            throw Failure.invalidDocument(limit: nil, actual: nil)
        }
        let pageCount = document.pageCount
        guard pageCount > 0 else { throw Failure.invalidDocument(limit: nil, actual: nil) }
        guard pageCount <= maxPages else {
            throw Failure.invalidDocument(limit: maxPages, actual: pageCount)
        }
        let numbers: [Int]
        if let selection {
            var seen = Set<Int>()
            var ordered: [Int] = []
            for number in selection {
                guard number >= 1, number <= pageCount else {
                    throw Failure.pageOutOfRange(page: number, pageCount: pageCount)
                }
                if seen.insert(number).inserted { ordered.append(number) }
            }
            numbers = ordered.sorted()
        } else {
            numbers = Array(1...pageCount)
        }

        var pages: [Page] = []
        pages.reserveCapacity(numbers.count)
        var textBytes = 0
        var sawText = false
        for number in numbers {
            guard let page = document.page(at: number - 1) else {
                throw Failure.invalidDocument(limit: nil, actual: nil)
            }
            var text = page.string ?? ""
            var method = Method.text
            if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                method = .ocr
                text = renderAndRecognize(page)
            }
            if !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                sawText = true
            }
            textBytes += text.utf8.count
            guard textBytes <= maxTextBytes else {
                throw Failure.textLimit(limit: maxTextBytes, actual: textBytes, page: number)
            }
            pages.append(Page(number: number, text: text, method: method))
        }
        guard sawText else { throw Failure.noText }
        return Document(media: .pdf, pageCount: pageCount, pages: pages, textBytes: textBytes)
    }

    /// Renders one page at a bounded, orientation-corrected size and returns
    /// Vision's recognized lines joined in reading order. Text is not
    /// post-processed: the caller sees exactly what OCR produced.
    private static func renderAndRecognize(_ page: PDFPage) -> String {
        guard let image = render(page) else { return "" }
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        let handler = VNImageRequestHandler(cgImage: image, options: [:])
        do { try handler.perform([request]) } catch { return "" }
        let observations = request.results ?? []
        return observations.compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
    }

    private static func render(_ page: PDFPage) -> CGImage? {
        let bounds = page.bounds(for: .mediaBox)
        let quarterTurn = page.rotation == 90 || page.rotation == 270
        let sourceWidth = quarterTurn ? bounds.height : bounds.width
        let sourceHeight = quarterTurn ? bounds.width : bounds.height
        guard sourceWidth > 0, sourceHeight > 0 else { return nil }
        let scale = min(2, CGFloat(maxOCRPageSidePixels) / max(sourceWidth, sourceHeight))
        let width = max(1, Int((sourceWidth * scale).rounded()))
        let height = max(1, Int((sourceHeight * scale).rounded()))
        guard width <= maxOCRPageSidePixels, height <= maxOCRPageSidePixels,
              let context = CGContext(data: nil, width: width, height: height,
                                      bitsPerComponent: 8, bytesPerRow: 0,
                                      space: CGColorSpaceCreateDeviceGray(),
                                      bitmapInfo: CGImageAlphaInfo.none.rawValue) else {
            return nil
        }
        context.setFillColor(gray: 1, alpha: 1)
        context.fill(CGRect(x: 0, y: 0, width: width, height: height))
        context.interpolationQuality = .high
        // PDFPage draws the page with its rotation applied, so the context is
        // sized for the displayed page rather than the media box.
        context.scaleBy(x: CGFloat(width) / sourceWidth, y: CGFloat(height) / sourceHeight)
        page.draw(with: .mediaBox, to: context)
        return context.makeImage()
    }
}
