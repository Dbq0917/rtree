package rtree

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"testing"

	"github.com/tidwall/lotsa"
)

func bulkTestData(n int, seed int64) (mins, maxs [][2]float64, data []int) {
	rng := rand.New(rand.NewSource(seed))
	mins = make([][2]float64, n)
	maxs = make([][2]float64, n)
	data = make([]int, n)
	for i := 0; i < n; i++ {
		x := rng.Float64()*360 - 180
		y := rng.Float64()*180 - 90
		w := rng.Float64() * 0.1
		h := rng.Float64() * 0.1
		mins[i] = [2]float64{x, y}
		maxs[i] = [2]float64{x + w, y + h}
		data[i] = i
	}
	return mins, maxs, data
}

func TestBulkPackRanges(t *testing.T) {
	tests := []struct {
		total int
		want  [][2]int
	}{
		{0, nil},
		{1, [][2]int{{0, 1}}},
		{63, [][2]int{{0, 63}}},
		{64, [][2]int{{0, 64}}},
		{65, [][2]int{{0, 32}, {32, 65}}},
		{100, [][2]int{{0, 64}, {64, 100}}},
		{128, [][2]int{{0, 64}, {64, 128}}},
		{129, [][2]int{{0, 64}, {64, 96}, {96, 129}}},
	}
	for _, tc := range tests {
		var got [][2]int
		bulkPackRanges(tc.total, maxEntries, func(start, end int) {
			got = append(got, [2]int{start, end})
		})
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("total %d: expected %v, got %v", tc.total, tc.want, got)
		}
	}
}

func TestBulkLoadSane(t *testing.T) {
	sizes := []int{0, 1, 2, 31, 32, 33, 63, 64, 65, 96, 127, 128, 129, 500, 4095, 4096, 4097}
	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			mins, maxs, data := bulkTestData(n, int64(n)+1)
			expected := make(map[int]rect[float64], n)
			for i := range data {
				expected[data[i]] = rect[float64]{min: mins[i], max: maxs[i]}
			}

			var tr RTreeG[int]
			tr.Bulk(mins, maxs, data)

			if err := rSane(&tr); err != nil {
				t.Fatal(err)
			}
			stats := tr.Stats()
			if stats.Count != n || tr.Len() != n {
				t.Fatalf("bad count: stats=%d len=%d n=%d", stats.Count, tr.Len(), n)
			}
			if n > 0 && stats.Leaves != bulkCeilDiv(n, maxEntries) {
				t.Fatalf("expected %d leaves, got %d", bulkCeilDiv(n, maxEntries), stats.Leaves)
			}

			seen := make(map[int]rect[float64], n)
			tr.Scan(func(min, max [2]float64, value int) bool {
				seen[value] = rect[float64]{min: min, max: max}
				return true
			})
			if len(seen) != n {
				t.Fatalf("expected %d scanned items, got %d", n, len(seen))
			}
			for value, wantRect := range expected {
				gotRect, ok := seen[value]
				if !ok {
					t.Fatalf("missing value %d", value)
				}
				if !wantRect.equals(&gotRect) {
					t.Fatalf("value %d: expected %v, got %v", value, wantRect, gotRect)
				}
			}

			for value, wantRect := range expected {
				found := false
				tr.Search(wantRect.min, wantRect.max,
					func(min, max [2]float64, gotValue int) bool {
						if gotValue == value {
							found = true
							return false
						}
						return true
					})
				if !found {
					t.Fatalf("value %d was not returned by Search", value)
				}
			}
		})
	}
}

func TestBulkLoadItemsAndGenericNumeric(t *testing.T) {
	items := make([]BulkItemGN[int32, string], 180)
	for i := range items {
		x := int32(i * 2)
		items[i] = BulkItemGN[int32, string]{
			Min:  [2]int32{x, int32(i % 7)},
			Max:  [2]int32{x + 1, int32(i%7) + 1},
			Data: fmt.Sprintf("item-%d", i),
		}
	}
	var tr RTreeGN[int32, string]
	tr.BulkInsert(items)
	if tr.Len() != len(items) {
		t.Fatalf("expected %d, got %d", len(items), tr.Len())
	}

	found := 0
	tr.Search([2]int32{0, 0}, [2]int32{1000, 1000},
		func(_, _ [2]int32, value string) bool {
			found++
			return true
		})
	if found != len(items) {
		t.Fatalf("expected %d search results, got %d", len(items), found)
	}

	var plain RTree
	plainMins := [][2]float64{{0, 0}, {1, 1}}
	plainMaxs := [][2]float64{{0, 0}, {1, 1}}
	plainData := []any{"zero", "one"}
	plain.Bulk(plainMins, plainMaxs, plainData)
	if plain.Len() != 2 || plain.NodeCount() != 1 || plain.Height() != 0 {
		t.Fatalf("bad RTree bulk shape: len=%d stats=%+v", plain.Len(), plain.Stats())
	}
}

func TestBulkLoadCopyWrite(t *testing.T) {
	const n = 1000
	mins, maxs, data := bulkTestData(n, 424242)
	var tr RTreeG[int]
	items := make([]BulkItem[int], n)
	for i := range data {
		items[i] = BulkItem[int]{Min: mins[i], Max: maxs[i], Data: data[i]}
	}
	tr.BulkLoad(items)
	if err := rSane(&tr); err != nil {
		t.Fatal(err)
	}
	before := tr.Stats()

	cloned := tr.Copy()
	for i := 0; i < 300; i++ {
		x := rand.Float64()*360 - 180
		y := rand.Float64()*180 - 90
		cloned.Insert([2]float64{x, y}, [2]float64{x, y}, n+i)
	}
	if err := rSane(tr.Copy()); err != nil {
		t.Fatalf("original after clone writes: %v", err)
	}
	if err := rSane(cloned); err != nil {
		t.Fatalf("clone after writes: %v", err)
	}
	if tr.Len() != n {
		t.Fatalf("original changed: len=%d", tr.Len())
	}
	if cloned.Len() != n+300 {
		t.Fatalf("clone len=%d", cloned.Len())
	}
	if got := tr.Stats(); got.Nodes != before.Nodes || got.Height != before.Height {
		t.Fatalf("original shape changed: before=%+v after=%+v", before, got)
	}

	extraMin := [][2]float64{{1, 1}, {2, 2}}
	extraMax := [][2]float64{{1, 1}, {2, 2}}
	extraData := []int{n + 300, n + 301}
	cloned.Bulk(extraMin, extraMax, extraData)
	if err := rSane(cloned); err != nil {
		t.Fatalf("clone after follow-up Bulk: %v", err)
	}
	if cloned.Len() != n+302 {
		t.Fatalf("clone after Bulk len=%d", cloned.Len())
	}
	if tr.Len() != n {
		t.Fatalf("original changed after follow-up Bulk: len=%d", tr.Len())
	}
}

func benchmarkBulkSearchRange(t *testing.T, tr *RTreeG[int], p float64) {
	t.Helper()
	rng := rand.New(rand.NewSource(99173))
	label := fmt.Sprintf("search-%g%%:", p*100)
	fmt.Printf("%-18s", label)
	lotsa.Ops(10_000, 1, func(_, _ int) {
		min := [2]float64{rng.Float64()*360 - 180, rng.Float64()*180 - 90}
		max := [2]float64{min[0] + 360*p, min[1] + 180*p}
		tr.Search(min, max, func(_, _ [2]float64, _ int) bool { return true })
	})
}

func TestBulkLoadBenchFloat64(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping million-rectangle bulk benchmark in short mode")
	}
	const n = 1_000_000
	mins := make([][2]float64, n)
	maxs := make([][2]float64, n)
	data := make([]int, n)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		r := randRectWith(rng, 'm')
		mins[i], maxs[i], data[i] = r.min, r.max, i
	}

	lotsa.Output = os.Stdout
	lotsa.MemUsage = true

	var sequential RTreeG[int]
	fmt.Printf("\n== bulk load comparison [float64, int] ==\n")
	fmt.Printf("insert:           ")
	lotsa.Ops(n, 1, func(i, _ int) {
		sequential.Insert(mins[i], maxs[i], data[i])
	})
	seqStats := sequential.Stats()
	fmt.Printf("sequential shape: height=%d levels=%d nodes=%d leaves=%d\n",
		seqStats.Height, seqStats.Levels(), seqStats.Nodes, seqStats.Leaves)

	var seqCount int
	fmt.Printf("seq-search-item:  ")
	lotsa.Ops(n, 1, func(i, _ int) {
		want := data[i]
		sequential.Search(mins[i], maxs[i], func(_, _ [2]float64, value int) bool {
			if value == want {
				seqCount++
				return false
			}
			return true
		})
	})
	if seqCount != n {
		t.Fatalf("sequential search found %d of %d", seqCount, n)
	}
	benchmarkBulkSearchRange(t, &sequential, 0.01)
	benchmarkBulkSearchRange(t, &sequential, 0.05)

	sequential = RTreeG[int]{}
	runtime.GC()

	var bulk RTreeG[int]
	fmt.Printf("bulk:             ")
	lotsa.Ops(1, 1, func(_, _ int) {
		bulk.Bulk(mins, maxs, data)
	})
	bulkStats := bulk.Stats()
	leafFill := 100 * float64(n) / (float64(bulkStats.Leaves) * maxEntries)
	fmt.Printf("bulk shape:       height=%d levels=%d nodes=%d leaves=%d leaf-fill=%.2f%%\n",
		bulkStats.Height, bulkStats.Levels(), bulkStats.Nodes, bulkStats.Leaves, leafFill)
	if bulkStats.Height > seqStats.Height {
		t.Fatalf("bulk height %d exceeds sequential height %d", bulkStats.Height, seqStats.Height)
	}
	if bulkStats.Nodes > seqStats.Nodes {
		t.Fatalf("bulk nodes %d exceed sequential nodes %d", bulkStats.Nodes, seqStats.Nodes)
	}

	var bulkCount int
	fmt.Printf("bulk-search-item: ")
	lotsa.Ops(n, 1, func(i, _ int) {
		want := data[i]
		bulk.Search(mins[i], maxs[i], func(_, _ [2]float64, value int) bool {
			if value == want {
				bulkCount++
				return false
			}
			return true
		})
	})
	if bulkCount != n {
		t.Fatalf("bulk search found %d of %d", bulkCount, n)
	}
	benchmarkBulkSearchRange(t, &bulk, 0.01)
	benchmarkBulkSearchRange(t, &bulk, 0.05)
}

func randRectWith(rng *rand.Rand, kind byte) rect[float64] {
	r := rect[float64]{
		min: [2]float64{rng.Float64()*360 - 180, rng.Float64()*180 - 90},
	}
	r.max = r.min
	if kind == 'r' || (kind == 'm' && rng.Int()%2 == 0) {
		size := 0.01
		r.max[0] = r.min[0] + rng.Float64()*size
		r.max[1] = r.min[1] + rng.Float64()*size
	}
	return r
}
