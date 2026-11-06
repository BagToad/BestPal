package slidepuzzle

import "sort"

// Boards are row-major slices of grid*grid cells. Tiles are numbered 1..n-1 by
// their home position (tile t belongs at index t-1); 0 marks the gap, which
// belongs in the last cell.

// solvedBoard returns the finished board for a grid.
func solvedBoard(grid int) []int {
	n := grid * grid
	b := make([]int, n)
	for i := 0; i < n-1; i++ {
		b[i] = i + 1
	}
	return b
}

func isSolved(b []int) bool {
	for i := 0; i < len(b)-1; i++ {
		if b[i] != i+1 {
			return false
		}
	}
	return b[len(b)-1] == 0
}

func gapIndex(b []int) int {
	for i, v := range b {
		if v == 0 {
			return i
		}
	}
	return -1
}

// neighbors returns the cells orthogonally adjacent to idx.
func neighbors(idx, grid int) []int {
	r, c := idx/grid, idx%grid
	var out []int
	if r > 0 {
		out = append(out, idx-grid)
	}
	if r < grid-1 {
		out = append(out, idx+grid)
	}
	if c > 0 {
		out = append(out, idx-1)
	}
	if c < grid-1 {
		out = append(out, idx+1)
	}
	return out
}

// movableTiles returns the tiles that can slide into the gap, ascending.
func movableTiles(b []int, grid int) []int {
	var out []int
	for _, idx := range neighbors(gapIndex(b), grid) {
		out = append(out, b[idx])
	}
	sort.Ints(out)
	return out
}

// applyMove slides tile into the gap, returning a new board. ok is false when
// the tile is not next to the gap.
func applyMove(b []int, grid, tile int) ([]int, bool) {
	gap := gapIndex(b)
	for _, idx := range neighbors(gap, grid) {
		if b[idx] == tile {
			out := append([]int(nil), b...)
			out[gap], out[idx] = tile, 0
			return out, true
		}
	}
	return nil, false
}

// moveDirection describes which way a movable tile slides into the gap.
func moveDirection(b []int, grid, tile int) string {
	gap := gapIndex(b)
	for _, idx := range neighbors(gap, grid) {
		if b[idx] != tile {
			continue
		}
		switch gap - idx {
		case -grid:
			return "⬆️ up"
		case grid:
			return "⬇️ down"
		case -1:
			return "⬅️ left"
		case 1:
			return "➡️ right"
		}
	}
	return ""
}

// shuffledBoard scrambles a solved board with random legal moves, so the
// result is always solvable. It never undoes the previous move and never
// returns an already-solved board.
func shuffledBoard(grid int, randN func(int) int) []int {
	b := solvedBoard(grid)
	steps := max(100, 40*grid*grid)
	prevGap := -1
	for i := 0; i < steps || isSolved(b); i++ {
		gap := gapIndex(b)
		var opts []int
		for _, idx := range neighbors(gap, grid) {
			if idx != prevGap {
				opts = append(opts, idx)
			}
		}
		idx := opts[randN(len(opts))]
		b[gap], b[idx] = b[idx], 0
		prevGap = gap
	}
	return b
}

// pickWinner returns the tile with the most votes among the movable tiles,
// breaking ties uniformly at random. ok is false when no movable tile has votes.
func pickWinner(tally map[int]int, movable []int, randN func(int) int) (int, bool) {
	best := 0
	var top []int
	for _, t := range movable {
		n := tally[t]
		switch {
		case n == 0:
		case n > best:
			best, top = n, []int{t}
		case n == best:
			top = append(top, t)
		}
	}
	if len(top) == 0 {
		return 0, false
	}
	return top[randN(len(top))], true
}
