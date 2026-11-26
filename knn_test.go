package rtree

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type knnFixture struct {
	tree  *RTreeG[int]
	rects []rect[float64]
}

func newKNNFixture(n int, seed int64) knnFixture {
	rng := rand.New(rand.NewSource(seed))
	rects := make([]rect[float64], n)
	var tree RTreeG[int]
	for i := 0; i < n; i++ {
		min := [2]float64{rng.Float64()*360 - 180, rng.Float64()*180 - 90}
		max := min
		if rng.Intn(2) == 0 {
			max[0] += rng.Float64() * 0.1
			max[1] += rng.Float64() * 0.1
		}
		rects[i] = rect[float64]{min: min, max: max}
		tree.Insert(min, max, i)
	}
	return knnFixture{tree: &tree, rects: rects}
}

func bruteKNN(rects []rect[float64], k int,
	dist func(min, max [2]float64, data int) float64,
) []KNNEntry[float64, int] {
	all := make([]KNNEntry[float64, int], len(rects))
	for i, r := range rects {
		all[i] = KNNEntry[float64, int]{
			Min:  r.min,
			Max:  r.max,
			Data: i,
			Dist: dist(r.min, r.max, i),
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Dist != all[j].Dist {
			return all[i].Dist < all[j].Dist
		}
		return all[i].Data < all[j].Data
	})
	if k < len(all) {
		all = all[:k]
	}
	return all
}

func assertKNNMatchesBrute(t *testing.T,
	got []KNNEntry[float64, int], want []KNNEntry[float64, int],
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].Data != want[i].Data || got[i].Dist != want[i].Dist {
			t.Fatalf("result %d mismatch: got (id=%d dist=%v), want (id=%d dist=%v)",
				i, got[i].Data, got[i].Dist, want[i].Data, want[i].Dist)
		}
		if i > 0 && got[i].Dist < got[i-1].Dist {
			t.Fatalf("results out of order at %d: %v < %v",
				i, got[i].Dist, got[i-1].Dist)
		}
	}
}

func TestKNNBox100kPruning(t *testing.T) {
	const n = 100_000
	const k = 10
	fx := newKNNFixture(n, 0x5eed1234)
	query := [2]float64{10, 20}
	algo := BoxDist[float64, int](query, query, nil)
	itemDist := func(min, max [2]float64, data int) float64 {
		return algo(min, max, data, true)
	}

	got, stats := fx.tree.KNN(k, algo)
	want := bruteKNN(fx.rects, k, itemDist)
	assertKNNMatchesBrute(t, got, want)

	_, fullStats := fx.tree.KNNExact(k, itemDist)
	t.Logf("box 100k k=%d: visited=%d/%d nodes, bounds=%d, items=%d/%d, pruned=%d",
		k, stats.NodesVisited, fullStats.NodesVisited,
		stats.NodeBoundsEvaluated, stats.ItemsEvaluated, n, stats.NodesPruned)

	if stats.ItemsEvaluated >= n/10 {
		t.Fatalf("expected item pruning, evaluated %d of %d", stats.ItemsEvaluated, n)
	}
	if stats.NodesVisited >= fullStats.NodesVisited/5 {
		t.Fatalf("expected node pruning, visited %d of %d",
			stats.NodesVisited, fullStats.NodesVisited)
	}
	if stats.NodesPruned == 0 {
		t.Fatal("expected at least one queued node to be pruned")
	}

	weightedItem := func(min, max [2]float64, data int) float64 {
		return algo(min, max, data, true) + float64(data%7)
	}
	weightedGot, weightedStats := fx.tree.KNNWithLowerBound(
		k, weightedItem, BoxLowerBound[float64](query, query),
	)
	weightedWant := bruteKNN(fx.rects, k, weightedItem)
	assertKNNMatchesBrute(t, weightedGot, weightedWant)
	if weightedStats.ItemsEvaluated >= n/10 {
		t.Fatalf("expected weighted-distance pruning, evaluated %d of %d",
			weightedStats.ItemsEvaluated, n)
	}
}

func TestKNNArbitraryDistanceIsExact(t *testing.T) {
	const n = 100_000
	const k = 10
	fx := newKNNFixture(n, 0xa1234567)

	// Deliberately independent of geometry and negative. A geometric mindist
	// cannot be a lower bound for this function.
	arbitrary := func(min, max [2]float64, data int) float64 {
		return -float64(data)
	}
	got, stats := fx.tree.KNNExact(k, arbitrary)
	want := bruteKNN(fx.rects, k, arbitrary)
	assertKNNMatchesBrute(t, got, want)

	if stats.ItemsEvaluated != n {
		t.Fatalf("arbitrary distance must evaluate every item: got %d, want %d",
			stats.ItemsEvaluated, n)
	}
	if stats.NodesVisited != fx.tree.base.countNodes() {
		t.Fatalf("arbitrary distance must scan every node: got %d, want %d",
			stats.NodesVisited, fx.tree.base.countNodes())
	}
}

func TestKNNCustomManhattanLowerBound(t *testing.T) {
	const n = 100_000
	const k = 10
	fx := newKNNFixture(n, 0x9e3779b9)
	query := [2]float64{-40, 10}

	axisGap := func(q, min, max float64) float64 {
		if q < min {
			return min - q
		}
		if q > max {
			return q - max
		}
		return 0
	}
	manhattan := func(min, max [2]float64, _ int, _ bool) float64 {
		return axisGap(query[0], min[0], max[0]) +
			2*axisGap(query[1], min[1], max[1])
	}
	itemDist := func(min, max [2]float64, data int) float64 {
		return manhattan(min, max, data, true)
	}

	got, stats := fx.tree.KNN(k, manhattan)
	want := bruteKNN(fx.rects, k, itemDist)
	assertKNNMatchesBrute(t, got, want)

	t.Logf("weighted Manhattan 100k k=%d: visited=%d nodes, items=%d/%d, pruned=%d",
		k, stats.NodesVisited, stats.ItemsEvaluated, n, stats.NodesPruned)
	if stats.ItemsEvaluated >= n/10 {
		t.Fatalf("expected custom-distance pruning, evaluated %d of %d",
			stats.ItemsEvaluated, n)
	}
}

func TestKNNRandomSmallMatchesBruteForce(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			const n = 1200
			fx := newKNNFixture(n, seed)
			query := [2]float64{
				rand.New(rand.NewSource(seed+1000)).Float64()*360 - 180,
				rand.New(rand.NewSource(seed+2000)).Float64()*180 - 90,
			}
			algo := BoxDist[float64, int](query, query, nil)
			itemDist := func(min, max [2]float64, data int) float64 {
				return algo(min, max, data, true)
			}
			for _, k := range []int{1, 3, 10, n} {
				got, _ := fx.tree.KNN(k, algo)
				want := bruteKNN(fx.rects, k, itemDist)
				assertKNNMatchesBrute(t, got, want)

				arbitrary := func(_, _ [2]float64, data int) float64 {
					return -float64(data)
				}
				gotExact, _ := fx.tree.KNNExact(k, arbitrary)
				wantExact := bruteKNN(fx.rects, k, arbitrary)
				assertKNNMatchesBrute(t, gotExact, wantExact)
			}
		})
	}
}

func TestKNNGenericIntAndBounds(t *testing.T) {
	var tr RTreeGN[int, int]
	tr.Insert([2]int{100, 100}, [2]int{100, 100}, 1)
	tr.Insert([2]int{200, 200}, [2]int{200, 200}, 2)

	got, stats := tr.KNN(1, BoxDist[int, int]([2]int{10, 10}, [2]int{10, 10}, nil))
	if len(got) != 1 || got[0].Data != 1 || got[0].Dist != 16200 {
		t.Fatalf("unexpected int KNN result: %+v", got)
	}
	if stats.ItemsEvaluated != 2 {
		t.Fatalf("expected both leaf items to be evaluated, got %+v", stats)
	}

	if got, stats := tr.KNN(0, nil); got != nil || stats != (KNNStats{}) {
		t.Fatalf("k=0 should be empty, got %+v %+v", got, stats)
	}

	var empty RTreeGN[int, int]
	if got, _ := empty.KNNExact(1, func(_, _ [2]int, data int) int {
		return data
	}); got != nil {
		t.Fatalf("empty tree should return nil, got %+v", got)
	}
}

func (tr *RTreeGN[N, T]) countNodes() int {
	if tr.root == nil {
		return 0
	}
	return tr.root.countNodes()
}

func (n *node[N, T]) countNodes() int {
	total := 1
	if !n.leaf() {
		for _, child := range n.children()[:n.count] {
			total += child.countNodes()
		}
	}
	return total
}
