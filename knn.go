package rtree

import (
	"container/heap"
)

// KNNEntry is one item returned by a k-nearest-neighbor query.
type KNNEntry[N numeric, T any] struct {
	Min  [2]N
	Max  [2]N
	Data T
	Dist N
}

// KNNStats describes how much of an R-tree a KNN query accessed.
type KNNStats struct {
	// NodesVisited is the number of tree nodes that were expanded or scanned.
	NodesVisited int
	// NodeBoundsEvaluated is the number of node rectangle lower bounds calculated.
	NodeBoundsEvaluated int
	// ItemsEvaluated is the number of caller distance calls made for items.
	ItemsEvaluated int
	// NodesPruned is the number of queued nodes discarded by the kth bound.
	NodesPruned int
}

// KNN returns up to k nearest items in ascending distance order.
//
// The dist callback has the same form as Nearby's callback. When item is false,
// it receives a node bounding rectangle and its result is used as that node's
// priority and pruning bound. It must be a lower bound for every item in the
// node's subtree; this is the branch-and-bound contract and does not require a
// Euclidean distance. Use KNNExact when the caller's distance is an arbitrary
// black-box function for which no such lower bound is available.
func (tr *RTreeGN[N, T]) KNN(
	k int,
	dist func(min, max [2]N, data T, item bool) N,
) ([]KNNEntry[N, T], KNNStats) {
	var empty T
	return tr.KNNWithLowerBound(k,
		func(min, max [2]N, data T) N {
			return dist(min, max, data, true)
		},
		func(min, max [2]N) N {
			return dist(min, max, empty, false)
		},
	)
}

// KNNWithLowerBound is KNN with separate item-distance and node-bound funcs.
// lowerBound must satisfy lowerBound(min, max) <= itemDist(item) for every
// item whose rectangle is contained in min/max.
func (tr *RTreeGN[N, T]) KNNWithLowerBound(
	k int,
	itemDist func(min, max [2]N, data T) N,
	lowerBound func(min, max [2]N) N,
) ([]KNNEntry[N, T], KNNStats) {
	var stats KNNStats
	if tr.root == nil || k <= 0 || itemDist == nil || lowerBound == nil {
		return nil, stats
	}

	best := &knnBestHeap[N, T]{}
	heap.Init(best)

	q := tr.qpool.Get().(*queue[N, T])
	defer func() {
		*q = (*q)[:0]
		tr.qpool.Put(q)
	}()
	*q = (*q)[:0]

	rootBound := lowerBound(tr.rect.min, tr.rect.max)
	stats.NodeBoundsEvaluated++
	q.push(qnode[N, T]{
		dist: rootBound,
		rect: tr.rect,
		node: tr.root,
	})

	for len(*q) > 0 {
		nodes := *q
		head := nodes[0]
		if best.Len() == k && head.dist > best.threshold() {
			stats.NodesPruned += len(nodes)
			break
		}

		qn, _ := q.pop()
		n := qn.node
		if n == nil {
			continue
		}
		stats.NodesVisited++

		if n.leaf() {
			rects := n.rects[:n.count]
			items := n.items()[:n.count]
			for i := 0; i < len(rects); i++ {
				d := itemDist(rects[i].min, rects[i].max, items[i])
				stats.ItemsEvaluated++
				best.add(KNNEntry[N, T]{
					Min:  rects[i].min,
					Max:  rects[i].max,
					Data: items[i],
					Dist: d,
				}, k)
			}
			continue
		}

		rects := n.rects[:n.count]
		children := n.children()[:n.count]
		for i := 0; i < len(rects); i++ {
			bound := lowerBound(rects[i].min, rects[i].max)
			stats.NodeBoundsEvaluated++
			q.push(qnode[N, T]{
				dist: bound,
				rect: rects[i],
				node: children[i],
			})
		}
	}

	return best.ascending(), stats
}

// KNNExact returns the same top-k result as scanning every item. It makes no
// geometric or metric assumption about dist, so an arbitrary caller function
// is safe. The returned stats count the full tree and all items.
func (tr *RTreeGN[N, T]) KNNExact(
	k int,
	dist func(min, max [2]N, data T) N,
) ([]KNNEntry[N, T], KNNStats) {
	var stats KNNStats
	if tr.root == nil || k <= 0 || dist == nil {
		return nil, stats
	}

	best := &knnBestHeap[N, T]{}
	heap.Init(best)
	tr.root.knnExact(k, dist, best, &stats)
	return best.ascending(), stats
}

func (n *node[N, T]) knnExact(
	k int,
	dist func(min, max [2]N, data T) N,
	best *knnBestHeap[N, T],
	stats *KNNStats,
) {
	stats.NodesVisited++
	if n.leaf() {
		rects := n.rects[:n.count]
		items := n.items()[:n.count]
		for i := 0; i < len(rects); i++ {
			d := dist(rects[i].min, rects[i].max, items[i])
			stats.ItemsEvaluated++
			best.add(KNNEntry[N, T]{
				Min:  rects[i].min,
				Max:  rects[i].max,
				Data: items[i],
				Dist: d,
			}, k)
		}
		return
	}
	children := n.children()[:n.count]
	for i := 0; i < len(children); i++ {
		children[i].knnExact(k, dist, best, stats)
	}
}

// BoxLowerBound returns the squared box-distance lower bound used by BoxDist.
func BoxLowerBound[N numeric](targetMin, targetMax [2]N,
) func(min, max [2]N) N {
	targ := rect[N]{min: targetMin, max: targetMax}
	return func(min, max [2]N) N {
		return targ.boxDist(&rect[N]{min: min, max: max})
	}
}

type knnBestHeap[N numeric, T any] []KNNEntry[N, T]

func (h knnBestHeap[N, T]) Len() int { return len(h) }

// Less orders by descending distance, making h[0] the current kth threshold.
func (h knnBestHeap[N, T]) Less(i, j int) bool {
	return h[i].Dist > h[j].Dist
}

func (h knnBestHeap[N, T]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *knnBestHeap[N, T]) Push(x any) {
	*h = append(*h, x.(KNNEntry[N, T]))
}

func (h *knnBestHeap[N, T]) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func (h *knnBestHeap[N, T]) threshold() N {
	return (*h)[0].Dist
}

func (h *knnBestHeap[N, T]) add(entry KNNEntry[N, T], k int) {
	if h.Len() < k {
		heap.Push(h, entry)
		return
	}
	if entry.Dist < h.threshold() {
		(*h)[0] = entry
		heap.Fix(h, 0)
	}
}

func (h *knnBestHeap[N, T]) ascending() []KNNEntry[N, T] {
	out := make([]KNNEntry[N, T], h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(KNNEntry[N, T])
	}
	return out
}

// KNN is the float64 wrapper for RTreeG.KNN.
func (tr *RTreeG[T]) KNN(
	k int,
	dist func(min, max [2]float64, data T, item bool) float64,
) ([]KNNEntry[float64, T], KNNStats) {
	return tr.base.KNN(k, dist)
}

// KNNWithLowerBound is the float64 wrapper for RTreeG.KNNWithLowerBound.
func (tr *RTreeG[T]) KNNWithLowerBound(
	k int,
	itemDist func(min, max [2]float64, data T) float64,
	lowerBound func(min, max [2]float64) float64,
) ([]KNNEntry[float64, T], KNNStats) {
	return tr.base.KNNWithLowerBound(k, itemDist, lowerBound)
}

// KNNExact is the float64 wrapper for RTreeG.KNNExact.
func (tr *RTreeG[T]) KNNExact(
	k int,
	dist func(min, max [2]float64, data T) float64,
) ([]KNNEntry[float64, T], KNNStats) {
	return tr.base.KNNExact(k, dist)
}

// KNN is the any-data wrapper for RTree.KNN.
func (tr *RTree) KNN(
	k int,
	dist func(min, max [2]float64, data any, item bool) float64,
) ([]KNNEntry[float64, any], KNNStats) {
	return tr.base.KNN(k, dist)
}

// KNNWithLowerBound is the any-data wrapper for RTree.KNNWithLowerBound.
func (tr *RTree) KNNWithLowerBound(
	k int,
	itemDist func(min, max [2]float64, data any) float64,
	lowerBound func(min, max [2]float64) float64,
) ([]KNNEntry[float64, any], KNNStats) {
	return tr.base.KNNWithLowerBound(k, itemDist, lowerBound)
}

// KNNExact is the any-data wrapper for RTree.KNNExact.
func (tr *RTree) KNNExact(
	k int,
	dist func(min, max [2]float64, data any) float64,
) ([]KNNEntry[float64, any], KNNStats) {
	return tr.base.KNNExact(k, dist)
}
