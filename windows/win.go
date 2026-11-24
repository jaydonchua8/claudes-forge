//go:build windows

package main

// Windows shell: a click-through layered window pinned to the top center of the
// primary screen, a tail on the hook event file, sounds, and the installer.

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func init() { runtime.LockOSThread() }

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pSetTimer                      = user32.NewProc("SetTimer")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pUpdateLayeredWindow           = user32.NewProc("UpdateLayeredWindow")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pFindWindowW                   = user32.NewProc("FindWindowW")
	pDrawTextW                     = user32.NewProc("DrawTextW")
	pGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateFontW        = gdi32.NewProc("CreateFontW")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")
	pGdiFlush           = gdi32.NewProc("GdiFlush")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")

	pPlaySoundW = winmm.NewProc("PlaySoundW")

	pRegOpenKeyExW   = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey     = advapi32.NewProc("RegCloseKey")
)

const (
	wsPopup         = 0x80000000
	wsExLayered     = 0x00080000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmNull          = 0x0000
	wmTimer         = 0x0113
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmMouseActivate = 0x0021
	wmDisplayChange = 0x007E
	wmDpiChanged    = 0x02E0
	maNoActivate    = 3
	swShowNoActive  = 4
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoActivate   = 0x0010
	ulwAlpha        = 0x2
	mfString        = 0x0
	mfChecked       = 0x8
	mfSeparator     = 0x800
	tpmReturnCmd    = 0x100
	tpmRightButton  = 0x2
	dtSingleLine    = 0x20
	dtNoPrefix      = 0x800
	dtCalcRect      = 0x400
	dtEndEllipsis   = 0x8000
	mbIconInfo      = 0x40
	mbIconError     = 0x10
	sndAsync        = 0x1
	sndNoDefault    = 0x2
	sndFilename     = 0x20000
	hkcu            = 0x80000001
	keySetValue     = 0x0002
	regSZ           = 1

	className = "ClaudesForgeIsland"
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName   = "ClaudesForge"
)

var hwndTopmost = ^uintptr(0) // (HWND)-1

type point struct{ X, Y int32 }
type size struct{ CX, CY int32 }
type rect struct{ L, T, R, B int32 }

type wndClassEx struct {
	cbSize, style       uint32
	wndProc             uintptr
	clsExtra, wndExtra  int32
	instance, icon      uintptr
	cursor, background  uintptr
	menuName, className *uint16
	iconSm              uintptr
}

type msgT struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	h      bitmapInfoHeader
	colors [1]uint32
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func ptr(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

func msgBox(text string, icon uintptr) {
	pMessageBoxW.Call(0, ptr(u16(text)), ptr(u16("Claude's Forge")), icon)
}

// ---- paths ----

func installDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "ClaudesForge")
}
func dataDir() string    { return filepath.Join(homeDir(), ".claudes-forge") }
func eventsPath() string { return filepath.Join(dataDir(), "events.jsonl") }
func mutedPath() string  { return filepath.Join(dataDir(), "muted") }

// ---- 32-bit DIB ----

type dib struct {
	dc, bmp uintptr
	w, h    int
	bits    []byte
}

func newDIB(w, h int) *dib {
	screen, _, _ := pGetDC.Call(0)
	dc, _, _ := pCreateCompatibleDC.Call(screen)
	pReleaseDC.Call(0, screen)
	var bi bitmapInfo
	bi.h.Size = uint32(unsafe.Sizeof(bi.h))
	bi.h.Width = int32(w)
	bi.h.Height = -int32(h)
	bi.h.Planes = 1
	bi.h.BitCount = 32
	var p unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&p)), 0, 0)
	pSelectObject.Call(dc, bmp)
	return &dib{dc: dc, bmp: bmp, w: w, h: h, bits: unsafe.Slice((*byte)(p), w*h*4)}
}

func (d *dib) free() {
	pDeleteDC.Call(d.dc)
	pDeleteObject.Call(d.bmp)
}

// ---- GDI text into a coverage mask ----

type gdiText struct {
	d     *dib
	fonts map[int]uintptr
}

func newGDIText(w, h int) *gdiText { return &gdiText{d: newDIB(w, h), fonts: map[int]uintptr{}} }

func (t *gdiText) free() {
	for _, f := range t.fonts {
		pDeleteObject.Call(f)
	}
	t.d.free()
}

func (t *gdiText) font(px int) uintptr {
	if f, ok := t.fonts[px]; ok {
		return f
	}
	h := int32(-px)
	f, _, _ := pCreateFontW.Call(uintptr(h), 0, 0, 0, 500, 0, 0, 0, 1, 0, 0, 4, 0x31, ptr(u16("Consolas")))
	t.fonts[px] = f
	return f
}

func (t *gdiText) Render(s string, px, maxW int) *TextMask {
	d := t.d
	maxW = min(maxW, d.w)
	pSelectObject.Call(d.dc, t.font(px))
	pSetBkMode.Call(d.dc, 1)
	pSetTextColor.Call(d.dc, 0x00FFFFFF)
	for i := range d.bits {
		d.bits[i] = 0
	}
	text := u16(s)
	r := rect{0, 0, int32(maxW), int32(d.h)}
	pDrawTextW.Call(d.dc, ptr(text), ^uintptr(0), uintptr(unsafe.Pointer(&r)), dtCalcRect|dtSingleLine|dtNoPrefix)
	w := min(int(r.R), maxW)
	h := min(int(r.B), d.h)
	if w <= 0 || h <= 0 {
		return nil
	}
	r2 := rect{0, 0, int32(w), int32(h)}
	pDrawTextW.Call(d.dc, ptr(text), ^uintptr(0), uintptr(unsafe.Pointer(&r2)), dtSingleLine|dtNoPrefix|dtEndEllipsis)
	pGdiFlush.Call()
	m := &TextMask{W: w, H: h, Pix: make([]uint8, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Pix[y*w+x] = d.bits[(y*d.w+x)*4+2]
		}
	}
	return m
}

// ---- event file tail ----

type tail struct {
	off     int64
	started bool
	buf     []byte
	last    time.Time
}

func (t *tail) poll(now time.Time, handle func(map[string]any)) {
	if now.Sub(t.last) < 150*time.Millisecond {
		return
	}
	t.last = now
	fi, err := os.Stat(eventsPath())
	if err != nil {
		t.started = true
		t.off = 0
		return
	}
	if !t.started {
		t.started = true
		t.off = fi.Size()
		return
	}
	if fi.Size() < t.off {
		t.off = 0
		t.buf = nil
	}
	if fi.Size() == t.off {
		return
	}
	f, err := os.Open(eventsPath())
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return
	}
	data, _ := io.ReadAll(io.LimitReader(f, 4<<20))
	t.off += int64(len(data))
	t.buf = append(t.buf, data...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		line := t.buf[:i]
		t.buf = t.buf[i+1:]
		var o map[string]any
		if json.Unmarshal(line, &o) == nil {
			handle(o)
		}
	}
}

// ---- the app ----

type App struct {
	hwnd      uintptr
	forge     *Forge
	rend      *Renderer
	canvas    *Canvas
	win       *dib
	tx        *gdiText
	tl        tail
	x         int32
	cs        int
	frame     int
	animating bool
}

var app *App

func dpi() int {
	if pGetDpiForSystem.Find() == nil {
		if d, _, _ := pGetDpiForSystem.Call(); d > 0 {
			return int(d)
		}
	}
	screen, _, _ := pGetDC.Call(0)
	d, _, _ := pGetDeviceCaps.Call(screen, 88)
	pReleaseDC.Call(0, screen)
	if d == 0 {
		return 96
	}
	return int(d)
}

func (a *App) layout() {
	cs := max(2, int(math.Round(2*float64(dpi())/96)))
	if cs != a.cs || a.rend == nil {
		a.cs = cs
		if a.win != nil {
			a.win.free()
		}
		if a.tx != nil {
			a.tx.free()
		}
		a.tx = newGDIText(cellsWe*cs, 10*cs)
		a.rend = NewRenderer(cs, a.tx)
		w, h := a.rend.WindowSize()
		a.canvas = NewCanvas(w, h)
		a.win = newDIB(w, h)
	}
	sw, _, _ := pGetSystemMetrics.Call(0)
	a.x = int32((int(sw) - a.canvas.W) / 2)
	if a.hwnd != 0 {
		pSetWindowPos.Call(a.hwnd, hwndTopmost, uintptr(a.x), 0, uintptr(a.canvas.W), uintptr(a.canvas.H), swpNoActivate)
		a.render(time.Now())
	}
}

func (a *App) render(now time.Time) {
	a.animating = a.rend.Draw(a.canvas, a.forge, now)
	src, dst := a.canvas.Pix, a.win.bits
	for i := 0; i+3 < len(src); i += 4 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], src[i+3]
	}
	screen, _, _ := pGetDC.Call(0)
	pt := point{a.x, 0}
	sz := size{int32(a.canvas.W), int32(a.canvas.H)}
	var zero point
	blend := uint32(0x01FF0000) // AC_SRC_OVER, 0, 255, AC_SRC_ALPHA
	pUpdateLayeredWindow.Call(a.hwnd, screen, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)),
		a.win.dc, uintptr(unsafe.Pointer(&zero)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	pReleaseDC.Call(0, screen)
}

func (a *App) onFrame() {
	now := time.Now()
	a.forge.Tick(now)
	a.tl.poll(now, func(o map[string]any) { a.forge.Handle(o, now) })
	a.frame++
	fast := a.forge.Mode == Working || a.forge.Walking(now) || a.animating ||
		now.Sub(a.forge.LineAt) < 700*time.Millisecond
	if fast || a.frame%4 == 0 {
		a.render(now)
	}
	if a.frame%300 == 0 {
		pSetWindowPos.Call(a.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	}
}

func (a *App) menu() {
	m, _, _ := pCreatePopupMenu.Call()
	add := func(id uintptr, flags uintptr, label string) {
		pAppendMenuW.Call(m, flags, id, ptr(u16(label)))
	}
	muted := uintptr(mfString)
	if a.forge.Muted {
		muted |= mfChecked
	}
	add(1, mfString, "Run demo")
	add(2, muted, "Mute sounds")
	add(3, mfString, "Reset")
	pAppendMenuW.Call(m, mfSeparator, 0, 0)
	add(4, mfString, "Quit Claude's Forge")
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(a.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, a.hwnd, 0)
	pPostMessageW.Call(a.hwnd, wmNull, 0, 0)
	pDestroyMenu.Call(m)
	now := time.Now()
	switch cmd {
	case 1:
		a.forge.Demo(now)
	case 2:
		a.forge.Muted = !a.forge.Muted
		if a.forge.Muted {
			_ = os.WriteFile(mutedPath(), []byte("1"), 0o644)
		} else {
			_ = os.Remove(mutedPath())
		}
	case 3:
		a.forge.Reset(now)
	case 4:
		pDestroyWindow.Call(a.hwnd)
	}
}

func playSound(name string) {
	media := filepath.Join(os.Getenv("WINDIR"), "Media")
	files := []string{"Windows Notify System Generic.wav", "chimes.wav"}
	if name == "ping" {
		files = []string{"Windows Ding.wav", "ding.wav"}
	}
	for _, f := range files {
		p := filepath.Join(media, f)
		if _, err := os.Stat(p); err == nil {
			pPlaySoundW.Call(ptr(u16(p)), 0, sndFilename|sndAsync|sndNoDefault)
			return
		}
	}
}

func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case wmTimer:
		app.onFrame()
		return 0
	case wmLButtonUp:
		app.forge.Tap(time.Now())
		return 0
	case wmRButtonUp:
		app.menu()
		return 0
	case wmMouseActivate:
		return maNoActivate
	case wmDisplayChange, wmDpiChanged:
		app.layout()
		return 0
	case wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func runIsland(demo bool) {
	if _, _, err := pCreateMutexW.Call(0, 0, ptr(u16(`Local\ClaudesForgeIsland`))); err == syscall.Errno(183) {
		return // already running
	}
	if pSetProcessDpiAwarenessContext.Find() == nil {
		pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	} else {
		pSetProcessDPIAware.Call()
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	if fi, err := os.Stat(eventsPath()); err == nil && fi.Size() > 2<<20 {
		_ = os.Truncate(eventsPath(), 0)
	}

	f := NewForge()
	_, err := os.Stat(mutedPath())
	f.Muted = err == nil
	f.Play = playSound
	app = &App{forge: f}
	app.layout()

	inst, _, _ := pGetModuleHandleW.Call(0)
	cursor, _, _ := pLoadCursorW.Call(0, 32512)
	wc := wndClassEx{
		wndProc:   syscall.NewCallback(wndProc),
		instance:  inst,
		cursor:    cursor,
		className: u16(className),
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowExW.Call(wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		ptr(u16(className)), ptr(u16("Claude's Forge")), wsPopup,
		uintptr(app.x), 0, uintptr(app.canvas.W), uintptr(app.canvas.H), 0, 0, inst, 0)
	if hwnd == 0 {
		msgBox("Could not create the window.", mbIconError)
		return
	}
	app.hwnd = hwnd
	now := time.Now()
	f.Tick(now)
	if demo {
		f.Demo(now)
	}
	app.render(now)
	pShowWindow.Call(hwnd, swShowNoActive)
	pSetTimer.Call(hwnd, 1, 33, 0)

	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// ---- install / uninstall ----

func setRunKey(value string, remove bool) {
	var hk uintptr
	if r, _, _ := pRegOpenKeyExW.Call(hkcu, ptr(u16(runKey)), 0, keySetValue, uintptr(unsafe.Pointer(&hk))); r != 0 {
		return
	}
	defer pRegCloseKey.Call(hk)
	if remove {
		pRegDeleteValueW.Call(hk, ptr(u16(runName)))
		return
	}
	v, _ := syscall.UTF16FromString(value)
	pRegSetValueExW.Call(hk, ptr(u16(runName)), 0, regSZ, uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2))
}

func closeRunning() {
	for i := 0; i < 40; i++ {
		h, _, _ := pFindWindowW.Call(ptr(u16(className)), 0)
		if h == 0 {
			return
		}
		if i == 0 {
			pPostMessageW.Call(h, wmClose, 0, 0)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

func install(exe string) {
	src := filepath.Dir(exe)
	hookSrc := filepath.Join(src, "forge-hook.exe")
	if _, err := os.Stat(hookSrc); err != nil {
		msgBox("forge-hook.exe needs to be in the same folder as claudes-forge.exe. Unzip both files and run claudes-forge.exe again.", mbIconError)
		return
	}
	closeRunning()
	dir := installDir()
	appExe := filepath.Join(dir, "claudes-forge.exe")
	hookExe := filepath.Join(dir, "forge-hook.exe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		msgBox("Could not create "+dir+": "+err.Error(), mbIconError)
		return
	}
	if err := copyFile(exe, appExe); err != nil {
		msgBox("Could not copy the app: "+err.Error(), mbIconError)
		return
	}
	if err := copyFile(hookSrc, hookExe); err != nil {
		msgBox("Could not copy forge-hook.exe: "+err.Error(), mbIconError)
		return
	}
	cmd := filepath.ToSlash(hookExe)
	if strings.Contains(cmd, " ") {
		cmd = `"` + cmd + `"`
	}
	if err := addHooks(cmd); err != nil {
		msgBox("Could not update "+settingsPath()+": "+err.Error(), mbIconError)
		return
	}
	setRunKey(`"`+appExe+`"`, false)
	_ = exec.Command(appExe).Start()
	msgBox("Claude's Forge is installed and running at the top of your screen.\n\n"+
		"It reacts to Claude Code sessions on this PC and starts when you sign in. "+
		"Right-click it for the demo, mute and quit.\n\n"+
		"To remove it, run claudes-forge.exe --uninstall.", mbIconInfo)
}

func uninstall() {
	closeRunning()
	err := removeHooks()
	setRunKey("", true)
	_ = os.RemoveAll(installDir())
	_ = os.RemoveAll(dataDir())
	if err != nil {
		msgBox("Removed the app, but could not edit "+settingsPath()+": "+err.Error(), mbIconError)
		return
	}
	msgBox("Claude's Forge was removed.", mbIconInfo)
}

func main() {
	args := strings.Join(os.Args[1:], " ")
	if strings.Contains(args, "--uninstall") {
		uninstall()
		return
	}
	exe, _ := os.Executable()
	if !strings.EqualFold(filepath.Clean(filepath.Dir(exe)), filepath.Clean(installDir())) {
		install(exe)
		return
	}
	runIsland(strings.Contains(args, "--demo"))
}
