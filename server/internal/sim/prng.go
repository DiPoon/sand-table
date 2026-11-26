package sim

import "math/bits"

// Purposes separate the random streams of different systems.
const (
	purposeCombat          uint64 = 1
	purposeReinforce       uint64 = 2 // defending side
	purposeReinforceAttack uint64 = 3
)

// rng is a keyed SplitMix64 generator. It is derived from
// (seed, tick, purpose, entity) and carries no state between ticks.
type rng struct{ s uint64 }

func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func newRNG(seed, tick, purpose, entity uint64) rng {
	h := mix64(seed + 0x9E3779B97F4A7C15)
	h = mix64(h ^ tick)
	h = mix64(h ^ (purpose<<32 | entity))
	return rng{s: h}
}

func (r *rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	return mix64(r.s)
}

// intn returns a uniform value in [0, n). n must be > 0.
func (r *rng) intn(n int64) int64 {
	un := uint64(n)
	hi, lo := bits.Mul64(r.next(), un)
	if lo < un {
		thresh := -un % un
		for lo < thresh {
			hi, lo = bits.Mul64(r.next(), un)
		}
	}
	return int64(hi)
}
