// Command icongen renders dsky.ico — the app icon used by the Start-menu
// and desktop shortcuts. Run once to (re)generate: go run ./test/icongen dsky.ico
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
)

const size = 256

func main() {
	out := "dsky.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	img := render()
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		panic(err)
	}
	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if strings.HasSuffix(strings.ToLower(out), ".png") {
		f.Write(pngBuf.Bytes())
		return
	}
	writeICO(f, pngBuf.Bytes())
}

var (
	blue   = color.NRGBA{0x25, 0x63, 0xEB, 0xFF} // accent
	blueLo = color.NRGBA{0x16, 0x3E, 0x9E, 0xFF} // deeper for the gradient
	white  = color.NRGBA{0xF4, 0xF8, 0xFF, 0xFF}
)

func render() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const radius = 48.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if a := roundedRectCoverage(float64(x), float64(y), 0, 0, size, size, radius); a > 0 {
				t := float64(y) / size
				bg := lerp(blue, blueLo, t)
				img.SetNRGBA(x, y, alpha(bg, a))
			}
		}
	}

	// A white "write-to-device" glyph: a down arrow onto a tray bar.
	cx := float64(size) / 2
	drawRect(img, cx-16, 60, cx+16, 150, white)                   // shaft
	drawTriangle(img, cx, 196, cx-46, 132, cx+46, 132, white)     // arrowhead
	drawRect(img, cx-58, 206, cx+58, 222, white)                  // device tray
	return img
}

// roundedRectCoverage returns 1 inside a rounded rect, 0 outside, with a
// 1px feathered edge for anti-aliasing.
func roundedRectCoverage(px, py, x0, y0, w, h, r float64) float64 {
	x1, y1 := x0+w, y0+h
	// distance outside the rounded rectangle
	dx := math.Max(math.Max(x0+r-px, px-(x1-r)), 0)
	dy := math.Max(math.Max(y0+r-py, py-(y1-r)), 0)
	var d float64
	if (px < x0+r || px > x1-r) && (py < y0+r || py > y1-r) {
		d = math.Hypot(dx, dy) - r
	} else if px < x0 || px > x1 || py < y0 || py > y1 {
		d = math.Max(math.Max(x0-px, px-x1), math.Max(y0-py, py-y1))
	} else {
		d = -1
	}
	if d <= -1 {
		return 1
	}
	if d >= 0 {
		return math.Max(0, 1-d)
	}
	return 1
}

func drawRect(img *image.NRGBA, x0, y0, x1, y1 float64, c color.NRGBA) {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			blend(img, x, y, c)
		}
	}
}

func drawTriangle(img *image.NRGBA, ax, ay, bx, by, cx, cy float64, c color.NRGBA) {
	minX, maxX := int(math.Min(ax, math.Min(bx, cx))), int(math.Max(ax, math.Max(bx, cx)))
	minY, maxY := int(math.Min(ay, math.Min(by, cy))), int(math.Max(ay, math.Max(by, cy)))
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if inTriangle(float64(x)+0.5, float64(y)+0.5, ax, ay, bx, by, cx, cy) {
				blend(img, x, y, c)
			}
		}
	}
}

func inTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	d1 := sign(px, py, ax, ay, bx, by)
	d2 := sign(px, py, bx, by, cx, cy)
	d3 := sign(px, py, cx, cy, ax, ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func sign(px, py, ax, ay, bx, by float64) float64 {
	return (px-bx)*(ay-by) - (ax-bx)*(py-by)
}

func blend(img *image.NRGBA, x, y int, c color.NRGBA) {
	if x < 0 || y < 0 || x >= size || y >= size {
		return
	}
	if img.NRGBAAt(x, y).A == 0 {
		return // keep transparency outside the rounded square
	}
	img.SetNRGBA(x, y, c)
}

func lerp(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		0xFF,
	}
}

func alpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(a * 255)
	return c
}

// writeICO wraps a PNG in a single-image ICO container (valid on Vista+).
func writeICO(f *os.File, pngBytes []byte) {
	var hdr bytes.Buffer
	binary.Write(&hdr, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&hdr, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&hdr, binary.LittleEndian, uint16(1)) // count
	// ICONDIRENTRY
	hdr.WriteByte(0) // width 0 => 256
	hdr.WriteByte(0) // height 0 => 256
	hdr.WriteByte(0) // colors
	hdr.WriteByte(0) // reserved
	binary.Write(&hdr, binary.LittleEndian, uint16(1))              // planes
	binary.Write(&hdr, binary.LittleEndian, uint16(32))             // bpp
	binary.Write(&hdr, binary.LittleEndian, uint32(len(pngBytes)))  // size
	binary.Write(&hdr, binary.LittleEndian, uint32(6+16))           // offset
	f.Write(hdr.Bytes())
	f.Write(pngBytes)
}
