// Draws the app icon, assets/app/Conn.icns: the Conn mark (the squared C with a pill in its
// opening, the drawing in src/lib/theme/brandMark.ts and public/favicon.svg) in teal on a black
// tile. Run from the repository root on macOS:
//
//	swift scripts/make-app-icon.swift
//
// Every size is drawn from the shapes, never scaled from another size, so the edges stay sharp.
// The tile is 824 of the 1024 grid with the rest left clear, the size macOS gives its own icons.
import AppKit

let tileHex = (red: 0.0, green: 0.0, blue: 0.0)
// The app's Teal accent in dark mode, oklch(0.78 0.12 195), as sRGB.
let markHex = (red: 59.0 / 255, green: 207.0 / 255, blue: 207.0 / 255)

let gridSize = 1024.0
let tileSize = 824.0
let tileRadius = 185.0
// How much of the tile's width the mark fills.
let markFill = 0.58

// The mark on the favicon's 32 grid, and the box its strokes cover.
let strokeWidth = 3.6
let markBox = CGRect(x: 7.5 - strokeWidth / 2, y: 7.5 - strokeWidth / 2,
                     width: 28.9 - (7.5 - strokeWidth / 2), height: 24.5 + strokeWidth / 2 - (7.5 - strokeWidth / 2))

func markPath() -> CGPath {
    let path = CGMutablePath()
    path.move(to: CGPoint(x: 25.5, y: 7.5))
    path.addLine(to: CGPoint(x: 13, y: 7.5))
    path.addArc(tangent1End: CGPoint(x: 7.5, y: 7.5), tangent2End: CGPoint(x: 7.5, y: 13), radius: 5.5)
    path.addLine(to: CGPoint(x: 7.5, y: 19))
    path.addArc(tangent1End: CGPoint(x: 7.5, y: 24.5), tangent2End: CGPoint(x: 13, y: 24.5), radius: 5.5)
    path.addLine(to: CGPoint(x: 25.5, y: 24.5))

    return path
}

func draw(pixels: Int, into context: CGContext) {
    let scale = Double(pixels) / gridSize
    context.scaleBy(x: scale, y: scale)

    let tile = CGRect(x: (gridSize - tileSize) / 2, y: (gridSize - tileSize) / 2,
                      width: tileSize, height: tileSize)
    context.setFillColor(CGColor(red: tileHex.red, green: tileHex.green, blue: tileHex.blue, alpha: 1))
    context.addPath(CGPath(roundedRect: tile, cornerWidth: tileRadius, cornerHeight: tileRadius,
                           transform: nil))
    context.fillPath()

    // The grid has y down and the context y up, so the mark is flipped as it is placed, centred on
    // the box its strokes cover.
    let unit = tileSize * markFill / Double(markBox.width)
    context.saveGState()
    context.translateBy(x: gridSize / 2, y: gridSize / 2)
    context.scaleBy(x: unit, y: -unit)
    context.translateBy(x: -Double(markBox.midX), y: -Double(markBox.midY))

    let color = CGColor(red: markHex.red, green: markHex.green, blue: markHex.blue, alpha: 1)
    context.setStrokeColor(color)
    context.setFillColor(color)
    context.setLineWidth(strokeWidth)
    context.setLineCap(.round)
    context.setLineJoin(.round)
    context.addPath(markPath())
    context.strokePath()
    context.addPath(CGPath(roundedRect: CGRect(x: 22.5, y: 13.6, width: 6.4, height: 4.8),
                           cornerWidth: 2.4, cornerHeight: 2.4, transform: nil))
    context.fillPath()
    context.restoreGState()
}

func png(pixels: Int) -> Data {
    guard let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8,
        samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
        bytesPerRow: 0, bitsPerPixel: 0),
        let graphics = NSGraphicsContext(bitmapImageRep: bitmap)
    else {
        fatalError("no bitmap for \(pixels) pixels")
    }

    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = graphics
    draw(pixels: pixels, into: graphics.cgContext)
    NSGraphicsContext.restoreGraphicsState()

    guard let data = bitmap.representation(using: .png, properties: [:]) else {
        fatalError("no png for \(pixels) pixels")
    }

    return data
}

let work = FileManager.default.temporaryDirectory.appendingPathComponent("conn-icon-\(getpid())")
let iconset = work.appendingPathComponent("Conn.iconset")
try FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)

// The names iconutil reads: each size, and its @2x at twice the pixels.
for size in [16, 32, 128, 256, 512] {
    try png(pixels: size).write(to: iconset.appendingPathComponent("icon_\(size)x\(size).png"))
    try png(pixels: size * 2).write(to: iconset.appendingPathComponent("icon_\(size)x\(size)@2x.png"))
}

let task = Process()
task.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
task.arguments = ["--convert", "icns", "--output", "assets/app/Conn.icns", iconset.path]
try task.run()
task.waitUntilExit()
try FileManager.default.removeItem(at: work)
exit(task.terminationStatus)
