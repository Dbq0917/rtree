# rtree

[![GoDoc](https://godoc.org/github.com/tidwall/rtree?status.svg)](https://godoc.org/github.com/tidwall/rtree)

This package provides an in-memory R-Tree implementation for Go. It's designed
for [Tile38](https://github.com/tidwall/tile38) and is optimized for fast rect 
inserts and replacements.

<img src="cities.png" width="512" border="0" alt="Cities">

## Usage

### Installing

To start using rtree, install Go and run `go get`:

```sh
$ go get -u github.com/tidwall/rtree
```

### Basic operations

```go
// create a 2D RTree
var tr rtree.RTree

// insert a point
tr.Insert([2]float64{-112.0078, 33.4373}, [2]float64{-112.0078, 33.4373}, "PHX")

// insert a rect
tr.Insert([2]float64{10, 10}, [2]float64{20, 20}, "rect")

// search 
tr.Search([2]float64{-112.1, 33.4}, [2]float64{-112.0, 33.5}, 
 	func(min, max [2]float64, data any) bool {
		println(data.(string)) // prints "PHX"
	},
)

// delete 
tr.Delete([2]float64{-112.0078, 33.4373}, [2]float64{-112.0078, 33.4373}, "PHX")
```

### Support for Generics (Go 1.18+)

```go
// create a 2D RTree
var tr rtree.RTreeG[string]

// insert a point
tr.Insert([2]float64{-112.0078, 33.4373}, [2]float64{-112.0078, 33.4373}, "PHX")

// insert a rect
tr.Insert([2]float64{10, 10}, [2]float64{20, 20}, "rect")

// search 
tr.Search([2]float64{-112.1, 33.4}, [2]float64{-112.0, 33.5}, 
 	func(min, max [2]float64, data string) bool {
		println(data) // prints "PHX"
	},
)

// delete 
tr.Delete([2]float64{-112.0078, 33.4373}, [2]float64{-112.0078, 33.4373}, "PHX")
```


### Support for generic numeric types, like int, float32, etc.

```go
// create a 2D RTree
var tr rtree.RTreeGN[float32, string]

// insert a point
tr.Insert([2]float32{-112.0078, 33.4373}, [2]float32{-112.0078, 33.4373}, "PHX")

// insert a rect
tr.Insert([2]float32{10, 10}, [2]float32{20, 20}, "rect")

// search 
tr.Search([2]float32{-112.1, 33.4}, [2]float32{-112.0, 33.5}, 
 	func(min, max [2]float32, data string) bool {
		println(data) // prints "PHX"
	},
)

// delete 
tr.Delete([2]float32{-112.0078, 33.4373}, [2]float32{-112.0078, 33.4373}, "PHX")
```


## Algorithms

This implementation is a variant of the original paper:  
[R-TREES. A DYNAMIC INDEX STRUCTURE FOR SPATIAL SEARCHING](https://www.cs.princeton.edu/courses/archive/fall08/cos597B/papers/rtrees.pdf)

### Inserting

Similar to the original paper. From the root to the leaf, the rects which will
incur the least enlargment are chosen. Ties go to rects with the smallest area. 

Added to this implementation: Before the least enlargement process, each
rect is looked at, and for rects that do not incur any enlargement at all, the
one with the smallest area is choosen. Also added is all child rectangles in
every node are ordered by their minimum x value. This can dramatically speed up
searching for intersecting rectangles on most modern hardware.

### Deleting

A target rect is searched for from root to the leaf, and if found it's deleted. When there are no more child rects in a node, that node is immedately removed from the tree.

### Searching

Same as the original algorithm.

### Splitting

The split is data aware. The legacy single-axis split always cuts the
overflowing node's largest axis at its midpoint, which is cheap but produces
two badly unbalanced halves when the data is clustered on one side of that
axis. The dense half then overflows and splits again, so one branch keeps
growing deeper and point queries pay for the long path.

The current algorithm instead builds one count-balanced candidate per axis by
partitioning the child centers at their median, and picks the candidate whose
two resulting bounding rects have the least overlap, then the most similar
areas, and finally the most even entry counts. Ties prefer the largest axis,
preserving the old behavior on uniform data.

To keep this cheap, the evaluation only touches on-stack scalars: child
centers are collected in fixed-size buffers, each axis' median is found with
an allocation-free median-of-three quickselect over at most `maxEntries`
elements, and the candidate bounding rects are expanded in a second linear
pass. No child data is moved while comparing; only the winning candidate is
physically applied, so each split still allocates exactly one new node and the
copy-on-write copy count is unchanged. When no candidate can keep both sides
above the minimum entry count, the legacy largest-axis split is used as a
fallback.

## License

rtree source code is available under the MIT License.

