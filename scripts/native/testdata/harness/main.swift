// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
// Test-only driver for native/Extraction.swift. It builds synthetic PDFs and
// prints extraction results as one JSON document. It never imports
// FoundationModels: no model is created, no session exists, no prompt is sent.
import CoreGraphics
import CoreText
import Foundation
import ImageIO
import PDFKit
import Vision

let arguments = CommandLine.arguments
guard arguments.count >= 2 else { fputs("usage: harness <mode> ...\n", stderr); exit(2) }

func fail(_ message: String) -> Never {
    let payload: [String: Any] = ["ok": false, "error": message]
    let data = try! JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
    FileHandle.standardOutput.write(data + Data([10]))
    exit(0)
}

func emit(_ payload: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
    FileHandle.standardOutput.write(data + Data([10]))
}

let mediaBox = CGRect(x: 0, y: 0, width: 612, height: 792)

func drawLine(_ context: CGContext, _ text: String, fontSize: CGFloat, x: CGFloat, y: CGFloat) {
    let font = CTFontCreateWithName("Helvetica" as CFString, fontSize, nil)
    let attributed = NSAttributedString(string: text, attributes: [kCTFontAttributeName as NSAttributedString.Key: font])
    let line = CTLineCreateWithAttributedString(attributed)
    context.saveGState()
    context.textPosition = CGPoint(x: x, y: y)
    CTLineDraw(line, context)
    context.restoreGState()
}

func makePDF(path: String, pages: Int, text: String, scan: (Int) -> Bool) {
    var box = mediaBox
    guard let context = CGContext(URL(fileURLWithPath: path) as CFURL, mediaBox: &box, nil) else {
        fail("could not create PDF")
    }
    for index in 1...max(1, pages) {
        context.beginPDFPage(nil)
        context.setFillColor(gray: 1, alpha: 1)
        context.fill(mediaBox)
        context.setFillColor(gray: 0, alpha: 1)
        if scan(index) {
            // Draw the line into a bitmap and place the bitmap on the page, so
            // the page carries pixels and no text layer.
            let width = 1200, height = 400
            if let bitmap = CGContext(data: nil, width: width, height: height,
                                      bitsPerComponent: 8, bytesPerRow: 0,
                                      space: CGColorSpaceCreateDeviceGray(),
                                      bitmapInfo: CGImageAlphaInfo.none.rawValue) {
                bitmap.setFillColor(gray: 1, alpha: 1)
                bitmap.fill(CGRect(x: 0, y: 0, width: width, height: height))
                bitmap.setFillColor(gray: 0, alpha: 1)
                drawLine(bitmap, text, fontSize: 96, x: 60, y: 150)
                if let image = bitmap.makeImage() {
                    context.draw(image, in: CGRect(x: 60, y: 300, width: 492, height: 164))
                }
            }
        } else {
            drawLine(context, "Page \(index)", fontSize: 20, x: 72, y: 700)
            drawLine(context, text, fontSize: 24, x: 72, y: 640)
        }
        context.endPDFPage()
    }
    context.closePDF()
}

func makeLongPDF(path: String, pages: Int, lines: Int, text: String) {
    var box = mediaBox
    guard let context = CGContext(URL(fileURLWithPath: path) as CFURL, mediaBox: &box, nil) else {
        fail("could not create PDF")
    }
    for _ in 1...max(1, pages) {
        context.beginPDFPage(nil)
        context.setFillColor(gray: 1, alpha: 1)
        context.fill(mediaBox)
        context.setFillColor(gray: 0, alpha: 1)
        for line in 0..<max(1, lines) {
            drawLine(context, text, fontSize: 10, x: 24, y: 740 - CGFloat(line) * 12)
        }
        context.endPDFPage()
    }
    context.closePDF()
}

func makeBlankPDF(path: String) {
    var box = mediaBox
    guard let context = CGContext(URL(fileURLWithPath: path) as CFURL, mediaBox: &box, nil) else {
        fail("could not create PDF")
    }
    context.beginPDFPage(nil)
    context.setFillColor(gray: 1, alpha: 1)
    context.fill(mediaBox)
    context.setFillColor(gray: 0, alpha: 1)
    context.fill(CGRect(x: 100, y: 400, width: 200, height: 120))
    context.endPDFPage()
    context.closePDF()
}

func makeRotatedPDF(path: String, text: String, rotation: Int = 90) {
    var box = mediaBox
    guard let context = CGContext(URL(fileURLWithPath: path) as CFURL, mediaBox: &box, nil) else {
        fail("could not create PDF")
    }
    context.beginPDFPage(nil)
    context.setFillColor(gray: 1, alpha: 1)
    context.fill(mediaBox)
    context.setFillColor(gray: 0, alpha: 1)
    let width = 1600, height = 800
    if let bitmap = CGContext(data: nil, width: width, height: height,
                              bitsPerComponent: 8, bytesPerRow: 0,
                              space: CGColorSpaceCreateDeviceGray(),
                              bitmapInfo: CGImageAlphaInfo.none.rawValue) {
        bitmap.setFillColor(gray: 1, alpha: 1)
        bitmap.fill(CGRect(x: 0, y: 0, width: width, height: height))
        bitmap.setFillColor(gray: 0, alpha: 1)
        drawLine(bitmap, text, fontSize: 140, x: 80, y: 340)
        if let image = bitmap.makeImage() {
            // A tall page rotated a quarter turn, as a scanned portrait page
            // commonly arrives.
            context.draw(image, in: CGRect(x: 200, y: 96, width: 400, height: 600))
        }
    }
    context.endPDFPage()
    context.closePDF()
    guard let document = PDFDocument(url: URL(fileURLWithPath: path)) else { fail("could not reopen PDF") }
    document.page(at: 0)?.rotation = rotation
    document.write(to: URL(fileURLWithPath: path))
}

func parsePages(_ value: String?) -> [Int]? {
    guard let value, !value.isEmpty else { return nil }
    return value.split(separator: ",").compactMap { Int($0) }
}

struct InjectedFault: Error {}

/// Replaces one OCR step so a failure the frameworks do not produce on demand
/// is reached through the library's real error handling.
func ocr(fault: String?) -> Extraction.OCR {
    var ocr = Extraction.OCR.vision
    switch fault ?? "" {
    case "":
        break
    case "render-fails":
        ocr.render = { _ in nil }
    case "recognize-fails":
        ocr.recognize = { _ in throw InjectedFault() }
    case "recognize-empty":
        ocr.recognize = { _ in [] }
    default:
        fail("unknown fault \(fault ?? "")")
    }
    return ocr
}

func extractBackupFixture(path: String, fault: String, marker: String?) {
    var attempts: [String] = []
    var orientations: [String] = []
    var renderCalls = 0
    var firstImage: CGImage?
    var firstLanguages: [String]?
    var sameImage = true
    var sameLanguages = true
    var injected = Extraction.OCR.vision
    let render = injected.render
    injected.render = { page in
        renderCalls += 1
        return fault == "render-nil" ? nil : render(page)
    }
    injected.recognize = { image in
        try Extraction.recognizeLines(image, checkCancellation: {
            if fault == "cancel-between" && !orientations.isEmpty { throw CancellationError() }
            try Task.checkCancellation()
        }) { image, request, orientation in
            let accurate = request.recognitionLevel == .accurate
            attempts.append(accurate ? "accurate" : "fast")
            if !accurate {
                let names: [CGImagePropertyOrientation: String] = [.up: "up", .right: "right", .down: "down", .left: "left"]
                orientations.append(names[orientation]!)
            }
            if let firstImage {
                sameImage = sameImage && image === firstImage
                sameLanguages = sameLanguages && request.recognitionLanguages == firstLanguages
            } else {
                firstImage = image
                firstLanguages = request.recognitionLanguages
            }
            if accurate {
                switch fault {
                case "accurate-success": return [.init(text: "Banana bread recipe", confidence: 1)]
                case "accurate-empty": return []
                case "cancelled": throw CancellationError()
                case "vision-cancelled": throw NSError(domain: VNErrorDomain, code: VNErrorCode.requestCancelled.rawValue)
                case "vision-timeout": throw NSError(domain: VNErrorDomain, code: VNErrorCode.timeout.rawValue)
                case "swift-cancelled": throw VisionError.requestCancelled("synthetic cancellation")
                case "swift-timeout": throw VisionError.timeout("synthetic timeout")
                case "url-cancelled": throw URLError(.cancelled)
                case "url-timeout": throw URLError(.timedOut)
                case "cocoa-cancelled": throw CocoaError(.userCancelled)
                case "posix-cancelled": throw POSIXError(.ECANCELED)
                case "posix-timeout": throw POSIXError(.ETIMEDOUT)
                case "underlying-timeout":
                    throw NSError(domain: "harness.Recognition", code: 1,
                                  userInfo: [NSUnderlyingErrorKey: URLError(.timedOut)])
                default: throw InjectedFault()
                }
            }
            if fault == "both-throw" { throw InjectedFault() }
            if fault == "fast-cancelled" { throw CancellationError() }
            if fault == "fast-timeout" { throw URLError(.timedOut) }
            if fault == "partial-throw" && orientation != .left { throw InjectedFault() }
            if fault == "partial-throw" { return [.init(text: "last orientation", confidence: 1)] }
            if fault == "tie" {
                let text = orientation == .up ? "prefer up" : "not chose"
                return [.init(text: text, confidence: 1)]
            }
            if fault == "weighted" {
                return orientation == .up ? [.init(text: "I", confidence: 1)] :
                    [.init(text: "longer reading", confidence: 0.8)]
            }
            if fault == "summed" {
                return orientation == .up ? [.init(text: "aa", confidence: 1), .init(text: "bb", confidence: 1)] :
                    [.init(text: "ccc", confidence: 1)]
            }
            if fault == "slow-fast" {
                if let marker { try Data("fast".utf8).write(to: URL(fileURLWithPath: marker)) }
                Thread.sleep(forTimeInterval: 60)
            }
            // Upright finds nothing; one turned view returns text at a confidence.
            let sideways: [String: (String, Float)] = [
                "sideways-below": ("Hi", 0.5), "sideways-at": ("ab", 1.0),
                "sideways-just-below": ("abc", 0.66), "up-throws-short": ("Hi", 0.5),
            ]
            if let (text, confidence) = sideways[fault] {
                if orientation == .up {
                    if fault == "up-throws-short" { throw InjectedFault() }
                    return []
                }
                return orientation == .right ? [.init(text: text, confidence: confidence)] : []
            }
            if fault == "upright-short" {
                return orientation == .up ? [.init(text: "OK", confidence: 0.5)] : []
            }
            if fault == "upside-down-tie" {
                switch orientation {
                case .up: return [.init(text: "MOU AEd", confidence: 0.5)]
                case .down: return [.init(text: "Pay now", confidence: 0.5)]
                default: return []
                }
            }
            if fault == "sideways-not-above-up" {
                return orientation == .up ? [.init(text: "abcd", confidence: 0.5)] :
                    [.init(text: "wx", confidence: 1.0)]
            }
            return try Extraction.performRecognition(image, request, orientation)
        }
    }
    var payload: [String: Any]
    do {
        let document = try Extraction.extract(path: path, ocr: injected)
        payload = ["ok": true, "pages": document.pages.map {
            ["page": $0.number, "text": $0.text, "method": $0.method.rawValue]
        }]
    } catch Extraction.Failure.noText {
        payload = ["ok": false, "failure": "extract_no_text"]
    } catch {
        payload = ["ok": false, "failure": "invalid_document"]
    }
    payload["attempts"] = attempts
    payload["orientations"] = orientations
    payload["render_calls"] = renderCalls
    payload["same_image"] = sameImage
    payload["same_languages"] = sameLanguages
    emit(payload)
}

let mode = arguments[1]
switch mode {
case "extract-backup":
    extractBackupFixture(path: arguments[2], fault: arguments[3],
                         marker: arguments.count > 4 ? arguments[4] : nil)
case "make-text-pdf":
    makePDF(path: arguments[2], pages: Int(arguments[3]) ?? 1, text: arguments[4], scan: { _ in false })
    emit(["ok": true])
case "make-mixed-pdf":
    // Page 1 has a text layer; page 2 carries pixels only and needs OCR.
    makePDF(path: arguments[2], pages: 2, text: arguments[3], scan: { $0 == 2 })
    emit(["ok": true])
case "make-long-pdf":
    makeLongPDF(path: arguments[2], pages: Int(arguments[3]) ?? 1,
                lines: Int(arguments[4]) ?? 1, text: arguments[5])
    emit(["ok": true])
case "make-locked-pdf":
    makePDF(path: arguments[2], pages: 1, text: "Synthetic locked document", scan: { _ in false })
    guard let document = PDFDocument(url: URL(fileURLWithPath: arguments[2])) else { fail("could not reopen PDF") }
    let options: [PDFDocumentWriteOption: Any] = [.userPasswordOption: "synthetic-user-password",
                                                  .ownerPasswordOption: "synthetic-owner-password"]
    guard document.write(to: URL(fileURLWithPath: arguments[2]), withOptions: options),
          PDFDocument(url: URL(fileURLWithPath: arguments[2]))?.isLocked == true else { fail("PDF was not locked") }
    emit(["ok": true])
case "make-scan-pdf":
    makePDF(path: arguments[2], pages: Int(arguments[3]) ?? 1, text: arguments[4], scan: { _ in true })
    emit(["ok": true])
case "make-blank-pdf":
    makeBlankPDF(path: arguments[2])
    emit(["ok": true])
case "make-rotated-pdf":
    makeRotatedPDF(path: arguments[2], text: arguments[3],
                   rotation: arguments.count > 4 ? Int(arguments[4]) ?? 90 : 90)
    emit(["ok": true])
case "extract", "detect":
    let path = arguments[2]
    let pages = arguments.count > 3 ? parsePages(arguments[3]) : nil
    let fault = arguments.count > 4 ? arguments[4] : nil
    do {
        if mode == "detect" {
            let data = try Data(contentsOf: URL(fileURLWithPath: path))
            emit(["ok": true, "media": try Extraction.detect(data).rawValue])
        } else {
            let document = try Extraction.extract(path: path, pages: pages, ocr: ocr(fault: fault))
            emit([
                "ok": true,
                "media": document.media.rawValue,
                "page_count": document.pageCount,
                "text_bytes": document.textBytes,
                "pages": document.pages.map { ["page": $0.number, "text": $0.text, "method": $0.method.rawValue] },
            ])
        }
    } catch let failure as Extraction.Failure {
        switch failure {
        case .unsupportedMedia:
            emit(["ok": false, "failure": "unsupported_media"])
        case .invalidDocument(let limit, let actual):
            var payload: [String: Any] = ["ok": false, "failure": "invalid_document"]
            if let limit { payload["limit"] = limit }
            if let actual { payload["actual"] = actual }
            emit(payload)
        case .mediaMismatch(let detected):
            emit(["ok": false, "failure": "extract_media_mismatch", "detected": detected.rawValue])
        case .pageOutOfRange(let page, let pageCount):
            emit(["ok": false, "failure": "page_out_of_range", "page": page, "page_count": pageCount])
        case .noText:
            emit(["ok": false, "failure": "extract_no_text"])
        case .textLimit(let limit, let actual, let page):
            emit(["ok": false, "failure": "context_capacity", "limit": limit, "actual": actual, "page": page])
        }
    } catch {
        emit(["ok": false, "failure": "invalid_document"])
    }
default:
    fail("unknown mode \(mode)")
}
