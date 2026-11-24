package main

// The state machine: reads Claude Code hook events and decides what the bot is doing.
// Ported from the macOS app. Everything is driven by Tick(now) from the frame timer.

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

type Mode int

const (
	Idle Mode = iota
	Working
	Done
	NeedsInput
)

const (
	bedX      = 14.0
	anvilX    = 21.0
	walkSpeed = 5.0 // cells per second
)

type session struct {
	cwd     string
	working bool
	last    time.Time
	started time.Time
}

type pending struct {
	at time.Time
	ev map[string]any
}

type Forge struct {
	Mode      Mode
	Expanded  bool
	Line      string
	LineAt    time.Time
	PrevLine  string
	LastHit   time.Time
	WorkStart time.Time
	Muted     bool
	Play      func(sound string) // "done" or "ping"

	sessions   map[string]*session
	queue      []string
	striking   bool
	nextStrike time.Time
	collapseAt time.Time
	lastSweep  time.Time
	demo       []pending

	posFrom, posTo float64
	walkStart      time.Time
}

func NewForge() *Forge {
	return &Forge{sessions: map[string]*session{}, posFrom: bedX, posTo: bedX}
}

func spot(m Mode) float64 {
	if m == Working || m == NeedsInput {
		return anvilX
	}
	return bedX
}

func (f *Forge) setMode(m Mode, now time.Time) {
	f.Mode = m
	to := spot(m)
	if to == f.posTo {
		return
	}
	f.posFrom = f.Pos(now)
	f.posTo = to
	f.walkStart = now
}

// Pos is the bot's x position in scene cells.
func (f *Forge) Pos(now time.Time) float64 {
	dist := f.posTo - f.posFrom
	dur := math.Abs(dist) / walkSpeed
	if dur <= 0 {
		return f.posTo
	}
	k := now.Sub(f.walkStart).Seconds() / dur
	k = math.Max(0, math.Min(1, k))
	return f.posFrom + dist*k
}

func (f *Forge) Walking(now time.Time) bool { return math.Abs(f.Pos(now)-f.posTo) > 0.01 }
func (f *Forge) WalkingLeft() bool          { return f.posTo < f.posFrom }

func (f *Forge) anyWorking() bool {
	for _, s := range f.sessions {
		if s.working {
			return true
		}
	}
	return false
}

func str(o map[string]any, k string) string {
	s, _ := o[k].(string)
	return s
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 90 {
		r = r[:90]
	}
	return string(r)
}

func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "claude"
	}
	return p
}

func spoken(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s >= 60 {
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

func Clock(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Handle one hook event.
func (f *Forge) Handle(o map[string]any, now time.Time) {
	ev := str(o, "hook_event_name")
	sid := str(o, "session_id")
	if sid == "" {
		sid = "?"
	}
	cwd := str(o, "cwd")
	switch ev {
	case "UserPromptSubmit":
		f.begin(sid, cwd, now)
		if p := str(o, "prompt"); p != "" {
			f.push("› " + p)
		}
	case "PreToolUse":
		f.begin(sid, cwd, now)
		ti, _ := o["tool_input"].(map[string]any)
		if ti == nil {
			ti = map[string]any{}
		}
		for _, s := range snippets(str(o, "tool_name"), ti) {
			f.push(s)
		}
	case "PostToolUse":
		f.begin(sid, cwd, now)
	case "Stop":
		f.finish(sid, cwd, now)
	case "Notification":
		typ := str(o, "notification_type")
		low := strings.ToLower(str(o, "message"))
		if typ == "idle_prompt" || strings.Contains(low, "waiting for your input") {
			f.quiet(sid, now)
		} else if typ == "permission_prompt" || strings.Contains(low, "permission") || strings.Contains(low, "approval") {
			f.needs(sid, cwd, now)
		}
	case "SessionEnd":
		f.quiet(sid, now)
		delete(f.sessions, sid)
	}
}

func (f *Forge) setLine(s string, now time.Time) {
	f.PrevLine = f.Line
	f.Line = s
	f.LineAt = now
}

func (f *Forge) begin(sid, cwd string, now time.Time) {
	s := f.sessions[sid]
	if s == nil {
		s = &session{cwd: cwd, started: now}
		f.sessions[sid] = s
	}
	if !s.working {
		s.started = now
	}
	s.working = true
	s.last = now
	if cwd != "" {
		s.cwd = cwd
	}
	f.collapseAt = time.Time{}
	if f.Mode != Working {
		if f.Mode != NeedsInput {
			f.WorkStart = now
			f.queue = nil
			f.Line, f.PrevLine, f.LineAt = "", "", now
		}
		f.setMode(Working, now)
	}
	f.Expanded = false
	f.startStrikes(now)
}

func (f *Forge) finish(sid, cwd string, now time.Time) {
	s := f.sessions[sid]
	if s == nil || !s.working {
		return
	}
	s.working = false
	name := cwd
	if name == "" {
		name = s.cwd
	}
	name = baseName(name)
	took := spoken(now.Sub(s.started))
	f.play("done")
	if f.anyWorking() {
		f.push(name + " finished")
		return
	}
	f.striking = false
	f.setMode(Done, now)
	f.Expanded = true
	f.setLine("Done · "+name+" · "+took, now)
	f.collapseAt = now.Add(10 * time.Second)
}

func (f *Forge) needs(sid, cwd string, now time.Time) {
	name := cwd
	if name == "" && f.sessions[sid] != nil {
		name = f.sessions[sid].cwd
	}
	f.collapseAt = time.Time{}
	f.striking = false
	f.setMode(NeedsInput, now)
	f.Expanded = true
	f.setLine("Needs you · "+baseName(name), now)
	f.play("ping")
}

func (f *Forge) quiet(sid string, now time.Time) {
	if s := f.sessions[sid]; s != nil {
		s.working = false
	}
	if f.Mode == Working && !f.anyWorking() {
		f.striking = false
		f.setMode(Idle, now)
		f.Expanded = false
	}
}

// Tap dismisses a Done or Needs-you alert.
func (f *Forge) Tap(now time.Time) {
	if f.Mode != Done && f.Mode != NeedsInput {
		return
	}
	f.collapseAt = time.Time{}
	f.Expanded = false
	if f.anyWorking() {
		f.setMode(Working, now)
		f.startStrikes(now)
	} else {
		f.setMode(Idle, now)
	}
}

func (f *Forge) Reset(now time.Time) {
	f.collapseAt = time.Time{}
	f.sessions = map[string]*session{}
	f.queue = nil
	f.demo = nil
	f.striking = false
	f.setMode(Idle, now)
	f.Expanded = false
}

func (f *Forge) play(s string) {
	if !f.Muted && f.Play != nil {
		f.Play(s)
	}
}

func (f *Forge) push(s string) {
	c := oneLine(s)
	if c == "" {
		return
	}
	f.queue = append(f.queue, c)
	if len(f.queue) > 10 {
		f.queue = f.queue[len(f.queue)-10:]
	}
}

func (f *Forge) startStrikes(now time.Time) {
	if f.striking {
		return
	}
	f.striking = true
	f.strike(now)
}

func (f *Forge) strike(now time.Time) {
	f.LastHit = now
	f.nextStrike = now.Add(850 * time.Millisecond)
	if len(f.queue) > 0 {
		f.setLine(f.queue[0], now)
		f.queue = f.queue[1:]
	}
}

// Tick advances timers. Call it every frame.
func (f *Forge) Tick(now time.Time) {
	for len(f.demo) > 0 && !now.Before(f.demo[0].at) {
		e := f.demo[0]
		f.demo = f.demo[1:]
		f.Handle(e.ev, now)
	}
	if f.striking && !now.Before(f.nextStrike) {
		f.strike(now)
	}
	if !f.collapseAt.IsZero() && !now.Before(f.collapseAt) {
		f.collapseAt = time.Time{}
		if f.Mode == Done {
			f.Expanded = false
		}
	}
	if now.Sub(f.lastSweep) > 30*time.Second {
		f.lastSweep = now
		for k, s := range f.sessions {
			if s.working && now.Sub(s.last) > 20*time.Minute {
				f.quiet(k, now)
			}
		}
	}
}

func snippets(tool string, ti map[string]any) []string {
	path := str(ti, "file_path")
	if path == "" {
		path = str(ti, "path")
	}
	if path == "" {
		path = str(ti, "notebook_path")
	}
	file := "file"
	if path != "" {
		file = baseName(path)
	}
	switch tool {
	case "Bash", "PowerShell":
		return []string{"$ " + str(ti, "command")}
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		body := str(ti, "new_string")
		if body == "" {
			body = str(ti, "content")
		}
		if body == "" {
			body = str(ti, "new_source")
		}
		if body == "" {
			if edits, ok := ti["edits"].([]any); ok {
				var parts []string
				for _, e := range edits {
					if m, ok := e.(map[string]any); ok {
						parts = append(parts, str(m, "new_string"))
					}
				}
				body = strings.Join(parts, "\n")
			}
		}
		out := []string{"edit " + file}
		for _, l := range strings.Split(body, "\n") {
			l = strings.TrimSpace(l)
			if len([]rune(l)) > 3 {
				out = append(out, l)
				if len(out) == 4 {
					break
				}
			}
		}
		return out
	case "Read":
		return []string{"read " + file}
	case "Grep", "Glob":
		return []string{"find " + str(ti, "pattern")}
	case "WebSearch":
		return []string{"search " + str(ti, "query")}
	case "WebFetch":
		u := str(ti, "url")
		if p, err := url.Parse(u); err == nil && p.Host != "" {
			u = p.Host
		}
		return []string{"fetch " + u}
	case "Task", "Agent":
		d := str(ti, "description")
		if d == "" {
			d = "subtask"
		}
		return []string{"agent: " + d}
	case "TodoWrite":
		if todos, ok := ti["todos"].([]any); ok {
			for _, t := range todos {
				m, _ := t.(map[string]any)
				if m != nil && str(m, "status") == "in_progress" {
					if a := str(m, "activeForm"); a != "" {
						return []string{"todo: " + a}
					}
					return []string{"todo: " + str(m, "content")}
				}
			}
		}
		return []string{"updating todos"}
	}
	if strings.HasPrefix(tool, "mcp__") {
		return []string{strings.Join(strings.Split(tool[5:], "__"), " · ")}
	}
	return []string{tool}
}

// Demo plays a fake session.
func (f *Forge) Demo(now time.Time) {
	f.Reset(now)
	ev := func(m map[string]any) map[string]any {
		m["session_id"] = "demo"
		m["cwd"] = "C:/demo/claudes-forge"
		return m
	}
	at := func(s float64) time.Time { return now.Add(time.Duration(s * float64(time.Second))) }
	f.demo = []pending{
		{at(0), ev(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "add a dark mode toggle to settings"})},
		{at(1.2), ev(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Grep", "tool_input": map[string]any{"pattern": "ThemeProvider"}})},
		{at(2.4), ev(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Read", "tool_input": map[string]any{"file_path": "C:/x/Settings.tsx"}})},
		{at(3.6), ev(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": map[string]any{
			"file_path":  "C:/x/Settings.tsx",
			"new_string": "const [dark, setDark] = useState(false)\n<Toggle checked={dark} onChange={setDark} />\ndocument.body.classList.toggle('dark', dark)",
		}})},
		{at(7.5), ev(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "npm test -- settings"}})},
		{at(9.5), ev(map[string]any{"hook_event_name": "Stop"})},
	}
}
