// Package analysis derives sample metadata: filename and path parsing, tag
// suggestions, loop versus one-shot classification, tempo and key detection,
// and merging by source priority.
//
// Analyze runs the whole pipeline on decoded mono audio and Merge turns its
// Result into the values to store. The package is pure computation: no I/O,
// no shared state, safe to call from any number of goroutines.
package analysis
