package tokenize

// Stem implements the Porter (1980) stemming algorithm, in the form of the
// reference ANSI C implementation (with the later "bli"/"logi" amendments).
// Input must be lower-case ASCII letters; anything else is returned as-is.
func Stem(w string) string {
	if len(w) <= 2 {
		return w
	}
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return w
		}
	}
	z := &stemmer{b: []byte(w), k: len(w) - 1}
	z.step1ab()
	if z.k > 0 {
		z.step1c()
		z.step2()
		z.step3()
		z.step4()
		z.step5()
	}
	return string(z.b[:z.k+1])
}

type stemmer struct {
	b    []byte
	k, j int
}

// cons reports whether b[i] is a consonant.
func (z *stemmer) cons(i int) bool {
	switch z.b[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		if i == 0 {
			return true
		}
		return !z.cons(i - 1)
	}
	return true
}

// m measures the number of consonant sequences between 0 and j:
// <c><v> -> 0, <c>vc<v> -> 1, <c>vcvc<v> -> 2, ...
func (z *stemmer) m() int {
	n, i := 0, 0
	for {
		if i > z.j {
			return n
		}
		if !z.cons(i) {
			break
		}
		i++
	}
	i++
	for {
		for {
			if i > z.j {
				return n
			}
			if z.cons(i) {
				break
			}
			i++
		}
		i++
		n++
		for {
			if i > z.j {
				return n
			}
			if !z.cons(i) {
				break
			}
			i++
		}
		i++
	}
}

func (z *stemmer) vowelInStem() bool {
	for i := 0; i <= z.j; i++ {
		if !z.cons(i) {
			return true
		}
	}
	return false
}

func (z *stemmer) doublec(j int) bool {
	if j < 1 || z.b[j] != z.b[j-1] {
		return false
	}
	return z.cons(j)
}

// cvc: i-2,i-1,i has the form consonant-vowel-consonant and the last
// consonant is not w, x or y.
func (z *stemmer) cvc(i int) bool {
	if i < 2 || !z.cons(i) || z.cons(i-1) || !z.cons(i-2) {
		return false
	}
	switch z.b[i] {
	case 'w', 'x', 'y':
		return false
	}
	return true
}

func (z *stemmer) ends(s string) bool {
	l := len(s)
	if l > z.k+1 || string(z.b[z.k-l+1:z.k+1]) != s {
		return false
	}
	z.j = z.k - l
	return true
}

func (z *stemmer) setto(s string) {
	z.b = append(z.b[:z.j+1], s...)
	z.k = z.j + len(s)
}

func (z *stemmer) r(s string) {
	if z.m() > 0 {
		z.setto(s)
	}
}

func (z *stemmer) step1ab() {
	if z.b[z.k] == 's' {
		switch {
		case z.ends("sses"):
			z.k -= 2
		case z.ends("ies"):
			z.setto("i")
		case z.b[z.k-1] != 's':
			z.k--
		}
	}
	if z.ends("eed") {
		if z.m() > 0 {
			z.k--
		}
	} else if (z.ends("ed") || z.ends("ing")) && z.vowelInStem() {
		z.k = z.j
		switch {
		case z.ends("at"):
			z.setto("ate")
		case z.ends("bl"):
			z.setto("ble")
		case z.ends("iz"):
			z.setto("ize")
		case z.doublec(z.k):
			z.k--
			if ch := z.b[z.k]; ch == 'l' || ch == 's' || ch == 'z' {
				z.k++
			}
		default:
			z.j = z.k
			if z.m() == 1 && z.cvc(z.k) {
				z.setto("e")
			}
		}
	}
}

func (z *stemmer) step1c() {
	if z.ends("y") && z.vowelInStem() {
		z.b[z.k] = 'i'
	}
}

type rule struct{ suffix, repl string }

func (z *stemmer) applyRules(rules []rule) {
	for _, ru := range rules {
		if z.ends(ru.suffix) {
			z.r(ru.repl)
			return
		}
	}
}

var step2Rules = []rule{
	{"ational", "ate"}, {"tional", "tion"}, {"enci", "ence"}, {"anci", "ance"},
	{"izer", "ize"}, {"bli", "ble"}, {"alli", "al"}, {"entli", "ent"}, {"eli", "e"},
	{"ousli", "ous"}, {"ization", "ize"}, {"ation", "ate"}, {"ator", "ate"},
	{"alism", "al"}, {"iveness", "ive"}, {"fulness", "ful"}, {"ousness", "ous"},
	{"aliti", "al"}, {"iviti", "ive"}, {"biliti", "ble"}, {"logi", "log"},
}

var step3Rules = []rule{
	{"icate", "ic"}, {"ative", ""}, {"alize", "al"}, {"iciti", "ic"},
	{"ical", "ic"}, {"ful", ""}, {"ness", ""},
}

func (z *stemmer) step2() { z.applyRules(step2Rules) }
func (z *stemmer) step3() { z.applyRules(step3Rules) }

var step4Suffixes = []string{
	"al", "ance", "ence", "er", "ic", "able", "ible", "ant", "ement", "ment",
	"ent", "ion", "ou", "ism", "ate", "iti", "ous", "ive", "ize",
}

func (z *stemmer) step4() {
	for _, s := range step4Suffixes {
		if !z.ends(s) {
			continue
		}
		if s == "ion" && !(z.j >= 0 && (z.b[z.j] == 's' || z.b[z.j] == 't')) {
			return
		}
		if z.m() > 1 {
			z.k = z.j
		}
		return
	}
}

func (z *stemmer) step5() {
	z.j = z.k
	if z.b[z.k] == 'e' {
		a := z.m()
		if a > 1 || (a == 1 && !z.cvc(z.k-1)) {
			z.k--
		}
	}
	if z.b[z.k] == 'l' && z.doublec(z.k) && z.m() > 1 {
		z.k--
	}
}
