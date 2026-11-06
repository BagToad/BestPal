package slidepuzzle

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSolvedBoard(t *testing.T) {
	b := solvedBoard(3)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 0}, b)
	assert.True(t, isSolved(b))
	assert.Equal(t, []int{6, 8}, movableTiles(b, 3))
}

func TestApplyMove(t *testing.T) {
	b := solvedBoard(3)
	nb, ok := applyMove(b, 3, 8)
	require.True(t, ok)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 0, 8}, nb)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 0}, b, "original board must not change")
	assert.Equal(t, "➡️ right", moveDirection(b, 3, 8))
	assert.Equal(t, "⬇️ down", moveDirection(b, 3, 6))

	_, ok = applyMove(b, 3, 1)
	assert.False(t, ok, "a tile away from the gap cannot move")
	assert.False(t, isSolved(nb))
}

// inversionsSolvable checks the standard solvability rule for n-puzzles.
func inversionsSolvable(b []int, grid int) bool {
	inv := 0
	for i := range b {
		for j := i + 1; j < len(b); j++ {
			if b[i] != 0 && b[j] != 0 && b[i] > b[j] {
				inv++
			}
		}
	}
	if grid%2 == 1 {
		return inv%2 == 0
	}
	rowFromBottom := grid - gapIndex(b)/grid
	return (inv+rowFromBottom)%2 == 1
}

func TestShuffledBoardIsValidSolvableAndScrambled(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for grid := 2; grid <= 8; grid++ {
		for range 20 {
			b := shuffledBoard(grid, r.IntN)
			require.Len(t, b, grid*grid)
			sorted := append([]int(nil), b...)
			sort.Ints(sorted)
			for i, v := range sorted {
				require.Equal(t, i, v, "board must be a permutation")
			}
			assert.False(t, isSolved(b))
			assert.True(t, inversionsSolvable(b, grid), "grid %d board %v must be solvable", grid, b)
		}
	}
}

func TestPickWinner(t *testing.T) {
	movable := []int{2, 4, 6}
	_, ok := pickWinner(map[int]int{}, movable, func(int) int { return 0 })
	assert.False(t, ok, "no votes means no winner")

	tile, ok := pickWinner(map[int]int{2: 1, 4: 3, 6: 2}, movable, func(int) int { return 0 })
	require.True(t, ok)
	assert.Equal(t, 4, tile)

	// Votes for tiles that are no longer movable are ignored.
	tile, ok = pickWinner(map[int]int{9: 10, 6: 1}, movable, func(int) int { return 0 })
	require.True(t, ok)
	assert.Equal(t, 6, tile)

	// Ties are a random pick among the tied tiles.
	tally := map[int]int{2: 2, 4: 1, 6: 2}
	var gotN int
	tile, _ = pickWinner(tally, movable, func(n int) int { gotN = n; return 1 })
	assert.Equal(t, 2, gotN, "random pick should be over the two tied tiles")
	assert.Equal(t, 6, tile)
	tile, _ = pickWinner(tally, movable, func(int) int { return 0 })
	assert.Equal(t, 2, tile)
}

func TestRenderBoard(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := range 600 {
		for x := range 400 {
			src.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	for _, solved := range []bool{false, true} {
		board := solvedBoard(5)
		if !solved {
			board, _ = applyMove(board, 5, 24)
		}
		out, err := renderBoard(src, 5, board, solved, map[int]int{20: 3, 23: 0})
		require.NoError(t, err)
		img, err := jpeg.Decode(bytes.NewReader(out))
		require.NoError(t, err)
		assert.Equal(t, 640, img.Bounds().Dx())
		assert.Equal(t, 960, img.Bounds().Dy())
	}
}

func TestDefaultImageRenders(t *testing.T) {
	src, err := decodeImage(defaultImage)
	require.NoError(t, err)
	_, err = renderBoard(src, 5, shuffledBoard(5, rand.IntN), false, nil)
	require.NoError(t, err)
}

func TestNormalizeUpload(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, big, nil))
	out, err := normalizeUpload(buf.Bytes())
	require.NoError(t, err)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	require.NoError(t, err)
	assert.Equal(t, 1280, cfg.Width)
	assert.Equal(t, 640, cfg.Height)

	_, err = normalizeUpload([]byte("not an image"))
	assert.Error(t, err)

	buf.Reset()
	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 20, 20)), nil))
	_, err = normalizeUpload(buf.Bytes())
	assert.ErrorContains(t, err, "at least 64px")
}

func TestRenderBoardDrawsVoteCounts(t *testing.T) {
	src, err := decodeImage(defaultImage)
	require.NoError(t, err)
	board, _ := applyMove(solvedBoard(5), 5, 24)
	plain, err := renderBoard(src, 5, board, false, nil)
	require.NoError(t, err)
	withVotes, err := renderBoard(src, 5, board, false, map[int]int{20: 2})
	require.NoError(t, err)
	assert.NotEqual(t, plain, withVotes)
}

func TestVoteLabelShortensWhenNarrow(t *testing.T) {
	face, err := labelFace(20)
	require.NoError(t, err)
	defer face.Close()
	assert.Equal(t, "1 vote", voteLabel(face, 1, 500))
	assert.Equal(t, "7 votes", voteLabel(face, 7, 500))
	assert.Equal(t, "7", voteLabel(face, 7, 10))
}

// The vote badge must stay blue on any background (a premultiplied colour
// with channels above alpha used to turn olive over light tiles).
func TestVoteBadgeColourOnLightTile(t *testing.T) {
	for _, bg := range []color.RGBA{{255, 255, 255, 255}, {0, 0, 0, 255}, {120, 140, 60, 255}} {
		dst := image.NewRGBA(image.Rect(0, 0, 200, 200))
		draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
		face, err := labelFace(20)
		require.NoError(t, err)
		drawVoteBadge(dst, dst.Bounds(), face, 3)
		face.Close()
		px := dst.RGBAAt(198, 198)
		assert.Greater(t, int(px.B), int(px.R)+60, "bg %v -> %v", bg, px)
		assert.Greater(t, int(px.B), int(px.G)+60, "bg %v -> %v", bg, px)
	}
}
