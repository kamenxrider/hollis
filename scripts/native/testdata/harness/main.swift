// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
// Test-only driver for native/Extraction.swift. It builds synthetic PDFs and
// prints extraction results as one JSON document. It never imports
// FoundationModels: no model is created, no session exists, no prompt is sent.
import CoreGraphics
import CoreText
import Foundation
import PDFKit

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

func makePDF(path: String, pages: Int, text: String, scan: Bool) {
    var box = mediaBox
    guard let context = CGContext(URL(fileURLWithPath: path) as CFURL, mediaBox: &box, nil) else {
        fail("could not create PDF")
    }
    for index in 1...max(1, pages) {
        context.beginPDFPage(nil)
        context.setFillColor(gray: 1, alpha: 1)
        context.fill(mediaBox)
        context.setFillColor(gray: 0, alpha: 1)
        if scan {
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

func makeRotatedPDF(path: String, text: String) {
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
    document.page(at: 0)?.rotation = 90
    document.write(to: URL(fileURLWithPath: path))
}

func parsePages(_ value: String?) -> [Int]? {
    guard let value, !value.isEmpty else { return nil }
    return value.split(separator: ",").compactMap { Int($0) }
}

let mode = arguments[1]
switch mode {
case "make-text-pdf":
    makePDF(path: arguments[2], pages: Int(arguments[3]) ?? 1, text: arguments[4], scan: false)
    emit(["ok": true])
case "make-long-pdf":
    makeLongPDF(path: arguments[2], pages: Int(arguments[3]) ?? 1,
                lines: Int(arguments[4]) ?? 1, text: arguments[5])
    emit(["ok": true])
case "make-scan-pdf":
    makePDF(path: arguments[2], pages: Int(arguments[3]) ?? 1, text: arguments[4], scan: true)
    emit(["ok": true])
case "make-blank-pdf":
    makeBlankPDF(path: arguments[2])
    emit(["ok": true])
case "make-rotated-pdf":
    makeRotatedPDF(path: arguments[2], text: arguments[3])
    emit(["ok": true])
case "extract", "detect":
    let path = arguments[2]
    let pages = arguments.count > 3 ? parsePages(arguments[3]) : nil
    do {
        if mode == "detect" {
            let data = try Data(contentsOf: URL(fileURLWithPath: path))
            emit(["ok": true, "media": try Extraction.detect(data).rawValue])
        } else {
            let document = try Extraction.extract(path: path, pages: pages)
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
