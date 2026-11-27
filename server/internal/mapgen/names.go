package mapgen

import (
	"math/rand/v2"
	"strings"
)

// namer makes up place names from syllables. Every name it returns is new.
type namer struct {
	rng  *rand.Rand
	used map[string]bool
}

var (
	onsets = []string{"b", "br", "c", "d", "dr", "f", "g", "gr", "h", "k", "kr", "l", "m", "n", "p",
		"r", "s", "st", "t", "th", "tr", "v", "w", "z", "", ""}
	vowels  = []string{"a", "e", "i", "o", "u", "a", "e", "o", "ae", "ei", "ou", "ia"}
	codas   = []string{"", "", "", "n", "r", "s", "l", "m", "th", "nd", "rk", "st", "ld"}
	endings = []string{"", "", "", "", "ford", "mere", "holm", "vale", "wick", "dal", "mar", "stad", "ria", "ton"}
)

func newNamer(rng *rand.Rand) *namer {
	return &namer{rng: rng, used: map[string]bool{}}
}

func (n *namer) pick(list []string) string { return list[n.rng.IntN(len(list))] }

func (n *namer) word(syllables int) string {
	var b strings.Builder
	for i := 0; i < syllables; i++ {
		b.WriteString(n.pick(onsets))
		b.WriteString(n.pick(vowels))
		if i == syllables-1 || n.rng.IntN(3) == 0 {
			b.WriteString(n.pick(codas))
		}
	}
	return b.String()
}

// name returns a new capitalized name of two or three syllables, maybe with an ending.
func (n *namer) name() string {
	for {
		w := n.word(2 + n.rng.IntN(2))
		if len(w) < 4 {
			continue
		}
		if e := n.pick(endings); e != "" && len(w) < 7 {
			w += e
		}
		w = strings.ToUpper(w[:1]) + w[1:]
		if len(w) > 12 || n.used[w] {
			continue
		}
		n.used[w] = true
		return w
	}
}

var factionKinds = []string{"League", "Pact", "Union", "Federation", "Directorate", "Commonwealth", "Compact", "Concord"}

// faction returns a faction name and its key, e.g. "Veldora Pact" and "veldora".
func (n *namer) faction() (name, key string) {
	base := n.name()
	return base + " " + n.pick(factionKinds), strings.ToLower(base)
}
