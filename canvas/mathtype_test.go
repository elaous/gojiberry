package canvas

import (
	"strings"
	"testing"
)

func drawn(formula string, size float64) (*fakePainter, mbox) {
	p := &fakePainter{}
	b := typeset(p, parseMath(formula), size)
	b.draw(p, 10, 100)
	return p, b
}

func TestTypesetFractionHasARuleAndPutsTheNumeratorAbove(t *testing.T) {
	p, b := drawn("frac{a}{b}", 20)
	if b.asc <= 14 || b.desc <= 4 {
		t.Fatalf("a fraction reaches above and below the baseline: %+v", b)
	}
	if !p.has(`fillText "a"`) || !p.has(`fillText "b"`) || !p.has("fillRect") {
		t.Fatalf("numerator, denominator and rule: %v", p.calls)
	}
	var ya, yb float64
	for _, c := range p.calls {
		if strings.HasPrefix(c, `fillText "a"`) {
			ya = lastNumber(c)
		}
		if strings.HasPrefix(c, `fillText "b"`) {
			yb = lastNumber(c)
		}
	}
	if !(ya < 100 && yb > 100) {
		t.Fatalf("the numerator is above the baseline (100) and the denominator below: %v %v", ya, yb)
	}
}

func lastNumber(call string) float64 {
	f := strings.Fields(call)
	var v float64
	for _, c := range f[len(f)-1:] {
		for i := 0; i < len(c); i++ {
			_ = i
		}
		neg := strings.HasPrefix(c, "-")
		c = strings.TrimPrefix(c, "-")
		whole, frac, _ := strings.Cut(c, ".")
		for _, d := range whole {
			v = v*10 + float64(d-'0')
		}
		scale := 0.1
		for _, d := range frac {
			v += float64(d-'0') * scale
			scale /= 10
		}
		if neg {
			v = -v
		}
	}
	return v
}

func TestTypesetScriptsAndRoots(t *testing.T) {
	_, plain := drawn("x", 20)
	_, sup := drawn("x^2", 20)
	_, sub := drawn("x_1", 20)
	if !(sup.asc > plain.asc && sub.desc > plain.desc && sup.w > plain.w) {
		t.Fatalf("a superscript raises, a subscript lowers: %+v %+v %+v", plain, sup, sub)
	}
	p, root := drawn("sqrt{x+1}", 20)
	if !p.has("lineTo") || root.asc <= plain.asc || !p.has(`fillText "x"`) {
		t.Fatalf("a root has a radical sign over its body: %v", p.calls)
	}
	p2, sum := drawn("sum_{i=1}^{n} i", 20)
	if !p2.has(`fillText "∑"`) || sum.asc <= plain.asc || sum.desc <= plain.desc {
		t.Fatalf("a sum with limits: %v %+v", p2.calls, sum)
	}
}

func TestTypesetReadsTheNotationLikeTheMathMLExport(t *testing.T) {
	got := strings.Join(MathSymbols("x^2 + y_1 = frac{a}{b} sqrt{2} pi alpha sin x sum int infty"), " ")
	want := "x 2 + y 1 = a b 2 π α sin x ∑ ∫ ∞"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	// two letters are two identifiers; the script goes to the last
	if got := strings.Join(MathSymbols("ab^2"), " "); got != "a b 2" {
		t.Fatalf("%q", got)
	}
	// broken input still gives a tree
	for _, f := range []string{"{{", "}}", "^", "frac{a", "sqrt", "x^", "_1", "\x00 1"} {
		MathSymbols(f)
		drawn(f, 16)
	}
}

func TestInlineEquationSitsOnTheBaselineOfItsLine(t *testing.T) {
	eq := &EquationObject{Formula: "x_1", Alt: "x sub 1", Inline: true}
	f, _ := selFlow(Chars{Text: "Let "}, Inline{Object: eq}, Chars{Text: " be given."})
	lines := f.Layout().Lines
	if eq.Descent() <= 0 {
		t.Fatal("a formula with a subscript has a descent")
	}
	if len(lines) != 1 || len(lines[0].Objects) != 1 {
		t.Fatalf("one line with the formula in it: %+v", lines)
	}
	o := lines[0].Objects[0]
	if got, want := o.Y+o.H-o.Descent, lines[0].Baseline; got < want-0.001 || got > want+0.001 {
		t.Fatalf("the formula's baseline (%v) is the line's (%v)", got, want)
	}
}

func TestBlockEquationIsTypesetNotTyped(t *testing.T) {
	eq := &EquationObject{Formula: "frac{a}{b}", Alt: "a over b"}
	p := &fakePainter{}
	eq.Measure(p, 300, 0)
	eq.DrawAt(p, 0, 0)
	if p.has(`fillText "frac{a}{b}"`) || !p.has(`fillText "a"`) {
		t.Fatalf("the formula is drawn as a fraction: %v", p.calls)
	}
	empty := &EquationObject{}
	p = &fakePainter{}
	empty.Measure(p, 300, 0)
	empty.DrawAt(p, 0, 0)
	if !p.has(`fillText "(empty equation)"`) {
		t.Fatal("an empty equation says so")
	}
}

func TestTypesetLimitsOfASumGoAboveAndBelowInABlock(t *testing.T) {
	size := 20.0
	beside := parseMath("sum_{i=1}^{n} i")
	above := parseMath("sum_{i=1}^{n} i")
	markLimits(above)
	pa, pb := &fakePainter{}, &fakePainter{}
	a, b := typeset(pa, beside, size), typeset(pb, above, size)
	if !(b.w < a.w) || !(b.asc > a.asc || b.desc > a.desc) {
		t.Fatalf("limits stack over the sum: beside %+v, above %+v", a, b)
	}
}

func TestTypesetBracketsGrowWithAFraction(t *testing.T) {
	p, tall := drawn("(frac{a}{b})", 20)
	_, small := drawn("(x)", 20)
	if tall.asc <= small.asc || !p.has("lineTo") || p.has(`fillText "("`) {
		t.Fatalf("a bracket round a fraction is drawn tall, not typed: %v %+v", p.calls, tall)
	}
	q, plain := drawn("(x)", 20)
	if !q.has(`fillText "("`) || plain.asc != small.asc {
		t.Fatalf("a bracket round a letter stays a character: %v", q.calls)
	}
}

func TestTypesetMatrixAndAlignedEquations(t *testing.T) {
	p, m := drawn(`matrix{a & b \\ c & d}`, 20)
	for _, w := range []string{`fillText "a"`, `fillText "b"`, `fillText "c"`, `fillText "d"`, "lineTo"} {
		if !p.has(w) {
			t.Fatalf("matrix lacks %s: %v", w, p.calls)
		}
	}
	_, one := drawn("a", 20)
	if m.asc+m.desc < 2*(one.asc+one.desc) {
		t.Fatalf("two rows: %+v", m)
	}
	if got := strings.Join(MathSymbols(`matrix{a & b \\ c & d}`), " "); got != "( a b c d )" {
		t.Fatalf("symbols %q", got)
	}
	q, _ := drawn(`aligned{x &= 1 \\ y &= 2}`, 20)
	if !q.has(`fillText "x"`) || !q.has(`fillText "="`) || q.has("lineTo") {
		t.Fatalf("aligned has no brackets: %v", q.calls)
	}
}
