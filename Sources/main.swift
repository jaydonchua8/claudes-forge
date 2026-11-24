import AppKit
import SwiftUI

// NotchForge: a little smith bot that lives in the MacBook notch.
// It tails ~/.notchforge/events.jsonl, which Claude Code hooks append to.

let forgeDir = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".notchforge")
let eventsURL = forgeDir.appendingPathComponent("events.jsonl")

// MARK: - Helpers

func oneLine(_ s: String, max: Int = 90) -> String {
    let parts = s.split(whereSeparator: { $0 == "\n" || $0 == "\r" || $0 == "\t" || $0 == " " })
    return String(parts.joined(separator: " ").prefix(max))
}

func baseName(_ path: String) -> String {
    let n = (path as NSString).lastPathComponent
    return n.isEmpty ? "claude" : n
}

func clock(_ t: TimeInterval) -> String {
    let s = Swift.max(0, Int(t))
    return String(format: "%d:%02d", s / 60, s % 60)
}

func spoken(_ t: TimeInterval) -> String {
    let s = Swift.max(0, Int(t))
    return s >= 60 ? "\(s / 60)m \(s % 60)s" : "\(s)s"
}

// MARK: - Model

enum Mode: Equatable { case idle, working, done, needsInput }

struct Session {
    var cwd: String
    var working: Bool
    var lastEvent: Date
    var started: Date
}

final class Forge: ObservableObject {
    @Published var mode: Mode = .idle {
        didSet { retarget() }
    }
    @Published var walking = false
    @Published var expanded = false

    // Walking between the bed and the anvil (x position in scene cells).
    static let bedX = 14.0
    static let anvilX = 21.0
    static let walkSpeed = 5.0
    var posFrom = Forge.bedX
    var posTo = Forge.bedX
    var walkStart = Date.distantPast
    private var walkGen = 0

    static func spot(_ m: Mode) -> Double {
        (m == .working || m == .needsInput) ? anvilX : bedX
    }

    func pos(_ d: Date) -> Double {
        let dist = posTo - posFrom
        let dur = abs(dist) / Forge.walkSpeed
        guard dur > 0 else { return posTo }
        let k = Swift.min(1, Swift.max(0, d.timeIntervalSince(walkStart) / dur))
        return posFrom + dist * k
    }

    private func retarget() {
        let to = Forge.spot(mode)
        guard to != posTo else { return }
        let now = Date()
        posFrom = pos(now)
        posTo = to
        walkStart = now
        walking = true
        walkGen += 1
        let g = walkGen
        let dur = abs(posTo - posFrom) / Forge.walkSpeed
        DispatchQueue.main.asyncAfter(deadline: .now() + dur + 0.05) { [weak self] in
            if self?.walkGen == g { self?.walking = false }
        }
    }
    @Published var line = ""
    @Published var lineID = 0
    @Published var lastHit = Date.distantPast
    @Published var workStart = Date()
    @Published var notchW: CGFloat = 185
    @Published var notchH: CGFloat = 32
    @Published var muted = UserDefaults.standard.bool(forKey: "muted") {
        didSet { UserDefaults.standard.set(muted, forKey: "muted") }
    }

    private var sessions: [String: Session] = [:]
    private var queue: [String] = []
    private var strikeTimer: Timer?
    private var staleTimer: Timer?
    private var collapse: DispatchWorkItem?

    init() {
        let t = Timer(timeInterval: 30, repeats: true) { [weak self] _ in self?.sweep() }
        RunLoop.main.add(t, forMode: .common)
        staleTimer = t
    }

    private var anyWorking: Bool { sessions.values.contains { $0.working } }

    // MARK: events

    func handle(_ o: [String: Any]) {
        let ev = o["hook_event_name"] as? String ?? ""
        let sid = o["session_id"] as? String ?? "?"
        let cwd = o["cwd"] as? String ?? ""
        switch ev {
        case "UserPromptSubmit":
            begin(sid, cwd)
            if let p = o["prompt"] as? String, !p.isEmpty { push("› " + p) }
        case "PreToolUse":
            begin(sid, cwd)
            let tool = o["tool_name"] as? String ?? ""
            let ti = o["tool_input"] as? [String: Any] ?? [:]
            snippets(tool, ti).forEach(push)
        case "PostToolUse":
            begin(sid, cwd)
        case "Stop":
            finish(sid, cwd)
        case "Notification":
            let type = o["notification_type"] as? String ?? ""
            let msg = (o["message"] as? String ?? "")
            let low = msg.lowercased()
            if type == "idle_prompt" || low.contains("waiting for your input") {
                quiet(sid)
            } else if type == "permission_prompt" || low.contains("permission") || low.contains("approval") {
                needs(sid, cwd)
            }
        case "SessionEnd":
            quiet(sid)
            sessions[sid] = nil
        default:
            break
        }
    }

    private func begin(_ sid: String, _ cwd: String) {
        let now = Date()
        var s = sessions[sid] ?? Session(cwd: cwd, working: false, lastEvent: now, started: now)
        if !s.working { s.started = now }
        s.working = true
        s.lastEvent = now
        if !cwd.isEmpty { s.cwd = cwd }
        sessions[sid] = s
        collapse?.cancel()
        if mode != .working {
            if mode != .needsInput {
                workStart = now
                queue.removeAll()
                line = ""
                lineID += 1
            }
            mode = .working
        }
        expanded = false
        startStrikes()
    }

    private func finish(_ sid: String, _ cwd: String) {
        guard let s = sessions[sid], s.working else { return }
        sessions[sid]?.working = false
        let name = baseName(cwd.isEmpty ? s.cwd : cwd)
        let took = spoken(Date().timeIntervalSince(s.started))
        play("Glass")
        if anyWorking {
            push("✓ \(name) finished")
            return
        }
        stopStrikes()
        mode = .done
        expanded = true
        line = "Done · \(name) · \(took)"
        lineID += 1
        let w = DispatchWorkItem { [weak self] in
            guard let self, self.mode == .done else { return }
            self.expanded = false
        }
        collapse = w
        DispatchQueue.main.asyncAfter(deadline: .now() + 10, execute: w)
    }

    private func needs(_ sid: String, _ cwd: String) {
        let name = baseName(cwd.isEmpty ? (sessions[sid]?.cwd ?? "") : cwd)
        collapse?.cancel()
        stopStrikes()
        mode = .needsInput
        expanded = true
        line = "Needs you · \(name)"
        lineID += 1
        play("Ping")
    }

    private func quiet(_ sid: String) {
        sessions[sid]?.working = false
        if mode == .working && !anyWorking {
            stopStrikes()
            mode = .idle
            expanded = false
        }
    }

    private func sweep() {
        let cutoff = Date().addingTimeInterval(-20 * 60)
        for (k, s) in sessions where s.working && s.lastEvent < cutoff { quiet(k) }
    }

    func tap() {
        switch mode {
        case .done, .needsInput:
            collapse?.cancel()
            mode = anyWorking ? .working : .idle
            expanded = false
            if mode == .working { startStrikes() }
        default:
            break
        }
    }

    func reset() {
        collapse?.cancel()
        sessions.removeAll()
        queue.removeAll()
        stopStrikes()
        mode = .idle
        expanded = false
    }

    // MARK: anvil

    private func push(_ s: String) {
        let c = oneLine(s)
        guard !c.isEmpty else { return }
        queue.append(c)
        if queue.count > 10 { queue.removeFirst(queue.count - 10) }
    }

    private func startStrikes() {
        guard strikeTimer == nil else { return }
        let t = Timer(timeInterval: 0.85, repeats: true) { [weak self] _ in self?.strike() }
        RunLoop.main.add(t, forMode: .common)
        strikeTimer = t
        strike()
    }

    private func stopStrikes() {
        strikeTimer?.invalidate()
        strikeTimer = nil
    }

    private func strike() {
        lastHit = Date()
        if !queue.isEmpty {
            line = queue.removeFirst()
            lineID += 1
        }
    }

    private func snippets(_ tool: String, _ ti: [String: Any]) -> [String] {
        let path = (ti["file_path"] ?? ti["path"] ?? ti["notebook_path"]) as? String
        let file = path.map(baseName) ?? "file"
        switch tool {
        case "Bash":
            return ["$ " + (ti["command"] as? String ?? "")]
        case "Edit", "Write", "MultiEdit", "NotebookEdit":
            var body = (ti["new_string"] ?? ti["content"] ?? ti["new_source"]) as? String ?? ""
            if body.isEmpty, let edits = ti["edits"] as? [[String: Any]] {
                body = edits.compactMap { $0["new_string"] as? String }.joined(separator: "\n")
            }
            let lines = body.split(separator: "\n")
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { $0.count > 3 }
                .prefix(3)
            return ["✎ " + file] + lines
        case "Read":
            return ["read " + file]
        case "Grep", "Glob":
            return ["find " + (ti["pattern"] as? String ?? "")]
        case "WebSearch":
            return ["search " + (ti["query"] as? String ?? "")]
        case "WebFetch":
            let u = ti["url"] as? String ?? ""
            return ["fetch " + (URL(string: u)?.host ?? u)]
        case "Task", "Agent":
            return ["agent: " + (ti["description"] as? String ?? "subtask")]
        case "TodoWrite":
            let todos = ti["todos"] as? [[String: Any]] ?? []
            let cur = todos.first { ($0["status"] as? String) == "in_progress" }
            let text = (cur?["activeForm"] ?? cur?["content"]) as? String
            return [text.map { "☐ " + $0 } ?? "updating todos"]
        default:
            if tool.hasPrefix("mcp__") {
                let p = tool.dropFirst(5).components(separatedBy: "__")
                return [p.joined(separator: " · ")]
            }
            return [tool]
        }
    }

    private func play(_ name: String) {
        guard !muted else { return }
        NSSound(named: NSSound.Name(name))?.play()
    }

    // MARK: demo

    func demo() {
        reset()
        let sid = "demo"
        let cwd = "/demo/notch-forge"
        let steps: [(Double, [String: Any])] = [
            (0.0, ["hook_event_name": "UserPromptSubmit", "prompt": "add a dark mode toggle to settings"]),
            (1.2, ["hook_event_name": "PreToolUse", "tool_name": "Grep", "tool_input": ["pattern": "ThemeProvider"]]),
            (2.4, ["hook_event_name": "PreToolUse", "tool_name": "Read", "tool_input": ["file_path": "/x/Settings.tsx"]]),
            (3.6, ["hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": [
                "file_path": "/x/Settings.tsx",
                "new_string": "const [dark, setDark] = useState(false)\n<Toggle checked={dark} onChange={setDark} />\ndocument.body.classList.toggle('dark', dark)",
            ]]),
            (7.5, ["hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": ["command": "npm test -- settings"]]),
            (9.5, ["hook_event_name": "Stop"]),
        ]
        for (delay, ev) in steps {
            var e = ev
            e["session_id"] = sid
            e["cwd"] = cwd
            DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in self?.handle(e) }
        }
    }
}

// MARK: - Event file tail

final class Tail {
    private let url: URL
    private let onObject: ([String: Any]) -> Void
    private var fh: FileHandle?
    private var inode: Int = -1
    private var buf = Data()
    private var timer: Timer?

    init(url: URL, onObject: @escaping ([String: Any]) -> Void) {
        self.url = url
        self.onObject = onObject
        let fm = FileManager.default
        try? fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        if let size = attrs()?.size, size > 2_000_000 { try? Data().write(to: url) }
        if !fm.fileExists(atPath: url.path) { fm.createFile(atPath: url.path, contents: nil) }
        open(atEnd: true)
        let t = Timer(timeInterval: 0.15, repeats: true) { [weak self] _ in self?.poll() }
        RunLoop.main.add(t, forMode: .common)
        timer = t
    }

    private func attrs() -> (size: UInt64, inode: Int)? {
        guard let a = try? FileManager.default.attributesOfItem(atPath: url.path) else { return nil }
        let size = (a[.size] as? NSNumber)?.uint64Value ?? 0
        let ino = (a[.systemFileNumber] as? NSNumber)?.intValue ?? -1
        return (size, ino)
    }

    private func open(atEnd: Bool) {
        try? fh?.close()
        fh = try? FileHandle(forReadingFrom: url)
        inode = attrs()?.inode ?? -1
        buf.removeAll()
        if atEnd { _ = try? fh?.seekToEnd() }
    }

    private func poll() {
        guard let a = attrs() else { fh = nil; return }
        if fh == nil || a.inode != inode { open(atEnd: false) }
        guard let fh else { return }
        let pos = (try? fh.offset()) ?? 0
        if a.size < pos { try? fh.seek(toOffset: 0) } else if a.size == pos { return }
        guard let d = try? fh.readToEnd(), !d.isEmpty else { return }
        buf.append(d)
        while let nl = buf.firstIndex(of: 0x0A) {
            let lineData = buf.subdata(in: buf.startIndex..<nl)
            buf.removeSubrange(buf.startIndex...nl)
            if let o = (try? JSONSerialization.jsonObject(with: lineData)) as? [String: Any] {
                onObject(o)
            }
        }
    }
}

// MARK: - Views

let clay = Color(red: 0.85, green: 0.47, blue: 0.34)
let clayDark = Color(red: 0.62, green: 0.32, blue: 0.22)
let steel = Color(red: 0.66, green: 0.69, blue: 0.74)
let steelDark = Color(red: 0.40, green: 0.42, blue: 0.48)
let hot = Color(red: 1.0, green: 0.72, blue: 0.22)
let wood = Color(red: 0.60, green: 0.44, blue: 0.28)
let okGreen = Color(red: 0.30, green: 0.85, blue: 0.48)
let amber = Color(red: 1.0, green: 0.70, blue: 0.20)

// Cave palette
let caveBack = Color(red: 0.10, green: 0.08, blue: 0.14)
let rockLight = Color(red: 0.15, green: 0.12, blue: 0.20)
let stalCol = Color(red: 0.20, green: 0.16, blue: 0.26)
let floorTop = Color(red: 0.25, green: 0.20, blue: 0.31)
let floorDark = Color(red: 0.15, green: 0.12, blue: 0.19)
let pebble = Color(red: 0.30, green: 0.24, blue: 0.36)
let crystalA = Color(red: 0.45, green: 0.85, blue: 1.0)
let crystalB = Color(red: 0.78, green: 0.56, blue: 1.0)
let moonCol = Color(red: 1.0, green: 0.88, blue: 0.52)
let bedFrame = Color(red: 0.22, green: 0.27, blue: 0.58)
let bedFrameHi = Color(red: 0.33, green: 0.39, blue: 0.74)
let sheetCol = Color(red: 0.88, green: 0.87, blue: 0.95)
let pillowCol = Color(red: 0.98, green: 0.97, blue: 1.0)
let blanket = Color(red: 0.25, green: 0.33, blue: 0.80)
let blanketHi = Color(red: 0.36, green: 0.46, blue: 0.92)
let starCol = Color(red: 1.0, green: 0.90, blue: 0.55)
let visor = Color(red: 0.13, green: 0.10, blue: 0.10)


func hash(_ a: Int, _ b: Int) -> Int {
    var h = UInt64(truncatingIfNeeded: a &* 73856093 ^ b &* 19349663)
    h ^= h >> 13; h = h &* 0x5bd1e995; h ^= h >> 15
    return Int(h % 1_000_003)
}

let caveDeep = Color(red: 0.065, green: 0.05, blue: 0.095)

// The cave that fills the whole island, on the same pixel grid as SmithView.
struct CaveBackground: View {
    let width: CGFloat
    let height: CGFloat
    let notchH: CGFloat

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 8)) { tl in
            Canvas { ctx, _ in draw(ctx, tl.date) }
        }
        .frame(width: width, height: height)
        .allowsHitTesting(false)
    }

    private func draw(_ g: GraphicsContext, _ now: Date) {
        let c = SmithView.cell
        let gy = (notchH - CGFloat(SmithView.rows) * c) / 2   // y of sprite row 0
        let gx: CGFloat = 3                                                // x of sprite col 0
        let cols = Int(width / c) + 4
        let top = -Int((gy / c).rounded(.up)) - 1
        let bottom = Int(((height - gy) / c).rounded(.up)) + 1
        let secs = now.timeIntervalSinceReferenceDate
        func r(_ x: Int, _ y: Int, _ w: Int, _ h: Int, _ col: Color) {
            g.fill(Path(CGRect(x: gx + CGFloat(x) * c, y: gy + CGFloat(y) * c, width: CGFloat(w) * c, height: CGFloat(h) * c)), with: .color(col))
        }
        func glow(_ cx: Double, _ cy: Double, _ rad: CGFloat, _ col: Color, _ a: Double) {
            let p = CGPoint(x: gx + cx * c, y: gy + cy * c)
            g.fill(Path(ellipseIn: CGRect(x: p.x - rad, y: p.y - rad, width: rad * 2, height: rad * 2)),
                   with: .radialGradient(Gradient(colors: [col.opacity(a), col.opacity(0)]), center: p, startRadius: 0, endRadius: rad))
        }

        g.fill(Path(CGRect(x: 0, y: 0, width: width, height: height)), with: .color(caveBack))

        let x0 = -2
        // back wall rock patches
        for i in x0..<cols where hash(i, 1) % 6 == 0 {
            r(i, 2 + hash(i, 2) % 10, 2 + hash(i, 3) % 3, 1 + hash(i, 4) % 2, rockLight)
        }
        // stalactites hanging from the top edge
        var last = -10
        for i in x0..<cols where hash(i, 5) % 5 == 0 && i - last > 4 {
            last = i
            let len = 2 + hash(i, 6) % 3
            r(i, top, 3, 1 - top, stalCol)
            r(i + 1, 1, 1, len - 1, stalCol)
        }
        // glowing crystals on the wall
        for i in x0..<cols where hash(i, 7) % 13 == 0 {
            let y = 4 + hash(i, 8) % 7
            let col = hash(i, 9) % 2 == 0 ? crystalA : crystalB
            let tw = 0.55 + 0.45 * sin(secs * 1.3 + Double(hash(i, 10) % 60) / 10)
            glow(Double(i) + 0.5, Double(y) + 1, 4, col, 0.3 * tw)
            r(i, y, 1, 2, col.opacity(0.55 + 0.45 * tw))
            r(i + 1, y + 1, 1, 1, col.opacity(0.4 + 0.4 * tw))
        }
        // ledge the bed and anvil stand on
        r(x0, 15, cols - x0, 1, floorTop)
        r(x0, 16, cols - x0, 2, floorDark)
        for i in x0..<cols where hash(i, 11) % 5 == 0 {
            r(i, 16 + hash(i, 12) % 2, 1 + hash(i, 13) % 2, 1, pebble)
        }
        // deeper cave under the ledge, behind the text strip
        if bottom > 18 {
            let deepTop = gy + 18 * c
            g.fill(Path(CGRect(x: 0, y: deepTop, width: width, height: height - deepTop)),
                   with: .linearGradient(Gradient(colors: [floorDark, caveDeep]),
                                         startPoint: CGPoint(x: 0, y: deepTop),
                                         endPoint: CGPoint(x: 0, y: deepTop + 3 * c)))
            for i in x0..<cols where hash(i, 14) % 4 == 0 {
                r(i, 18, 1 + hash(i, 15) % 3, 1, floorDark)
            }
            for i in x0..<cols where hash(i, 16) % 19 == 0 {
                let y = 20 + hash(i, 17) % Swift.max(1, bottom - 22)
                let tw = 0.5 + 0.5 * sin(secs * 1.7 + Double(i))
                r(i, y, 1, 1, (hash(i, 18) % 2 == 0 ? crystalA : crystalB).opacity(0.25 + 0.35 * tw))
            }
        }
    }
}

struct SmithView: View {
    @ObservedObject var forge: Forge
    static let cols = 40
    static let rows = 18
    static let cell: CGFloat = 1.5

    var body: some View {
        let fast = forge.mode == .working || forge.walking
        TimelineView(.animation(minimumInterval: fast ? 1.0 / 30 : 1.0 / 12)) { tl in
            Canvas { ctx, _ in draw(ctx, tl.date) }
        }
        .frame(width: CGFloat(Self.cols) * Self.cell, height: CGFloat(Self.rows) * Self.cell)
    }

    private func draw(_ base: GraphicsContext, _ now: Date) {
        var g = base
        let c = Self.cell
        let W = CGFloat(Self.cols) * c, H = CGFloat(Self.rows) * c
        func r(_ x: Int, _ y: Int, _ w: Int, _ h: Int, _ col: Color) {
            g.fill(Path(CGRect(x: CGFloat(x) * c, y: CGFloat(y) * c, width: CGFloat(w) * c, height: CGFloat(h) * c)), with: .color(col))
        }
        func glow(_ cx: Double, _ cy: Double, _ rad: CGFloat, _ col: Color, _ a: Double) {
            let p = CGPoint(x: cx * c, y: cy * c)
            g.fill(Path(ellipseIn: CGRect(x: p.x - rad, y: p.y - rad, width: rad * 2, height: rad * 2)),
                   with: .radialGradient(Gradient(colors: [col.opacity(a), col.opacity(0)]), center: p, startRadius: 0, endRadius: rad))
        }

        let mode = forge.mode
        let t = now.timeIntervalSince(forge.lastHit)
        let secs = now.timeIntervalSinceReferenceDate
        let p = forge.pos(now)
        let moving = abs(p - forge.posTo) > 0.01
        let ox = Int(p.rounded())
        let asleep = !moving && (mode == .idle || mode == .done)
        let atAnvil = !moving && !asleep
        let holding = atAnvil

        // moon nightlight + stars
        glow(3.5, 3.8, asleep ? 11 : 8, moonCol, asleep ? 0.22 : 0.12)
        r(3, 2, 2, 1, moonCol)
        r(2, 3, 1, 2, moonCol)
        r(3, 5, 2, 1, moonCol)
        let tw = 0.5 + 0.5 * sin(secs * 2.2)
        r(7, 2, 1, 1, starCol.opacity(0.4 + 0.6 * tw))
        r(1, 7, 1, 1, starCol.opacity(1 - 0.6 * tw))

        // crystals
        let cg = 0.55 + 0.45 * sin(secs * 1.3)
        glow(38.5, 7.5, 5, crystalA, 0.35 * cg)
        r(38, 6, 1, 2, crystalA.opacity(0.6 + 0.4 * cg))
        r(37, 7, 1, 1, crystalA.opacity(0.5 + 0.4 * cg))
        glow(16.5, 10.5, 4, crystalB, 0.3 * (1.5 - cg))
        r(16, 10, 1, 2, crystalB.opacity(0.9 - 0.3 * cg))
        r(11, 5, 1, 1, crystalA.opacity(0.4 + 0.5 * (1 - cg)))

        // forge glow
        if mode == .working && atAnvil {
            let i = 0.18 + 0.30 * Swift.max(0, 1 - t / 0.35)
            glow(33.5, 11, 22, hot, i)
        }

        // ---- bed ----
        r(1, 8, 2, 1, bedFrameHi)
        r(1, 9, 2, 5, bedFrame)
        r(13, 10, 1, 4, bedFrame)
        r(13, 10, 1, 1, bedFrameHi)
        r(1, 13, 13, 1, bedFrame)
        r(2, 14, 1, 1, bedFrame)
        r(12, 14, 1, 1, bedFrame)
        r(3, 12, 10, 1, sheetCol)
        r(3, 10, 3, 2, pillowCol)

        if asleep {
            // head on the pillow
            r(3, 7, 1, 1, steelDark)
            r(3, 8, 4, 3, clay)
            r(4, 9, 3, 1, visor)
            let eye: Color = mode == .done ? okGreen : Color(red: 1, green: 0.95, blue: 0.82)
            r(4, 9, 1, 1, eye.opacity(0.4))
            r(6, 9, 1, 1, eye.opacity(0.4))
            // blanket, breathing
            let breath = sin(secs * 1.9) > -0.35
            r(6, 11, 7, 2, blanket)
            if breath { r(7, 10, 5, 1, blanket) }
            r(6, 11, 1, 1, sheetCol)
            r(8, breath ? 10 : 11, 1, 1, blanketHi)
            r(10, 12, 1, 1, starCol)
            r(12, 11, 1, 1, starCol)
            // zzz
            for k in 0..<3 {
                let ph = (secs * 0.42 + Double(k) / 3).truncatingRemainder(dividingBy: 1)
                let a = sin(ph * .pi)
                g.draw(Text("z").font(.system(size: 4 + ph * 3.5, weight: .heavy, design: .rounded))
                    .foregroundColor(Color(red: 0.78, green: 0.84, blue: 1).opacity(a * 0.95)),
                       at: CGPoint(x: (7.5 + ph * 7) * c, y: (7.5 - ph * 6.5) * c))
            }
        } else {
            // empty bed, blanket folded at the foot
            r(8, 11, 5, 2, blanket)
            r(8, 11, 5, 1, blanketHi)
            r(10, 12, 1, 1, starCol)
        }

        // ---- anvil ----
        r(30, 12, 8, 1, steel)
        r(38, 12, 1, 1, steel)
        r(32, 13, 4, 1, steelDark)
        r(31, 14, 6, 1, steel)
        if mode == .working && atAnvil {
            r(31, 11, 5, 1, t < 0.2 ? Color(red: 1, green: 0.93, blue: 0.6) : hot)
        }
        if !holding {
            r(31, 11, 3, 1, wood)
            r(34, 10, 2, 2, steel)
        }

        guard !asleep else { return }

        // ---- robot (standing or walking) ----
        let flip = moving && forge.posTo < forge.posFrom
        func s(_ x: Int, _ y: Int, _ w: Int, _ h: Int, _ col: Color) {
            let lx = flip ? 12 - x - w : x
            r(lx + ox - 3, y + 2, w, h, col)
        }
        let eye: Color = mode == .done ? okGreen : (mode == .needsInput ? amber : Color(red: 1, green: 0.95, blue: 0.82))
        let tip: Color = mode == .done ? okGreen : (mode == .needsInput ? amber : hot)
        let tipOn = mode == .working ? true : Int(secs * 2.5) % 2 == 0

        if moving {
            let ph = Int(secs * 8) % 2
            let bob = ph
            s(5, 1 + bob, 1, 1, tipOn ? tip : steelDark)
            s(5, 2 + bob, 1, 1, steelDark)
            s(3, 3 + bob, 6, 4, clay)
            s(5, 4 + bob, 4, 2, visor)
            s(6, 4 + bob, 1, 2, eye)
            s(8, 4 + bob, 1, 2, eye)
            s(4, 7 + bob, 4, 4 - bob, clayDark)
            s(5, 8 + bob, 2, 1, clay)
            s(ph == 0 ? 8 : 3, 8 + bob, 1, 2, clay)
            if ph == 0 {
                s(4, 11, 1, 2, steelDark); s(7, 11, 1, 2, steelDark)
            } else {
                s(5, 11, 1, 2, steelDark); s(6, 11, 1, 2, steelDark)
            }
            return
        }

        // at the anvil: 0 = hammer down, 1 = mid, 2 = up
        let fr: Int = mode == .working ? (t < 0.14 ? 0 : (t < 0.28 ? 1 : (t < 0.68 ? 2 : 1))) : 0
        let bob = (mode == .working && fr == 0) ? 1 : 0
        let blink = mode == .working && Int(secs * 2) % 9 == 0
        s(5, 1 + bob, 1, 1, tipOn ? tip : steelDark)
        s(5, 2 + bob, 1, 1, steelDark)
        s(3, 3 + bob, 6, 4, clay)
        s(5, 4 + bob, 4, 2, visor)
        if blink {
            s(6, 5 + bob, 1, 1, eye.opacity(0.45)); s(8, 5 + bob, 1, 1, eye.opacity(0.45))
        } else {
            s(6, 4 + bob, 1, 2, eye); s(8, 4 + bob, 1, 2, eye)
        }
        s(4, 7 + bob, 4, 4 - bob, clayDark)
        s(5, 8 + bob, 2, 1, clay)
        s(4, 11, 1, 2, steelDark)
        s(7, 11, 1, 2, steelDark)
        switch fr {
        case 0:
            s(8, 8, 3, 1, clay); s(11, 8, 2, 1, wood); s(13, 7, 2, 2, steel)
        case 1:
            s(8, 8, 1, 1, clay); s(9, 7, 1, 1, clay)
            s(10, 6, 1, 1, wood); s(11, 5, 1, 1, wood)
            s(12, 3, 2, 2, steel)
        default:
            s(8, 8, 1, 1, clay); s(9, 7, 1, 1, clay); s(10, 6, 1, 1, clay)
            s(10, 4, 1, 2, wood)
            s(9, 1, 3, 2, steel)
        }

        // sparks
        if mode == .working && t >= 0 && t < 0.38 {
            let k = t / 0.38
            let dirs: [(Double, Double)] = [(-1.6, -2.2), (0.8, -3.4), (2.6, -2.4), (3.8, -1.2), (-0.6, -3.6), (1.8, -1.0)]
            for (i, d) in dirs.enumerated() {
                let x = 33.0 + d.0 * k * 1.7
                let y = 10.5 + d.1 * k * 1.7 + 3.0 * k * k
                let col = i % 2 == 0 ? hot : Color(red: 1, green: 0.9, blue: 0.55)
                g.fill(Path(CGRect(x: x * c, y: y * c, width: c, height: c)), with: .color(col.opacity(1 - k)))
            }
        }
    }
}

struct SparkLine: View {
    let text: String
    let rest: Color
    @State private var isHot = true

    var body: some View {
        Text(text)
            .font(.system(size: 11, weight: .medium, design: .monospaced))
            .lineLimit(1)
            .truncationMode(.tail)
            .foregroundStyle(isHot ? hot : rest)
            .scaleEffect(isHot ? 1.06 : 1, anchor: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .onAppear { withAnimation(.easeOut(duration: 0.55)) { isHot = false } }
    }
}

struct RightEar: View {
    @ObservedObject var forge: Forge

    var body: some View {
        switch forge.mode {
        case .working:
            TimelineView(.periodic(from: forge.workStart, by: 1)) { tl in
                Text(clock(tl.date.timeIntervalSince(forge.workStart)))
                    .font(.system(size: 10.5, weight: .semibold, design: .monospaced))
                    .foregroundStyle(hot.opacity(0.95))
            }
        case .done:
            Image(systemName: "checkmark.circle.fill")
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(okGreen)
        case .needsInput:
            Image(systemName: "exclamationmark.circle.fill")
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(amber)
        case .idle:
            Circle().fill(Color.white.opacity(0.22)).frame(width: 4, height: 4)
        }
    }
}

struct IslandView: View {
    @ObservedObject var forge: Forge

    var body: some View {
        let big = forge.mode == .working || forge.expanded
        let earL: CGFloat = 66
        let ear: CGFloat = big ? 54 : 46
        let stripH: CGFloat = 26
        let w = forge.notchW + earL + ear
        let h = forge.notchH + (big ? stripH : 0)
        let accent: Color? = forge.mode == .done ? okGreen : (forge.mode == .needsInput ? amber : nil)
        let shape = UnevenRoundedRectangle(bottomLeadingRadius: big ? 14 : 10, bottomTrailingRadius: big ? 14 : 10, style: .continuous)

        ZStack(alignment: .top) {
            shape.fill(Color.black)
            CaveBackground(width: w, height: h, notchH: forge.notchH)
                .clipShape(shape)
            if let accent, forge.expanded {
                shape.stroke(accent.opacity(0.9), lineWidth: 1.5)
                    .shadow(color: accent.opacity(0.9), radius: 8)
                    .mask(alignment: .top) {
                        Rectangle().padding(.top, forge.notchH).padding(.horizontal, -30).padding(.bottom, -30)
                    }
            }
            VStack(spacing: 0) {
                HStack(spacing: 0) {
                    SmithView(forge: forge)
                        .frame(width: earL, height: forge.notchH)
                        .clipShape(UnevenRoundedRectangle(bottomLeadingRadius: big ? 0 : 10, style: .continuous))
                    Color.clear.frame(width: forge.notchW, height: forge.notchH)
                    RightEar(forge: forge)
                        .frame(width: ear, height: forge.notchH)
                }
                if big {
                    ZStack(alignment: .leading) {
                        if forge.line.isEmpty {
                            Text("heating the forge…")
                                .font(.system(size: 11, weight: .medium, design: .monospaced))
                                .foregroundStyle(Color.white.opacity(0.4))
                                .frame(maxWidth: .infinity, alignment: .leading)
                        } else {
                            SparkLine(text: forge.line, rest: accent ?? Color.white.opacity(0.92))
                                .id(forge.lineID)
                                .transition(.asymmetric(
                                    insertion: .move(edge: .leading).combined(with: .opacity),
                                    removal: .move(edge: .top).combined(with: .opacity)))
                        }
                    }
                    .padding(.horizontal, 14)
                    .padding(.bottom, 4)
                    .frame(width: w, height: stripH)
                    .clipped()
                    .animation(.spring(response: 0.28, dampingFraction: 0.8), value: forge.lineID)
                    .transition(.opacity)
                }
            }
        }
        .frame(width: w, height: h)
        .contentShape(Rectangle())
        .onTapGesture { forge.tap() }
        .offset(x: (ear - earL) / 2)
        .animation(.spring(response: 0.4, dampingFraction: 0.78), value: big)
        .animation(.easeInOut(duration: 0.25), value: forge.mode)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }
}

// MARK: - Window

final class IslandPanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
}

final class Host: NSHostingView<IslandView> {
    var menuProvider: (() -> NSMenu)?
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
    override func rightMouseDown(with event: NSEvent) {
        if let m = menuProvider?() { NSMenu.popUpContextMenu(m, with: event, for: self) }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    let forge = Forge()
    var panel: IslandPanel!
    var tail: Tail?

    func applicationDidFinishLaunching(_ note: Notification) {
        if let id = Bundle.main.bundleIdentifier,
           NSRunningApplication.runningApplications(withBundleIdentifier: id).count > 1 {
            exit(0)
        }
        panel = IslandPanel(contentRect: NSRect(x: 0, y: 0, width: 560, height: 130),
                            styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
        panel.level = NSWindow.Level(rawValue: NSWindow.Level.mainMenu.rawValue + 3)
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = false
        panel.hidesOnDeactivate = false
        panel.isMovable = false
        panel.collectionBehavior = [.canJoinAllSpaces, .stationary, .fullScreenAuxiliary, .ignoresCycle]
        let host = Host(rootView: IslandView(forge: forge))
        host.menuProvider = { [weak self] in self?.menu() ?? NSMenu() }
        panel.contentView = host
        place()
        panel.orderFrontRegardless()
        NotificationCenter.default.addObserver(self, selector: #selector(place),
                                               name: NSApplication.didChangeScreenParametersNotification, object: nil)
        tail = Tail(url: eventsURL) { [weak self] o in self?.forge.handle(o) }
        if CommandLine.arguments.contains("--demo") { forge.demo() }
    }

    @objc func place() {
        let screens = NSScreen.screens
        guard let screen = screens.first(where: { $0.safeAreaInsets.top > 0 }) ?? NSScreen.main ?? screens.first else { return }
        let f = screen.frame
        if screen.safeAreaInsets.top > 0 {
            forge.notchH = screen.safeAreaInsets.top
            if let l = screen.auxiliaryTopLeftArea, let r = screen.auxiliaryTopRightArea {
                forge.notchW = max(120, f.width - l.width - r.width)
            } else {
                forge.notchW = 185
            }
        } else {
            forge.notchH = max(24, f.maxY - screen.visibleFrame.maxY)
            forge.notchW = 150
        }
        let size = panel.frame.size
        panel.setFrame(NSRect(x: f.midX - size.width / 2, y: f.maxY - size.height, width: size.width, height: size.height), display: true)
    }

    func menu() -> NSMenu {
        let m = NSMenu()
        func add(_ title: String, _ sel: Selector, on: Bool = false) {
            let i = NSMenuItem(title: title, action: sel, keyEquivalent: "")
            i.target = self
            i.state = on ? .on : .off
            m.addItem(i)
        }
        add("Run demo", #selector(runDemo))
        add("Mute sounds", #selector(toggleMute), on: forge.muted)
        add("Reset", #selector(resetForge))
        m.addItem(.separator())
        add("Quit NotchForge", #selector(quit))
        return m
    }

    @objc func runDemo() { forge.demo() }
    @objc func toggleMute() { forge.muted.toggle() }
    @objc func resetForge() { forge.reset() }
    @objc func quit() { exit(0) }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
