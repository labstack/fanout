package dashboard

import (
	"cmp"
	"slices"

	"github.com/labstack/fanout/internal/panel"
)

// RowHeight is the grid row height in pixels; ui/host/src/dashboards/grid.tsx
// uses the same value.
const RowHeight = 40

const columns = 12

// Positions beyond these bounds are repacked rather than stored.
const (
	maxRow    = 1000
	maxHeight = 40
)

var heightRows = map[string]int{"s": 3, "m": 6, "l": 10}
var minimumRows = map[string]int{"stat": 4}

func firstFit(placed []panel.Grid, w, h int) (int, int) {
	for y := 0; ; y++ {
		for x := 0; x+w <= columns; x++ {
			if fits(placed, x, y, w, h) {
				return x, y
			}
		}
	}
}

func fits(placed []panel.Grid, x, y, w, h int) bool {
	for _, g := range placed {
		if x < g.X+g.W && g.X < x+w && y < g.Y+g.H && g.Y < y+h {
			return false
		}
	}
	return true
}

// needsPack reports whether any panel lacks a usable position or two overlap.
func needsPack(panels []panel.Panel) bool {
	placed := make([]panel.Grid, 0, len(panels))
	for _, p := range panels {
		g := p.Grid
		if g == nil || g.W < 1 || g.H < 1 || g.X < 0 || g.Y < 0 || g.W > columns || g.X > columns-g.W || g.Y > maxRow || g.H > maxHeight || !fits(placed, g.X, g.Y, g.W, g.H) {
			return true
		}
		placed = append(placed, *g)
	}
	return false
}

// PackMissing preserves every valid, non-overlapping supplied grid and packs
// only panels without usable coordinates around them.
func PackMissing(panels []panel.Panel) {
	packed := packMissing(panels)
	snapRowBands(panels, packed)
	fillRowBands(panels, packed)
}

func packMissing(panels []panel.Panel) []int {
	packed := make([]int, 0, len(panels))
	placed := make([]panel.Grid, 0, len(panels))
	for i := range panels {
		g := panels[i].Grid
		if g == nil {
			continue
		}
		if g.W < 1 || g.H < 1 || g.X < 0 || g.Y < 0 || g.W > columns || g.X > columns-g.W || g.Y > maxRow || g.H > maxHeight || !fits(placed, g.X, g.Y, g.W, g.H) {
			panels[i].Grid = nil
			continue
		}
		placed = append(placed, *g)
	}
	for i := range panels {
		if panels[i].Grid != nil {
			continue
		}
		w := min(max(panels[i].Width, 1), columns)
		h := heightRows[panels[i].Height]
		if h == 0 {
			h = heightRows["m"]
			if panels[i].Viz == "stat" {
				h = heightRows["s"]
			}
			if panels[i].Viz == "service_map" {
				h = heightRows["l"]
			}
		}
		h = max(h, minimumRows[panels[i].Viz])
		x, y := firstFit(placed, w, h)
		g := panel.Grid{X: x, Y: y, W: w, H: h}
		panels[i].Grid = &g
		packed = append(packed, i)
		placed = append(placed, g)
	}
	return packed
}

// Snap only newly packed panels. Scan rectangles, never individual grid cells
// or row coordinates, so work depends on panel count rather than grid Y.
func snapRowBands(panels []panel.Panel, packed []int) {
	bottoms := make(map[int]int, len(packed))
	placed := make([]panel.Grid, len(panels))
	for i, p := range panels {
		placed[i] = *p.Grid
	}
	for _, i := range packed {
		g := placed[i]
		bottoms[g.Y] = max(bottoms[g.Y], g.Y+g.H)
	}
	for _, i := range packed {
		g := panels[i].Grid
		bottom := bottoms[g.Y]
		if bottom > g.Y+g.H && bottom-g.Y <= maxHeight && fits(placed, g.X, g.Y+g.H, g.W, bottom-g.Y-g.H) {
			g.H = bottom - g.Y
			placed[i] = *g
		}
	}
}

// Fill only newly packed bands whose entire trailing rectangle is free.
// Share spare columns evenly, giving the leftmost panels the remainder.
func fillRowBands(panels []panel.Panel, packed []int) {
	slices.SortFunc(packed, func(a, b int) int {
		ga, gb := panels[a].Grid, panels[b].Grid
		return cmp.Or(cmp.Compare(ga.Y, gb.Y), cmp.Compare(ga.X, gb.X))
	})
	for start, end := 0, 0; start < len(packed); start = end {
		y, right, height := panels[packed[start]].Grid.Y, 0, 0
		for end = start; end < len(packed) && panels[packed[end]].Grid.Y == y; end++ {
			g := panels[packed[end]].Grid
			right, height = max(right, g.X+g.W), max(height, g.H)
		}
		spare := columns - right
		if spare == 0 {
			continue
		}
		band := packed[start:end]
		placed := make([]panel.Grid, 0, len(panels))
		for _, p := range panels {
			placed = append(placed, *p.Grid)
		}
		if !fits(placed, right, y, spare, height) {
			continue
		}
		// Remove this band's old rectangles before checking the new positions.
		for _, i := range band {
			placed[i] = panel.Grid{}
		}
		widened := make([]panel.Grid, 0, len(band))
		shift := 0
		for j, i := range band {
			g := *panels[i].Grid
			add := spare / len(band)
			if j < spare%len(band) {
				add++
			}
			g.X += shift
			g.W += add
			if !fits(placed, g.X, g.Y, g.W, g.H) {
				break
			}
			widened = append(widened, g)
			placed = append(placed, g)
			shift += add
		}
		if len(widened) == len(band) {
			for j, i := range band {
				*panels[i].Grid = widened[j]
			}
		}
	}
}

// Compact repairs unusable grids, then closes vertical gaps without changing
// usable columns or dimensions. Work depends on panel count, never grid Y.
func Compact(panels []panel.Panel) {
	packed := packMissing(panels)
	order := make([]int, 0, len(panels))
	for i, p := range panels {
		if p.Grid != nil {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(panels[a].Grid.Y, panels[b].Grid.Y) })
	placed := make([]panel.Grid, 0, len(order))
	for _, i := range order {
		g := *panels[i].Grid
		g.Y = 0
		for _, above := range placed {
			if g.X < above.X+above.W && above.X < g.X+g.W && g.Y < above.Y+above.H && above.Y < g.Y+g.H {
				g.Y = above.Y + above.H
			}
		}
		panels[i].Grid = &g
		at, _ := slices.BinarySearchFunc(placed, g, func(a, b panel.Grid) int { return cmp.Compare(a.Y, b.Y) })
		placed = slices.Insert(placed, at, g)
	}
	snapRowBands(panels, packed)
	fillRowBands(panels, packed)
}
