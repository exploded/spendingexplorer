package explore

import (
	"math"
	"testing"
)

func TestMoney(t *testing.T) {
	for in, want := range map[int64]string{
		833_261_609_000: "$833.3bn",
		512_400_000:     "$512m",
		4_260_000:       "$4.3m",
		45_000:          "$45k",
		-12_500_000_000: "−$12.5bn",
	} {
		if got := Money(in); got != want {
			t.Errorf("Money(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviousYear(t *testing.T) {
	for in, want := range map[string]string{"2026-27": "2025-26", "2000-01": "1999-00"} {
		if got := PreviousYear(in); got != want {
			t.Errorf("PreviousYear(%s) = %s", in, got)
		}
	}
}

func TestTreemapCoversBoxAndPreservesArea(t *testing.T) {
	nodes := []Node{{Name: "a", Cur: 600}, {Name: "b", Cur: 300}, {Name: "c", Cur: 60}, {Name: "d", Cur: 40}, {Name: "neg", Cur: -5}}
	tiles := Treemap(nodes, 2)
	if len(tiles) != 4 {
		t.Fatalf("got %d tiles, want 4 (negative left out)", len(tiles))
	}
	var area float64
	for _, tl := range tiles {
		if tl.X < -1e-9 || tl.Y < -1e-9 || tl.X+tl.W > 100+1e-6 || tl.Y+tl.H > 100+1e-6 {
			t.Errorf("tile %s out of bounds: %+v", tl.Name, tl)
		}
		area += tl.W * tl.H
		if got := tl.W * tl.H / 100; math.Abs(got-tl.Share) > 1e-6 {
			t.Errorf("tile %s area %.4f%%, want share %.4f%%", tl.Name, got, tl.Share)
		}
	}
	if math.Abs(area-10000) > 1e-6 {
		t.Errorf("tiles cover %.4f of 10000", area)
	}
}
