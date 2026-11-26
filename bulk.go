package rtree

import (
	"math"
	"sort"
	"sync"
)

// BulkItemGN is one rectangle and its data for an in-place bulk load.
type BulkItemGN[N numeric, T any] struct {
	Min  [2]N
	Max  [2]N
	Data T
}

// BulkItem is a BulkItemGN using the package's default float64 coordinates.
type BulkItem[T any] = BulkItemGN[float64, T]

// Stats describes the packed tree shape.
type Stats struct {
	// Count is the number of data items in the tree.
	Count int
	// Nodes includes leaf and branch nodes.
	Nodes int
	// Leaves is the number of leaf nodes.
	Leaves int
	// Branches is the number of non-leaf nodes.
	Branches int
	// Height is the number of edges from the root to a leaf. A leaf-only
	// tree has height zero.
	Height int
}

// Levels returns Height+1 for a non-empty tree and zero for an empty tree.
func (s Stats) Levels() int {
	if s.Nodes == 0 {
		return 0
	}
	return s.Height + 1
}

type bulkSliceSorter[N numeric, T any] struct {
	mins [][2]N
	maxs [][2]N
	data []T
	axis int
}

func (s bulkSliceSorter[N, T]) Len() int { return len(s.data) }

func (s bulkSliceSorter[N, T]) Swap(i, j int) {
	s.mins[i], s.mins[j] = s.mins[j], s.mins[i]
	s.maxs[i], s.maxs[j] = s.maxs[j], s.maxs[i]
	s.data[i], s.data[j] = s.data[j], s.data[i]
}

func (s bulkSliceSorter[N, T]) Less(i, j int) bool {
	return bulkCoordLess(
		s.mins[i], s.maxs[i],
		s.mins[j], s.maxs[j],
		s.axis,
	)
}

type bulkItemSorter[N numeric, T any] struct {
	items []BulkItemGN[N, T]
	axis  int
}

func (s bulkItemSorter[N, T]) Len() int { return len(s.items) }

func (s bulkItemSorter[N, T]) Swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
}

func (s bulkItemSorter[N, T]) Less(i, j int) bool {
	a := s.items[i]
	b := s.items[j]
	return bulkCoordLess(a.Min, a.Max, b.Min, b.Max, s.axis)
}

func bulkCoordLess[N numeric](amin, amax, bmin, bmax [2]N, axis int) bool {
	other := 1 - axis
	if amin[axis] != bmin[axis] {
		return amin[axis] < bmin[axis]
	}
	if amax[axis] != bmax[axis] {
		return amax[axis] < bmax[axis]
	}
	if amin[other] != bmin[other] {
		return amin[other] < bmin[other]
	}
	if amax[other] != bmax[other] {
		return amax[other] < bmax[other]
	}
	return false
}

func bulkCeilDiv(a, b int) int {
	return (a + b - 1) / b
}

// bulkPackRanges emits contiguous, non-overlapping ranges covering total.
// Complete ranges have length capacity. A short final range is either kept
// when it is at least half full or balanced with the preceding range,
// preventing a bulk load from ending in a nearly empty node.
func bulkPackRanges(total, capacity int, emit func(start, end int)) {
	if total <= 0 {
		return
	}
	full := total / capacity
	rem := total % capacity
	if rem == 0 {
		for i := 0; i < full; i++ {
			start := i * capacity
			emit(start, start+capacity)
		}
		return
	}
	if full == 0 || rem >= (capacity+1)/2 {
		for i := 0; i < full; i++ {
			start := i * capacity
			emit(start, start+capacity)
		}
		start := full * capacity
		emit(start, total)
		return
	}
	for i := 0; i < full-1; i++ {
		start := i * capacity
		emit(start, start+capacity)
	}
	tailStart := (full - 1) * capacity
	tailLen := capacity + rem
	split := tailStart + tailLen/2
	emit(tailStart, split)
	emit(split, total)
}

func bulkSTRSlice[N numeric, T any](s bulkSliceSorter[N, T]) {
	n := s.Len()
	if n < 2 {
		return
	}
	s.axis = 0
	sort.Sort(s)

	leafGroups := bulkCeilDiv(n, maxEntries)
	slabs := int(math.Ceil(math.Sqrt(float64(leafGroups))))
	if slabs < 1 {
		slabs = 1
	}
	slabSize := bulkCeilDiv(n, slabs)
	for start := 0; start < n; start += slabSize {
		end := min(start+slabSize, n)
		sub := s
		sub.mins = s.mins[start:end]
		sub.maxs = s.maxs[start:end]
		sub.data = s.data[start:end]
		sub.axis = 1
		sort.Sort(sub)
	}
}

func bulkSTRItems[N numeric, T any](s bulkItemSorter[N, T]) {
	n := s.Len()
	if n < 2 {
		return
	}
	s.axis = 0
	sort.Sort(s)

	leafGroups := bulkCeilDiv(n, maxEntries)
	slabs := int(math.Ceil(math.Sqrt(float64(leafGroups))))
	if slabs < 1 {
		slabs = 1
	}
	slabSize := bulkCeilDiv(n, slabs)
	for start := 0; start < n; start += slabSize {
		end := min(start+slabSize, n)
		sub := s
		sub.items = s.items[start:end]
		sub.axis = 1
		sort.Sort(sub)
	}
}

// Bulk builds a packed sort-tile-recursive R-tree from coordinate slices.
//
// The three slices must have equal lengths. They are reordered in place
// during spatial sorting; their element-to-element association is preserved.
// No second N-sized rectangle or data slice is allocated for sorting.
//
// Bulk is intended for an empty tree. Calling it on a non-empty tree inserts
// the supplied entries with the ordinary copy-on-write path.
func (tr *RTreeGN[N, T]) Bulk(mins, maxs [][2]N, data []T) {
	if len(mins) != len(maxs) || len(mins) != len(data) {
		panic("rtree: Bulk requires mins, maxs, and data to have equal lengths")
	}
	if tr.root != nil {
		for i := range data {
			tr.Insert(mins[i], maxs[i], data[i])
		}
		return
	}
	n := len(data)
	if n == 0 {
		return
	}

	sorter := bulkSliceSorter[N, T]{
		mins: mins,
		maxs: maxs,
		data: data,
	}
	bulkSTRSlice(sorter)

	leafCount := bulkCeilDiv(n, maxEntries)
	leaves := make([]*node[N, T], 0, leafCount)
	bulkPackRanges(n, maxEntries, func(start, end int) {
		leaf := tr.newNode(true)
		leaf.count = int16(end - start)
		items := leaf.items()
		for i := start; i < end; i++ {
			leafIndex := i - start
			leaf.rects[leafIndex] = rect[N]{min: mins[i], max: maxs[i]}
			items[leafIndex] = data[i]
		}
		leaves = append(leaves, leaf)
	})
	tr.adoptBulkNodes(leaves, n)
}

// BulkLoad sorts items in place and builds a packed R-tree.
func (tr *RTreeGN[N, T]) BulkLoad(items []BulkItemGN[N, T]) {
	if tr.root != nil {
		for i := range items {
			tr.Insert(items[i].Min, items[i].Max, items[i].Data)
		}
		return
	}
	n := len(items)
	if n == 0 {
		return
	}

	bulkSTRItems(bulkItemSorter[N, T]{items: items})

	leafCount := bulkCeilDiv(n, maxEntries)
	leaves := make([]*node[N, T], 0, leafCount)
	bulkPackRanges(n, maxEntries, func(start, end int) {
		leaf := tr.newNode(true)
		leaf.count = int16(end - start)
		leafItems := leaf.items()
		for i := start; i < end; i++ {
			leafIndex := i - start
			item := items[i]
			leaf.rects[leafIndex] = rect[N]{min: item.Min, max: item.Max}
			leafItems[leafIndex] = item.Data
		}
		leaves = append(leaves, leaf)
	})
	tr.adoptBulkNodes(leaves, n)
}

// BulkInsert is an alias for BulkLoad.
func (tr *RTreeGN[N, T]) BulkInsert(items []BulkItemGN[N, T]) {
	tr.BulkLoad(items)
}

// Load is an alias for BulkLoad.
func (tr *RTreeGN[N, T]) Load(items []BulkItemGN[N, T]) {
	tr.BulkLoad(items)
}

func (tr *RTreeGN[N, T]) adoptBulkNodes(level []*node[N, T], count int) {
	for len(level) > 1 {
		parents := make([]*node[N, T], 0, bulkCeilDiv(len(level), maxEntries))
		bulkPackRanges(len(level), maxEntries, func(start, end int) {
			parent := tr.newNode(false)
			parent.count = int16(end - start)
			children := parent.children()
			for i := start; i < end; i++ {
				childIndex := i - start
				parent.rects[childIndex] = level[i].rect()
				children[childIndex] = level[i]
			}
			parents = append(parents, parent)
		})
		level = parents
	}

	tr.root = level[0]
	tr.count = count
	tr.rect = tr.root.rect()
	if tr.qpool == nil {
		tr.qpool = &sync.Pool{
			New: func() any { return &queue[N, T]{} },
		}
	}
}

// Stats returns node and height information for the tree.
func (tr *RTreeGN[N, T]) Stats() Stats {
	stats := Stats{Count: tr.count}
	if tr.root == nil {
		return stats
	}
	tr.root.collectStats(0, &stats)
	return stats
}

func (n *node[N, T]) collectStats(depth int, stats *Stats) {
	stats.Nodes++
	if n.leaf() {
		stats.Leaves++
		if depth > stats.Height {
			stats.Height = depth
		}
		return
	}
	stats.Branches++
	children := n.children()
	for i := 0; i < int(n.count); i++ {
		children[i].collectStats(depth+1, stats)
	}
}

// Height returns the number of root-to-leaf edges.
func (tr *RTreeGN[N, T]) Height() int {
	return tr.Stats().Height
}

// NodeCount returns the total number of leaf and branch nodes.
func (tr *RTreeGN[N, T]) NodeCount() int {
	return tr.Stats().Nodes
}

// Bulk builds a packed tree from float64 coordinate slices.
func (tr *RTreeG[T]) Bulk(mins, maxs [][2]float64, data []T) {
	tr.base.Bulk(mins, maxs, data)
}

// BulkLoad builds a packed tree from an in-place item slice.
func (tr *RTreeG[T]) BulkLoad(items []BulkItem[T]) {
	tr.base.BulkLoad(items)
}

// BulkInsert is an alias for BulkLoad.
func (tr *RTreeG[T]) BulkInsert(items []BulkItem[T]) {
	tr.base.BulkLoad(items)
}

// Load is an alias for BulkLoad.
func (tr *RTreeG[T]) Load(items []BulkItem[T]) {
	tr.base.BulkLoad(items)
}

// Stats returns the packed tree statistics.
func (tr *RTreeG[T]) Stats() Stats { return tr.base.Stats() }

// Height returns the number of root-to-leaf edges.
func (tr *RTreeG[T]) Height() int { return tr.base.Height() }

// NodeCount returns the total number of nodes.
func (tr *RTreeG[T]) NodeCount() int { return tr.base.NodeCount() }

// Bulk builds a packed tree from coordinate slices.
func (tr *RTree) Bulk(mins, maxs [][2]float64, data []any) {
	tr.base.Bulk(mins, maxs, data)
}

// BulkLoad builds a packed tree from an in-place item slice.
func (tr *RTree) BulkLoad(items []BulkItem[any]) {
	tr.base.BulkLoad(items)
}

// BulkInsert is an alias for BulkLoad.
func (tr *RTree) BulkInsert(items []BulkItem[any]) {
	tr.base.BulkLoad(items)
}

// Load is an alias for BulkLoad.
func (tr *RTree) Load(items []BulkItem[any]) {
	tr.base.BulkLoad(items)
}

// Stats returns the packed tree statistics.
func (tr *RTree) Stats() Stats { return tr.base.Stats() }

// Height returns the number of root-to-leaf edges.
func (tr *RTree) Height() int { return tr.base.Height() }

// NodeCount returns the total number of nodes.
func (tr *RTree) NodeCount() int { return tr.base.NodeCount() }
