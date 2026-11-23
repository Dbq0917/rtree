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

Splits are distribution-based and local to the overflowing node. The node's
child centers are measured on both axes, and the axis with the greater spread
is selected. The node's entries are then sorted by their center on that axis
and split at the median, with half of the entries remaining in the original
node and half moving to the new sibling.

Only the entries of the node being split are reordered. No other node is
rebuilt or sorted, so insertion remains an in-place tree update while split
occupancy stays balanced regardless of insertion order.

## License

rtree source code is available under the MIT License.

