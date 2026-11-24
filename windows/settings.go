package main

// Adds and removes the Claude Code hooks in ~/.claude/settings.json.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func homeDir() string { h, _ := os.UserHomeDir(); return h }

var hookEvents = []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "Notification", "SessionEnd"}

func settingsPath() string { return filepath.Join(homeDir(), ".claude", "settings.json") }

func isOurs(entry any) bool {
	m, _ := entry.(map[string]any)
	hs, _ := m["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); strings.Contains(c, "forge-hook") {
			return true
		}
	}
	return false
}

func editSettings(edit func(root map[string]any)) error {
	p := settingsPath()
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		data = []byte("{}")
	} else if err != nil {
		return err
	} else if _, err := os.Stat(p + ".claudes-forge-backup"); os.IsNotExist(err) {
		_ = os.WriteFile(p+".claudes-forge-backup", data, 0o644)
	}
	root := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if len(bytes.TrimSpace(data)) > 0 {
		if err := dec.Decode(&root); err != nil {
			return err
		}
	}
	edit(root)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func addHooks(cmd string) error {
	return editSettings(func(root map[string]any) {
		hooks, _ := root["hooks"].(map[string]any)
		if hooks == nil {
			hooks = map[string]any{}
		}
		for _, ev := range hookEvents {
			list, _ := hooks[ev].([]any)
			found := false
			for _, e := range list {
				if isOurs(e) {
					found = true
				}
			}
			if found {
				continue
			}
			entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
			if ev == "PreToolUse" || ev == "PostToolUse" {
				entry["matcher"] = "*"
			}
			hooks[ev] = append(list, entry)
		}
		root["hooks"] = hooks
	})
}

func removeHooks() error {
	if _, err := os.Stat(settingsPath()); err != nil {
		return nil
	}
	return editSettings(func(root map[string]any) {
		hooks, _ := root["hooks"].(map[string]any)
		if hooks == nil {
			return
		}
		for ev, v := range hooks {
			list, _ := v.([]any)
			var keep []any
			for _, e := range list {
				if !isOurs(e) {
					keep = append(keep, e)
				}
			}
			if len(keep) == 0 {
				delete(hooks, ev)
			} else {
				hooks[ev] = keep
			}
		}
		if len(hooks) == 0 {
			delete(root, "hooks")
		}
	})
}
