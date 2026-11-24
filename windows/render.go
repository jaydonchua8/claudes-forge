package main

// Software renderer: draws the island (cave, bed, anvil, bot, text) into a
// premultiplied RGBA buffer. Platform independent; the Windows shell hands the
// buffer to UpdateLayeredWindow, and the preview tool writes it to PNG.

import (
	"fmt"
	"math"
	"time"
)

type RGB struct{ R, G, B float64 }

var (
	clay      = RGB{0.85, 0.47, 0.34}
	clayDark  = RGB{0.62, 0.32, 0.22}
	steel     = RGB{0.66, 0.69, 0.74}
	steelDark = RGB{0.40, 0.42, 0.48}
	hot       = RGB{1.0, 0.72, 0.22}
	hotPale   = RGB{1.0, 0.93, 0.6}
	sparkPale = RGB{1.0, 0.9, 0.55}
	wood      = RGB{0.60, 0.44, 0.28}
	okGreen   = RGB{0.30, 0.85, 0.48}
	amber     = RGB{1.0, 0.70, 0.20}
	cream     = RGB{1.0, 0.95, 0.82}
	white     = RGB{1, 1, 1}
	black     = RGB{0, 0, 0}

	caveBack   = RGB{0.10, 0.08, 0.14}
	caveDeep   = RGB{0.065, 0.05, 0.095}
	rockLight  = RGB{0.15, 0.12, 0.20}
	stalCol    = RGB{0.20, 0.16, 0.26}
	floorTop   = RGB{0.25, 0.20, 0.31}
	floorDark  = RGB{0.15, 0.12, 0.19}
	pebble     = RGB{0.30, 0.24, 0.36}
	crystalA   = RGB{0.45, 0.85, 1.0}
	crystalB   = RGB{0.78, 0.56, 1.0}
	moonCol    = RGB{1.0, 0.88, 0.52}
	bedFrame   = RGB{0.22, 0.27, 0.58}
	bedFrameHi = RGB{0.33, 0.39, 0.74}
	sheetCol   = RGB{0.88, 0.87, 0.95}
	pillowCol  = RGB{0.98, 0.97, 1.0}
	blanket    = RGB{0.25, 0.33, 0.80}
	blanketHi  = RGB{0.36, 0.46, 0.92}
	starCol    = RGB{1.0, 0.90, 0.55}
	visor      = RGB{0.13, 0.10, 0.10}
	zCol       = RGB{0.78, 0.84, 1.0}
)

func lerp(a, b RGB, t float64) RGB {
	return RGB{a.R + (b.R-a.R)*t, a.G + (b.G-a.G)*t, a.B + (b.B-a.B)*t}
}

func easeOut(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return 1 - (1-t)*(1-t)*(1-t)
}

// ---- canvas ----

type Canvas struct {
	W, H               int
	Pix                []uint8 // premultiplied RGBA
	cx0, cy0, cx1, cy1 int
}

func NewCanvas(w, h int) *Canvas {
	return &Canvas{W: w, H: h, Pix: make([]uint8, w*h*4), cx1: w, cy1: h}
}

func (c *Canvas) Clear() {
	for i := range c.Pix {
		c.Pix[i] = 0
	}
}

func (c *Canvas) SetClip(x0, y0, x1, y1 int) {
	c.cx0, c.cy0 = max(0, x0), max(0, y0)
	c.cx1, c.cy1 = min(c.W, x1), min(c.H, y1)
}

func (c *Canvas) blend(i int, col RGB, a float64) {
	if a <= 0 {
		return
	}
	if a > 1 {
		a = 1
	}
	p := c.Pix[i : i+4 : i+4]
	ia := 1 - a
	p[0] = uint8(col.R*255*a + float64(p[0])*ia + 0.5)
	p[1] = uint8(col.G*255*a + float64(p[1])*ia + 0.5)
	p[2] = uint8(col.B*255*a + float64(p[2])*ia + 0.5)
	p[3] = uint8(255*a + float64(p[3])*ia + 0.5)
}

func (c *Canvas) Rect(x, y, w, h int, col RGB, a float64) {
	x0, y0 := max(x, c.cx0), max(y, c.cy0)
	x1, y1 := min(x+w, c.cx1), min(y+h, c.cy1)
	for yy := y0; yy < y1; yy++ {
		i := (yy*c.W + x0) * 4
		for xx := x0; xx < x1; xx++ {
			c.blend(i, col, a)
			i += 4
		}
	}
}

func (c *Canvas) Glow(cx, cy, rad float64, col RGB, a float64) {
	if a <= 0 || rad <= 0 {
		return
	}
	x0, y0 := max(int(cx-rad), c.cx0), max(int(cy-rad), c.cy0)
	x1, y1 := min(int(cx+rad)+1, c.cx1), min(int(cy+rad)+1, c.cy1)
	for yy := y0; yy < y1; yy++ {
		for xx := x0; xx < x1; xx++ {
			d := math.Hypot(float64(xx)+0.5-cx, float64(yy)+0.5-cy)
			if d < rad {
				c.blend((yy*c.W+xx)*4, col, a*(1-d/rad))
			}
		}
	}
}

func (c *Canvas) VGradient(x, y, w, h int, top, bottom RGB, span int) {
	for yy := y; yy < y+h; yy++ {
		t := 1.0
		if span > 0 {
			t = math.Min(1, float64(yy-y)/float64(span))
		}
		c.Rect(x, yy, w, 1, lerp(top, bottom, t), 1)
	}
}

type TextMask struct {
	Pix  []uint8 // coverage 0..255
	W, H int
}

func (c *Canvas) Mask(m *TextMask, ox, oy int, col RGB, a float64) {
	if m == nil || a <= 0 {
		return
	}
	for yy := 0; yy < m.H; yy++ {
		py := oy + yy
		if py < c.cy0 || py >= c.cy1 {
			continue
		}
		for xx := 0; xx < m.W; xx++ {
			px := ox + xx
			if px < c.cx0 || px >= c.cx1 {
				continue
			}
			if v := m.Pix[yy*m.W+xx]; v > 0 {
				c.blend((py*c.W+px)*4, col, a*float64(v)/255)
			}
		}
	}
}

// RoundBottom clears everything outside the island and anti-aliases its bottom corners.
func (c *Canvas) RoundBottom(x0, y0, w, h int, r float64) {
	for yy := 0; yy < c.H; yy++ {
		for xx := 0; xx < c.W; xx++ {
			i := (yy*c.W + xx) * 4
			if xx < x0 || xx >= x0+w || yy < y0 || yy >= y0+h {
				c.Pix[i], c.Pix[i+1], c.Pix[i+2], c.Pix[i+3] = 0, 0, 0, 0
				continue
			}
			fy := float64(yy - y0)
			if fy < float64(h)-r {
				continue
			}
			var ccx float64
			fx := float64(xx - x0)
			switch {
			case fx < r:
				ccx = r
			case fx >= float64(w)-r:
				ccx = float64(w) - r
			default:
				continue
			}
			ccy := float64(h) - r
			in := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					dx := fx + (float64(sx)+0.5)/4 - ccx
					dy := fy + (float64(sy)+0.5)/4 - ccy
					if dx*dx+dy*dy <= r*r {
						in++
					}
				}
			}
			k := float64(in) / 16
			for j := 0; j < 4; j++ {
				c.Pix[i+j] = uint8(float64(c.Pix[i+j])*k + 0.5)
			}
		}
	}
}

// ---- renderer ----

type Texter interface {
	// Render returns a coverage mask for s at the given pixel height, cut to maxW with an ellipsis.
	Render(s string, px, maxW int) *TextMask
}

const (
	cellsWc = 66 // collapsed island, in cells
	cellsHc = 20
	cellsWe = 150 // expanded
	cellsHe = 33
)

type Renderer struct {
	CS         int // pixels per cell
	tx         Texter
	cache      map[string]*TextMask
	curW, curH float64
	last       time.Time
}

func NewRenderer(cs int, tx Texter) *Renderer {
	return &Renderer{CS: cs, tx: tx, cache: map[string]*TextMask{}}
}

func (r *Renderer) WindowSize() (int, int) { return cellsWe * r.CS, cellsHe * r.CS }

func (r *Renderer) text(s string, px, maxW int) *TextMask {
	if r.tx == nil || s == "" {
		return nil
	}
	k := fmt.Sprintf("%d|%d|%s", px, maxW, s)
	if m, ok := r.cache[k]; ok {
		return m
	}
	if len(r.cache) > 80 {
		r.cache = map[string]*TextMask{}
	}
	m := r.tx.Render(s, px, maxW)
	r.cache[k] = m
	return m
}

func hsh(a, b int) int {
	h := uint64(int64(a)*73856093 ^ int64(b)*19349663)
	h ^= h >> 13
	h *= 0x5bd1e995
	h ^= h >> 15
	return int(h % 1000003)
}

// Draw renders one frame. It returns true while the island is still resizing.
func (r *Renderer) Draw(c *Canvas, f *Forge, now time.Time) bool {
	cs := r.CS
	big := f.Mode == Working || f.Expanded
	tw, th := float64(cellsWc*cs), float64(cellsHc*cs)
	if big {
		tw, th = float64(cellsWe*cs), float64(cellsHe*cs)
	}
	dt := now.Sub(r.last).Seconds()
	r.last = now
	if r.curW == 0 || dt > 0.5 {
		r.curW, r.curH = tw, th
	}
	k := 1 - math.Exp(-math.Max(0, dt)*14)
	r.curW += (tw - r.curW) * k
	r.curH += (th - r.curH) * k
	if math.Abs(tw-r.curW) < 0.5 {
		r.curW = tw
	}
	if math.Abs(th-r.curH) < 0.5 {
		r.curH = th
	}
	animating := r.curW != tw || r.curH != th

	W, H := int(math.Round(r.curW)), int(math.Round(r.curH))
	x0, y0 := (c.W-W)/2, 0
	sx, sy := x0+3*cs, y0+cs

	c.SetClip(0, 0, c.W, c.H)
	c.Clear()
	c.SetClip(x0, y0, x0+W, y0+H)
	r.cave(c, now, sx, sy, x0, W, H)
	r.scene(c, f, now, sx, sy)
	r.status(c, f, now, x0+W-10*cs, y0+10*cs)

	var accent *RGB
	if f.Mode == Done {
		accent = &okGreen
	} else if f.Mode == NeedsInput {
		accent = &amber
	}
	if H > 21*cs {
		r.strip(c, f, now, accent, x0, y0, W, H)
	}
	if accent != nil && f.Expanded && H > 21*cs {
		n := 3 * cs
		for i := 0; i < n; i++ {
			a := 0.55 * (1 - float64(i)/float64(n))
			c.Rect(x0+i, y0+12*cs, 1, H-12*cs, *accent, a)
			c.Rect(x0+W-1-i, y0+12*cs, 1, H-12*cs, *accent, a)
			c.Rect(x0, y0+H-1-i, W, 1, *accent, a)
		}
	}
	c.SetClip(0, 0, c.W, c.H)
	rad := 5.0
	if big {
		rad = 7
	}
	c.RoundBottom(x0, y0, W, H, rad*float64(cs))
	return animating
}

func (r *Renderer) cave(c *Canvas, now time.Time, sx, sy, x0, W, H int) {
	cs := r.CS
	R := func(x, y, w, h int, col RGB, a float64) { c.Rect(sx+x*cs, sy+y*cs, w*cs, h*cs, col, a) }
	glow := func(cx, cy, rad float64, col RGB, a float64) {
		c.Glow(float64(sx)+cx*float64(cs), float64(sy)+cy*float64(cs), rad*float64(cs), col, a)
	}
	secs := float64(now.UnixNano()%1e12) / 1e9
	colMin := (x0-sx)/cs - 2
	colMax := (x0+W-sx)/cs + 2
	span := colMax - colMin
	bottomRow := (H - (sy - 0)) / cs

	c.Rect(x0, 0, W, H, caveBack, 1)
	for i := colMin; i < colMax; i++ {
		if hsh(i, 1)%6 == 0 {
			R(i, 2+hsh(i, 2)%10, 2+hsh(i, 3)%3, 1+hsh(i, 4)%2, rockLight, 1)
		}
	}
	last := -1000
	for i := colMin; i < colMax; i++ {
		if hsh(i, 5)%5 == 0 && i-last > 4 {
			last = i
			ln := 2 + hsh(i, 6)%3
			R(i, -1, 3, 2, stalCol, 1)
			R(i+1, 1, 1, ln-1, stalCol, 1)
		}
	}
	for i := colMin; i < colMax; i++ {
		if hsh(i, 7)%13 == 0 {
			y := 4 + hsh(i, 8)%7
			col := crystalA
			if hsh(i, 9)%2 == 1 {
				col = crystalB
			}
			tw := 0.55 + 0.45*math.Sin(secs*1.3+float64(hsh(i, 10)%60)/10)
			glow(float64(i)+0.5, float64(y)+1, 3.3, col, 0.3*tw)
			R(i, y, 1, 2, col, 0.55+0.45*tw)
			R(i+1, y+1, 1, 1, col, 0.4+0.4*tw)
		}
	}
	R(colMin, 15, span, 1, floorTop, 1)
	R(colMin, 16, span, 3, floorDark, 1)
	for i := colMin; i < colMax; i++ {
		if hsh(i, 11)%5 == 0 {
			R(i, 16+hsh(i, 12)%2, 1+hsh(i, 13)%2, 1, pebble, 1)
		}
	}
	if bottomRow > 19 {
		top := sy + 19*cs
		c.VGradient(x0, top, W, H-top, floorDark, caveDeep, 3*cs)
		for i := colMin; i < colMax; i++ {
			if hsh(i, 14)%4 == 0 {
				R(i, 19, 1+hsh(i, 15)%3, 1, floorDark, 1)
			}
			if hsh(i, 16)%19 == 0 {
				y := 21 + hsh(i, 17)%max(1, bottomRow-23)
				tw := 0.5 + 0.5*math.Sin(secs*1.7+float64(i))
				col := crystalA
				if hsh(i, 18)%2 == 1 {
					col = crystalB
				}
				R(i, y, 1, 1, col, 0.25+0.35*tw)
			}
		}
	}
}

func (r *Renderer) scene(c *Canvas, f *Forge, now time.Time, sx, sy int) {
	cs := r.CS
	fcs := float64(cs)
	R := func(x, y, w, h int, col RGB, a float64) { c.Rect(sx+x*cs, sy+y*cs, w*cs, h*cs, col, a) }
	glow := func(cx, cy, rad float64, col RGB, a float64) {
		c.Glow(float64(sx)+cx*fcs, float64(sy)+cy*fcs, rad*fcs, col, a)
	}
	mode := f.Mode
	t := now.Sub(f.LastHit).Seconds()
	secs := float64(now.UnixNano()%1e12) / 1e9
	p := f.Pos(now)
	moving := f.Walking(now)
	ox := int(math.Round(p))
	asleep := !moving && (mode == Idle || mode == Done)
	atAnvil := !moving && !asleep

	// moon nightlight and stars
	if asleep {
		glow(3.5, 3.8, 7.3, moonCol, 0.22)
	} else {
		glow(3.5, 3.8, 5.3, moonCol, 0.12)
	}
	R(3, 2, 2, 1, moonCol, 1)
	R(2, 3, 1, 2, moonCol, 1)
	R(3, 5, 2, 1, moonCol, 1)
	tw := 0.5 + 0.5*math.Sin(secs*2.2)
	R(7, 2, 1, 1, starCol, 0.4+0.6*tw)
	R(1, 7, 1, 1, starCol, 1-0.6*tw)

	// crystals by the bed and anvil
	cg := 0.55 + 0.45*math.Sin(secs*1.3)
	glow(38.5, 7.5, 3.3, crystalA, 0.35*cg)
	R(38, 6, 1, 2, crystalA, 0.6+0.4*cg)
	R(37, 7, 1, 1, crystalA, 0.5+0.4*cg)
	glow(16.5, 10.5, 2.7, crystalB, 0.3*(1.5-cg))
	R(16, 10, 1, 2, crystalB, 0.9-0.3*cg)
	R(11, 5, 1, 1, crystalA, 0.4+0.5*(1-cg))

	if mode == Working && atAnvil {
		glow(33.5, 11, 14.7, hot, 0.18+0.30*math.Max(0, 1-t/0.35))
	}

	// bed
	R(1, 8, 2, 1, bedFrameHi, 1)
	R(1, 9, 2, 5, bedFrame, 1)
	R(13, 10, 1, 4, bedFrame, 1)
	R(13, 10, 1, 1, bedFrameHi, 1)
	R(1, 13, 13, 1, bedFrame, 1)
	R(2, 14, 1, 1, bedFrame, 1)
	R(12, 14, 1, 1, bedFrame, 1)
	R(3, 12, 10, 1, sheetCol, 1)
	R(3, 10, 3, 2, pillowCol, 1)

	if asleep {
		R(3, 7, 1, 1, steelDark, 1)
		R(3, 8, 4, 3, clay, 1)
		R(4, 9, 3, 1, visor, 1)
		eye := cream
		if mode == Done {
			eye = okGreen
		}
		R(4, 9, 1, 1, eye, 0.4)
		R(6, 9, 1, 1, eye, 0.4)
		breath := math.Sin(secs*1.9) > -0.35
		R(6, 11, 7, 2, blanket, 1)
		if breath {
			R(7, 10, 5, 1, blanket, 1)
		}
		R(6, 11, 1, 1, sheetCol, 1)
		by := 11
		if breath {
			by = 10
		}
		R(8, by, 1, 1, blanketHi, 1)
		R(10, 12, 1, 1, starCol, 1)
		R(12, 11, 1, 1, starCol, 1)
		// rising z's
		for k := 0; k < 3; k++ {
			ph := math.Mod(secs*0.42+float64(k)/3, 1)
			a := math.Sin(ph * math.Pi)
			u := max(1, int(fcs*(0.45+ph*0.45)))
			zx := sx + int((7.5+ph*7)*fcs) - u
			zy := sy + int((7.5-ph*6.5)*fcs) - u
			for _, d := range [][2]int{{0, 0}, {1, 0}, {2, 0}, {1, 1}, {0, 2}, {1, 2}, {2, 2}} {
				c.Rect(zx+d[0]*u, zy+d[1]*u, u, u, zCol, a*0.95)
			}
		}
	} else {
		R(8, 11, 5, 2, blanket, 1)
		R(8, 11, 5, 1, blanketHi, 1)
		R(10, 12, 1, 1, starCol, 1)
	}

	// anvil
	R(30, 12, 8, 1, steel, 1)
	R(38, 12, 1, 1, steel, 1)
	R(32, 13, 4, 1, steelDark, 1)
	R(31, 14, 6, 1, steel, 1)
	if mode == Working && atAnvil {
		col := hot
		if t < 0.2 {
			col = hotPale
		}
		R(31, 11, 5, 1, col, 1)
	}
	if !atAnvil {
		R(31, 11, 3, 1, wood, 1)
		R(34, 10, 2, 2, steel, 1)
	}
	if asleep {
		return
	}

	// the bot, standing or walking
	flip := moving && f.WalkingLeft()
	s := func(x, y, w, h int, col RGB, a float64) {
		lx := x
		if flip {
			lx = 12 - x - w
		}
		R(lx+ox-3, y+2, w, h, col, a)
	}
	eye, tip := cream, hot
	switch mode {
	case Done:
		eye, tip = okGreen, okGreen
	case NeedsInput:
		eye, tip = amber, amber
	}
	tipCol := steelDark
	if mode == Working || int(secs*2.5)%2 == 0 {
		tipCol = tip
	}

	if moving {
		ph := int(secs*8) % 2
		bob := ph
		s(5, 1+bob, 1, 1, tipCol, 1)
		s(5, 2+bob, 1, 1, steelDark, 1)
		s(3, 3+bob, 6, 4, clay, 1)
		s(5, 4+bob, 4, 2, visor, 1)
		s(6, 4+bob, 1, 2, eye, 1)
		s(8, 4+bob, 1, 2, eye, 1)
		s(4, 7+bob, 4, 4-bob, clayDark, 1)
		s(5, 8+bob, 2, 1, clay, 1)
		if ph == 0 {
			s(8, 8+bob, 1, 2, clay, 1)
			s(4, 11, 1, 2, steelDark, 1)
			s(7, 11, 1, 2, steelDark, 1)
		} else {
			s(3, 8+bob, 1, 2, clay, 1)
			s(5, 11, 1, 2, steelDark, 1)
			s(6, 11, 1, 2, steelDark, 1)
		}
		return
	}

	fr := 0
	if mode == Working {
		switch {
		case t < 0.14:
			fr = 0
		case t < 0.28:
			fr = 1
		case t < 0.68:
			fr = 2
		default:
			fr = 1
		}
	}
	bob := 0
	if mode == Working && fr == 0 {
		bob = 1
	}
	blink := mode == Working && int(secs*2)%9 == 0
	s(5, 1+bob, 1, 1, tipCol, 1)
	s(5, 2+bob, 1, 1, steelDark, 1)
	s(3, 3+bob, 6, 4, clay, 1)
	s(5, 4+bob, 4, 2, visor, 1)
	if blink {
		s(6, 5+bob, 1, 1, eye, 0.45)
		s(8, 5+bob, 1, 1, eye, 0.45)
	} else {
		s(6, 4+bob, 1, 2, eye, 1)
		s(8, 4+bob, 1, 2, eye, 1)
	}
	s(4, 7+bob, 4, 4-bob, clayDark, 1)
	s(5, 8+bob, 2, 1, clay, 1)
	s(4, 11, 1, 2, steelDark, 1)
	s(7, 11, 1, 2, steelDark, 1)
	switch fr {
	case 0:
		s(8, 8, 3, 1, clay, 1)
		s(11, 8, 2, 1, wood, 1)
		s(13, 7, 2, 2, steel, 1)
	case 1:
		s(8, 8, 1, 1, clay, 1)
		s(9, 7, 1, 1, clay, 1)
		s(10, 6, 1, 1, wood, 1)
		s(11, 5, 1, 1, wood, 1)
		s(12, 3, 2, 2, steel, 1)
	default:
		s(8, 8, 1, 1, clay, 1)
		s(9, 7, 1, 1, clay, 1)
		s(10, 6, 1, 1, clay, 1)
		s(10, 4, 1, 2, wood, 1)
		s(9, 1, 3, 2, steel, 1)
	}

	if mode == Working && t >= 0 && t < 0.38 {
		k := t / 0.38
		dirs := [][2]float64{{-1.6, -2.2}, {0.8, -3.4}, {2.6, -2.4}, {3.8, -1.2}, {-0.6, -3.6}, {1.8, -1.0}}
		for i, d := range dirs {
			x := 33.0 + d[0]*k*1.7
			y := 10.5 + d[1]*k*1.7 + 3.0*k*k
			col := hot
			if i%2 == 1 {
				col = sparkPale
			}
			c.Rect(sx+int(x*fcs), sy+int(y*fcs), cs, cs, col, 1-k)
		}
	}
}

var circle9 = []string{
	"..xxxxx..",
	".xxxxxxx.",
	"xxxxxxxxx",
	"xxxxxxxxx",
	"xxxxxxxxx",
	"xxxxxxxxx",
	"xxxxxxxxx",
	".xxxxxxx.",
	"..xxxxx..",
}

func (r *Renderer) icon(c *Canvas, cx, cy int, col RGB, holes [][2]int) {
	cs := r.CS
	ox, oy := cx-9*cs/2, cy-9*cs/2
	hole := map[[2]int]bool{}
	for _, h := range holes {
		hole[h] = true
	}
	for y, row := range circle9 {
		for x, ch := range row {
			if ch == 'x' && !hole[[2]int{x, y}] {
				c.Rect(ox+x*cs, oy+y*cs, cs, cs, col, 1)
			}
		}
	}
}

func (r *Renderer) status(c *Canvas, f *Forge, now time.Time, cx, cy int) {
	cs := r.CS
	switch f.Mode {
	case Working:
		m := r.text(Clock(now.Sub(f.WorkStart)), 6*cs, 30*cs)
		if m != nil {
			c.Mask(m, cx-m.W/2, cy-m.H/2, hot, 0.95)
		}
	case Done:
		r.icon(c, cx, cy, okGreen, [][2]int{{2, 4}, {3, 5}, {4, 6}, {5, 5}, {6, 4}, {7, 3}})
	case NeedsInput:
		r.icon(c, cx, cy, amber, [][2]int{{4, 2}, {4, 3}, {4, 4}, {4, 5}, {4, 7}})
	default:
		c.Rect(cx-cs, cy-cs, 2*cs, 2*cs, white, 0.22)
	}
}

func (r *Renderer) strip(c *Canvas, f *Forge, now time.Time, accent *RGB, x0, y0, W, H int) {
	cs := r.CS
	px := 6 * cs
	maxW := (cellsWe - 14) * cs
	lx := x0 + 7*cs
	cy := y0 + 26*cs
	c.SetClip(x0, y0+20*cs, x0+W, y0+H)
	defer c.SetClip(x0, y0, x0+W, y0+H)

	rest, restA := white, 0.92
	if accent != nil {
		rest, restA = *accent, 1
	}
	if f.Line == "" {
		if m := r.text("heating the forge…", px, maxW); m != nil {
			c.Mask(m, lx, cy-m.H/2, white, 0.4)
		}
		return
	}
	age := now.Sub(f.LineAt).Seconds()
	e := easeOut(age / 0.25)
	if f.PrevLine != "" && e < 1 {
		if m := r.text(f.PrevLine, px, maxW); m != nil {
			c.Mask(m, lx, cy-m.H/2-int(e*6*float64(cs)), white, (1-e)*0.9)
		}
	}
	if m := r.text(f.Line, px, maxW); m != nil {
		col := lerp(hot, rest, easeOut(age/0.55))
		a := restA + (1-restA)*(1-easeOut(age/0.55))
		c.Mask(m, lx-int((1-e)*12*float64(cs)), cy-m.H/2, col, a*e)
	}
}
