// forge-hook.exe: Claude Code runs this for each hook event. It appends one
// trimmed JSON line to %USERPROFILE%\.claudes-forge\events.jsonl and exits.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

func trim(v any) any {
	switch x := v.(type) {
	case string:
		if r := []rune(x); len(r) > 400 {
			return string(r[:400])
		}
	case map[string]any:
		for k, e := range x {
			x[k] = trim(e)
		}
	case []any:
		for i, e := range x {
			x[i] = trim(e)
		}
	}
	return v
}

func main() {
	defer func() { _ = recover() }()
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
	if err != nil {
		return
	}
	var o map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&o) != nil {
		return
	}
	delete(o, "tool_response")
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(trim(o)) != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".claudes-forge")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(out.Bytes())
	_ = f.Close()
}
