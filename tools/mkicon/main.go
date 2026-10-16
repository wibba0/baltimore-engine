// Command mkicon draws the 1024×1024 app icon (a gold bar chart on a navy
// rounded square, following the macOS icon grid) as a PNG. The app build
// script turns it into AppIcon.icns with sips + iconutil.
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

const size = 1024

// roundedRectCoverage returns how much of pixel (x,y) lies inside a rounded
// rectangle, using 4×4 supersampling for smooth anti-aliased edges.
func roundedRectCoverage(x, y int, x0, y0, x1, y1, r float64) float64 {
	hit := 0
	for sy := 0; sy < 4; sy++ {
		for sx := 0; sx < 4; sx++ {
			px, py := float64(x)+(float64(sx)+.5)/4, float64(y)+(float64(sy)+.5)/4
			if px < x0 || px > x1 || py < y0 || py > y1 {
				continue
			}
			cx := math.Max(x0+r, math.Min(px, x1-r))
			cy := math.Max(y0+r, math.Min(py, y1-r))
			if (px-cx)*(px-cx)+(py-cy)*(py-cy) <= r*r {
				hit++
			}
		}
	}
	return float64(hit) / 16
}

func blend(dst color.NRGBA, src color.NRGBA, a float64) color.NRGBA {
	if a <= 0 {
		return dst
	}
	outA := a + float64(dst.A)/255*(1-a)
	if outA == 0 {
		return color.NRGBA{}
	}
	mix := func(s, d uint8) uint8 {
		return uint8((float64(s)*a + float64(d)*float64(dst.A)/255*(1-a)) / outA)
	}
	return color.NRGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), uint8(outA * 255)}
}

func lerp(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }

func main() {
	out := flag.String("o", "icon.png", "output PNG path")
	flag.Parse()

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	// macOS Big Sur+ grid: 824×824 body centred in 1024, corner radius ≈185.
	const inset, radius = 100.0, 185.0
	top := color.NRGBA{0x24, 0x38, 0x62, 0xff}
	bot := color.NRGBA{0x0c, 0x14, 0x26, 0xff}
	gold := color.NRGBA{0xf2, 0xb6, 0x31, 0xff}
	goldDim := color.NRGBA{0xc9, 0x8f, 0x1c, 0xff}

	bars := []struct{ h float64 }{{0.30}, {0.52}, {0.40}, {0.78}}
	const chartL, chartR, base, maxH = 250.0, 774.0, 760.0, 470.0
	gap := 34.0
	bw := (chartR - chartL - gap*float64(len(bars)-1)) / float64(len(bars))

	for y := 0; y < size; y++ {
		t := (float64(y) - inset) / (size - 2*inset)
		bg := color.NRGBA{lerp(top.R, bot.R, t), lerp(top.G, bot.G, t), lerp(top.B, bot.B, t), 0xff}
		for x := 0; x < size; x++ {
			c := color.NRGBA{}
			c = blend(c, bg, roundedRectCoverage(x, y, inset, inset, size-inset, size-inset, radius))
			for i, b := range bars {
				x0 := chartL + float64(i)*(bw+gap)
				col := gold
				if i%2 == 0 {
					col = goldDim
				}
				c = blend(c, col, roundedRectCoverage(x, y, x0, base-b.h*maxH, x0+bw, base, 18))
			}
			// baseline
			c = blend(c, color.NRGBA{0xe8, 0xec, 0xf4, 0xff}, 0.85*roundedRectCoverage(x, y, chartL-20, base+22, chartR+20, base+38, 8))
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
