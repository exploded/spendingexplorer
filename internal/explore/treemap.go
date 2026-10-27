package explore

import "sort"

// Tile is a node placed in the treemap. Position and size are percentages of
// the treemap's width and height.
type Tile struct {
	Node
	X, Y, W, H float64
	Share      float64 // per cent of the treemap total
	Label      bool    // big enough to carry a text label
}

// Treemap lays out nodes with positive Cur using the squarified algorithm
// (Bruls, Huizing and van Wijk) in a box of the given aspect ratio
// (width / height). Nodes with zero or negative amounts are left out; callers
// list them in the table instead.
func Treemap(nodes []Node, aspect float64) []Tile {
	var items []Node
	var total float64
	for _, n := range nodes {
		if n.Cur > 0 {
			items = append(items, n)
			total += float64(n.Cur)
		}
	}
	if total == 0 {
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Cur > items[j].Cur })

	// Work in a w x h box with area = w*h, then convert to percentages.
	w, h := 100*aspect, 100.0
	scale := w * h / total
	areas := make([]float64, len(items))
	for i, n := range items {
		areas[i] = float64(n.Cur) * scale
	}

	tiles := make([]Tile, 0, len(items))
	x, y := 0.0, 0.0
	for i := 0; i < len(items); {
		side := min(w, h)
		end := i + 1
		for end < len(items) && worst(areas[i:end+1], side) <= worst(areas[i:end], side) {
			end++
		}
		var sum float64
		for _, a := range areas[i:end] {
			sum += a
		}
		if w >= h {
			// Column on the left, items stacked top to bottom.
			colW := sum / h
			cy := y
			for k := i; k < end; k++ {
				th := areas[k] / colW
				tiles = append(tiles, tile(items[k], x, cy, colW, th, aspect, total))
				cy += th
			}
			x += colW
			w -= colW
		} else {
			// Row along the top, items left to right.
			rowH := sum / w
			cx := x
			for k := i; k < end; k++ {
				tw := areas[k] / rowH
				tiles = append(tiles, tile(items[k], cx, y, tw, rowH, aspect, total))
				cx += tw
			}
			y += rowH
			h -= rowH
		}
		i = end
	}
	return tiles
}

func tile(n Node, x, y, w, h, aspect, total float64) Tile {
	t := Tile{
		Node:  n,
		X:     x / aspect,
		Y:     y,
		W:     w / aspect,
		H:     h,
		Share: 100 * float64(n.Cur) / total,
	}
	t.Label = t.W >= 9 && t.H >= 14
	return t
}

// worst is the highest aspect ratio in a row of areas laid along side s.
func worst(row []float64, s float64) float64 {
	var sum, lo, hi float64
	lo = row[0]
	for _, a := range row {
		sum += a
		lo = min(lo, a)
		hi = max(hi, a)
	}
	s2, sum2 := s*s, sum*sum
	return max(s2*hi/sum2, sum2/(s2*lo))
}
