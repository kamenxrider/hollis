// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
// Test-only driver for native/Extraction.swift. It builds synthetic PDFs and
// prints extraction results as one JSON document. It never imports
// FoundationModels: no model is created, no session exists, no prompt is sent.
import CoreGraphics
import CoreML
import CoreText
import Foundation
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

func diagnostic(_ payload: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
    FileHandle.standardError.write(Data("HOLLIS_OCR_DIAGNOSTIC ".utf8) + data + Data([10]))
}

// Error descriptions and arbitrary userInfo can carry paths or document text.
// Only bounded identifier-style domains and numeric codes leave the harness.
func errorChain(_ error: Error) -> [[String: Any]] {
    let domainPattern = "\\A[A-Za-z][A-Za-z0-9_-]*(\\.[A-Za-z][A-Za-z0-9_-]*)*\\z"
    var chain: [[String: Any]] = []
    var current: NSError? = error as NSError
    var seen = Set<ObjectIdentifier>()
    while let item = current, chain.count < 8, seen.insert(ObjectIdentifier(item)).inserted {
        let safeDomain = item.domain.utf8.count <= 128 &&
            item.domain.range(of: domainPattern, options: .regularExpression) != nil
        chain.append(["domain": safeDomain ? item.domain : "redacted",
                      "code": item.code])
        current = item.userInfo[NSUnderlyingErrorKey] as? NSError
    }
    return chain
}

func imageProperties(_ image: CGImage) -> [String: Any] {
    var properties: [String: Any] = [
        "width": image.width, "height": image.height,
        "bits_per_component": image.bitsPerComponent, "bits_per_pixel": image.bitsPerPixel,
        "bytes_per_row": image.bytesPerRow, "alpha": image.alphaInfo.rawValue,
        "color_space_model": image.colorSpace?.model.rawValue ?? -1,
    ]
    if image.bitsPerComponent == 8, image.bitsPerPixel == 8,
       image.colorSpace?.model == .monochrome, let data = image.dataProvider?.data,
       CFDataGetLength(data) >= image.bytesPerRow * image.height,
       let bytes = CFDataGetBytePtr(data) {
        var count = 0
        for y in 0..<image.height {
            for x in 0..<image.width where bytes[y * image.bytesPerRow + x] < 250 {
                count += 1
            }
        }
        properties["nonwhite_pixels"] = count
    }
    return properties
}

// SDK-defined domain constants and enum cases only; never stringify an error.
func errorIdentityChain(_ error: Error) -> [[String: Any]] {
    let domains = [
        VNErrorDomain, "VNErrorDomain", "com.apple.Vision", "Vision.VisionError",
        MLModelErrorDomain, "com.apple.CoreML", NSCocoaErrorDomain,
        NSOSStatusErrorDomain, NSPOSIXErrorDomain, NSMachErrorDomain,
    ]
    var chain: [[String: Any]] = []
    var current: Error? = error
    var seen: [NSError] = []
    while let value = current, chain.count < 8 {
        let item = value as NSError
        guard !seen.contains(where: { $0 === item }) else { break }
        seen.append(item)
        var swiftCase = "unlisted"
        if let vision = value as? VisionError {
            switch vision {
            case .dataUnavailable: swiftCase = "VisionError.dataUnavailable"
            case .internalError: swiftCase = "VisionError.internalError"
            case .invalidArgument: swiftCase = "VisionError.invalidArgument"
            case .invalidFormat: swiftCase = "VisionError.invalidFormat"
            case .invalidImage: swiftCase = "VisionError.invalidImage"
            case .invalidModel: swiftCase = "VisionError.invalidModel"
            case .invalidOperation: swiftCase = "VisionError.invalidOperation"
            case .ioError: swiftCase = "VisionError.ioError"
            case .operationFailed: swiftCase = "VisionError.operationFailed"
            case .outOfBoundsError: swiftCase = "VisionError.outOfBoundsError"
            case .outOfMemory: swiftCase = "VisionError.outOfMemory"
            case .pixelBufferCreationFailed: swiftCase = "VisionError.pixelBufferCreationFailed"
            case .requestCancelled: swiftCase = "VisionError.requestCancelled"
            case .timeout: swiftCase = "VisionError.timeout"
            case .timeStampNotFound: swiftCase = "VisionError.timeStampNotFound"
            case .unsupportedComputeDevice: swiftCase = "VisionError.unsupportedComputeDevice"
            case .unsupportedComputeStage: swiftCase = "VisionError.unsupportedComputeStage"
            case .unsupportedRequest: swiftCase = "VisionError.unsupportedRequest"
            case .unsupportedRevision: swiftCase = "VisionError.unsupportedRevision"
            @unknown default: swiftCase = "VisionError.unlisted"
            }
        }
        chain.append([
            "domain_match": domains.first(where: { $0 == item.domain }) ?? "unlisted",
            "domain_bytes": item.domain.utf8.count,
            "domain_ascii": item.domain.utf8.allSatisfy { $0 < 128 },
            "swift_case": swiftCase, "code": item.code,
        ])
        current = item.userInfo[NSUnderlyingErrorKey] as? Error
    }
    return chain
}

enum ControlFailure: Error { case cpuUnavailable }

// Each control runs in its own process, after the unchanged extraction tests.
// A control result is telemetry, never a replacement for an extraction result.
func diagnosticControl(path: String, fixture: String, configuration: String) {
    guard ["scan", "rotated", "blank"].contains(fixture),
          ["production", "cpu_only", "fast"].contains(configuration) else {
        fail("unknown control")
    }
    var record: [String: Any] = [
        "stage": "control_result", "fixture": fixture,
        "configuration": configuration, "phase": "render",
    ]
    diagnostic(["stage": "control_begin", "fixture": fixture,
                "configuration": configuration])
    guard let document = PDFDocument(url: URL(fileURLWithPath: path)),
          let page = document.page(at: 0), let image = Extraction.OCR.vision.render(page) else {
        record["ok"] = false
        diagnostic(record)
        return
    }
    record["image"] = imageProperties(image)
    do {
        let lineCount: Int
        if configuration == "production" {
            record["phase"] = "recognize"
            lineCount = try Extraction.OCR.vision.recognize(image).count
        } else {
            record["phase"] = "configure"
            let request = VNRecognizeTextRequest()
            request.recognitionLevel = configuration == "fast" ? .fast : .accurate
            if configuration == "cpu_only" {
                let stages = try request.supportedComputeStageDevices
                guard !stages.isEmpty else { throw ControlFailure.cpuUnavailable }
                var configuredStages: [String] = []
                for (stage, devices) in stages {
                    guard let cpu = devices.first(where: {
                        if case .cpu = $0 { return true }
                        return false
                    }) else { throw ControlFailure.cpuUnavailable }
                    request.setComputeDevice(cpu, for: stage)
                    configuredStages.append(stage == .main ? "main" :
                        (stage == .postProcessing ? "post_processing" : "other"))
                }
                record["cpu_stages"] = configuredStages.sorted()
            }
            record["phase"] = "recognize"
            try VNImageRequestHandler(cgImage: image, options: [:]).perform([request])
            lineCount = (request.results ?? []).compactMap { $0.topCandidates(1).first }.count
        }
        record["ok"] = true
        record["line_count"] = lineCount
    } catch {
        record["ok"] = false
        record["identity"] = errorIdentityChain(error)
        if case ControlFailure.cpuUnavailable = error {
            record["control_failure"] = "cpu_unavailable"
        }
    }
    diagnostic(record)
}

func languageTag(_ value: String) -> String {
    value.range(of: "^[A-Za-z]{2,8}([_-][A-Za-z0-9]{1,8})*$",
                options: .regularExpression) != nil ? value : "redacted"
}

func visionEnvironment() {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    diagnostic(["stage": "vision", "revision": request.revision,
                "supported_revisions": Array(VNRecognizeTextRequest.supportedRevisions),
                "recognition_languages": request.recognitionLanguages.map(languageTag),
                "locale": languageTag(Locale.current.language.maximalIdentifier),
                "preferred_languages": Locale.preferredLanguages.map(languageTag)])
    do {
        diagnostic(["stage": "supported_languages",
                    "languages": try request.supportedRecognitionLanguages().map(languageTag)])
    } catch {
        diagnostic(["stage": "supported_languages", "errors": errorChain(error)])
    }
    do {
        let stages = try request.supportedComputeStageDevices
        for (stage, devices) in stages {
            let names = devices.map { device -> String in
                switch device {
                case .cpu: return "cpu"
                case .gpu: return "gpu"
                case .neuralEngine: return "neural_engine"
                @unknown default: return "unknown"
                }
            }.sorted()
            diagnostic(["stage": "supported_devices",
                        "compute_stage": stage == .main ? "main" :
                            (stage == .postProcessing ? "post_processing" : "unknown"),
                        "devices": names])
        }
    } catch {
        diagnostic(["stage": "supported_devices", "errors": errorChain(error)])
    }
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
    let render = ocr.render
    let recognize = ocr.recognize
    ocr.render = { page in
        diagnostic(["stage": "render_begin"])
        let image = render(page)
        var record: [String: Any] = ["stage": "render_end", "ok": image != nil]
        if let image { record["image"] = imageProperties(image) }
        diagnostic(record)
        return image
    }
    ocr.recognize = { image in
        diagnostic(["stage": "recognize_begin"])
        // Query metadata only after the real call, never as an OCR warm-up.
        defer { if fault == nil || fault == "" { visionEnvironment() } }
        do {
            let lines = try recognize(image)
            diagnostic(["stage": "recognize_end", "ok": true, "line_count": lines.count])
            return lines
        } catch {
            diagnostic(["stage": "recognize_end", "ok": false, "errors": errorChain(error),
                        "identity": errorIdentityChain(error)])
            throw error
        }
    }
    return ocr
}

let mode = arguments[1]
switch mode {
case "diagnostic-control":
    diagnosticControl(path: arguments[2], fixture: arguments[3], configuration: arguments[4])
    emit(["ok": true])
case "diagnostic-errors":
    var error = NSError(domain: NSOSStatusErrorDomain, code: -6662,
                        userInfo: [NSLocalizedDescriptionKey: "private diagnostic text"])
    for code in 0..<10 {
        error = NSError(domain: NSCocoaErrorDomain, code: code,
                        userInfo: [NSUnderlyingErrorKey: error])
    }
    error = NSError(domain: "private/domain", code: 42,
                    userInfo: [NSUnderlyingErrorKey: error,
                               NSLocalizedDescriptionKey: "private diagnostic text"])
    diagnostic(["stage": "error_sanitizer", "errors": errorChain(error)])
    diagnostic(["stage": "error_sanitizer",
                "errors": errorChain(NSError(domain: NSOSStatusErrorDomain, code: -6662))])
    emit(["ok": true])
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
    makeRotatedPDF(path: arguments[2], text: arguments[3])
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
