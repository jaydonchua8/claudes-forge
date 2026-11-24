package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestSettingsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	orig := `{"theme":"dark","n":12345678901234567890,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say hi"}]}]}}`
	_ = os.WriteFile(settingsPath(), []byte(orig), 0o644)
	if err := addHooks("C:/Users/me/AppData/Local/ClaudesForge/forge-hook.exe"); err != nil {
		t.Fatal(err)
	}
	if err := addHooks("C:/Users/me/AppData/Local/ClaudesForge/forge-hook.exe"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settingsPath())
	s := string(b)
	if strings.Count(s, "forge-hook.exe") != 6 || !strings.Contains(s, "say hi") || !strings.Contains(s, "12345678901234567890") {
		t.Fatalf("bad add:\n%s", s)
	}
	if err := removeHooks(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(settingsPath())
	s = string(b)
	if strings.Contains(s, "forge-hook") || !strings.Contains(s, "say hi") || !strings.Contains(s, `"theme": "dark"`) {
		t.Fatalf("bad remove:\n%s", s)
	}
	if bk, _ := os.ReadFile(settingsPath() + ".claudes-forge-backup"); string(bk) != orig {
		t.Fatal("backup should hold the original")
	}
	t.Log(s)
}

// Same field types as win.go's Win32 structs; layouts must match the C ABI on amd64.
type tWnd struct {
	cbSize, style       uint32
	wndProc             uintptr
	clsExtra, wndExtra  int32
	instance, icon      uintptr
	cursor, background  uintptr
	menuName, className *uint16
	iconSm              uintptr
}
type tPoint struct{ X, Y int32 }
type tMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      tPoint
	private uint32
}

func TestStructSizes(t *testing.T) {
	if unsafe.Sizeof(tWnd{}) != 80 || unsafe.Sizeof(tMsg{}) != 48 {
		t.Fatal(unsafe.Sizeof(tWnd{}), unsafe.Sizeof(tMsg{}))
	}
}

func TestForgeFlow(t *testing.T) {
	f := NewForge()
	now := f.LastHit
	f.Handle(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "a", "cwd": `C:\code\leetstrat`, "prompt": "hi"}, now)
	f.Tick(now.Add(900 * time.Millisecond))
	if f.Mode != Working || f.Line != "› hi" {
		t.Fatal("not working", f.Mode, f.Line)
	}
	f.Handle(map[string]any{"hook_event_name": "Stop", "session_id": "a", "cwd": `C:\code\leetstrat`}, now)
	if f.Mode != Done || !strings.HasPrefix(f.Line, "Done · leetstrat") {
		t.Fatal("not done", f.Line)
	}
}
