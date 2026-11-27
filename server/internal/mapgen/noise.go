package mapgen

import (
	"math"
	"math/rand/v2"
)

// noise is 2D gradient noise with a seeded permutation, summed over octaves.
type noise struct {
	perm [512]uint8
}

func newNoise(rng *rand.Rand) *noise {
	n := &noise{}
	p := rng.Perm(256)
	for i := 0; i < 512; i++ {
		n.perm[i] = uint8(p[i&255])
	}
	return n
}

func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// grad returns the dot product of one of eight gradient directions with (x, y).
func grad(h uint8, x, y float64) float64 {
	switch h & 7 {
	case 0:
		return x + y
	case 1:
		return x - y
	case 2:
		return -x + y
	case 3:
		return -x - y
	case 4:
		return x
	case 5:
		return -x
	case 6:
		return y
	}
	return -y
}

// at returns noise in about [-1, 1].
func (n *noise) at(x, y float64) float64 {
	xf, yf := math.Floor(x), math.Floor(y)
	xi, yi := int(xf)&255, int(yf)&255
	x, y = x-xf, y-yf
	u, v := fade(x), fade(y)
	p := &n.perm
	aa, ab := p[int(p[xi])+yi], p[int(p[xi])+yi+1]
	ba, bb := p[int(p[xi+1])+yi], p[int(p[xi+1])+yi+1]
	return lerp(
		lerp(grad(aa, x, y), grad(ba, x-1, y), u),
		lerp(grad(ab, x, y-1), grad(bb, x-1, y-1), u),
		v)
}

// fbm sums octaves of noise, each twice the frequency and half the amplitude.
func (n *noise) fbm(x, y float64, octaves int) float64 {
	sum, amp, norm := 0.0, 1.0, 0.0
	for i := 0; i < octaves; i++ {
		sum += amp * n.at(x, y)
		norm += amp
		amp /= 2
		x, y = x*2, y*2
	}
	return sum / norm
}
