//go:build !windows

package main

// Preview tool for non-Windows machines: plays the demo and writes PNG frames,
// so the art can be checked without Windows. Text is drawn as placeholder blocks.
//   go run . <outdir>

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"time"
)

type blockText struct{}

func (blockText) Render(s string, px, maxW int) *TextMask {
	cw := px * 6 / 10
	n := len([]rune(s))
	w := min(n*cw, maxW)
	m := &TextMask{W: w, H: px, Pix: make([]uint8, w*px)}
	for i, r := range []rune(s) {
		if r == ' ' {
			continue
		}
		for y := px / 4; y < px*9/10; y++ {
			for x := i*cw + 1; x < i*cw+cw-1 && x < w; x++ {
				m.Pix[y*w+x] = 220
			}
		}
	}
	return m
}

func save(c *Canvas, path string) {
	img := image.NewRGBA(image.Rect(0, 0, c.W, c.H))
	for i := 0; i < c.W*c.H; i++ {
		a := float64(c.Pix[i*4+3]) / 255
		bg := 236.0 * (1 - a) // light menu bar behind
		img.Pix[i*4+0] = uint8(float64(c.Pix[i*4+0]) + bg)
		img.Pix[i*4+1] = uint8(float64(c.Pix[i*4+1]) + bg)
		img.Pix[i*4+2] = uint8(float64(c.Pix[i*4+2]) + bg)
		img.Pix[i*4+3] = 255
	}
	fh, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer fh.Close()
	_ = png.Encode(fh, img)
	_ = color.RGBA{}
}

func main() {
	out := "preview"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	_ = os.MkdirAll(out, 0o755)
	cs := 3
	r := NewRenderer(cs, blockText{})
	w, h := r.WindowSize()
	c := NewCanvas(w, h)
	f := NewForge()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.Tick(t0)
	f.Demo(t0)
	shots := map[int]string{0: "idle", 50: "walk-to-anvil", 280: "working-hit", 300: "working", 480: "edit", 600: "walk-to-bed", 700: "done-asleep", 1300: "collapsed-asleep"}
	for i := 0; i <= 1300; i++ {
		now := t0.Add(time.Duration(i) * time.Second / 60)
		if i == 0 {
			// show the idle state before the demo starts
			g := NewForge()
			g.Tick(now)
			r.Draw(c, g, now)
			save(c, filepath.Join(out, "00-idle.png"))
			continue
		}
		f.Tick(now)
		r.Draw(c, f, now)
		if name, ok := shots[i]; ok {
			save(c, filepath.Join(out, fmt.Sprintf("%04d-%s.png", i, name)))
		}
	}
	fmt.Println("wrote", out)
}
