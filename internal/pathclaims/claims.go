// Package pathclaims contains pure conflict rules for canonical slash paths.
// Callers own namespace selection, normalization, locking and lease lifetimes.
package pathclaims

import "strings"

// Contains includes the parent itself. Root is represented by "/".
func Contains(parent, child string) bool {
	return parent == child || parent == "/" || strings.HasPrefix(child, parent+"/")
}

// Conflict reports overlapping claims with at least one writer.
func Conflict(a string, aWrite, aTree bool, b string, bWrite, bTree bool) bool {
	return (aWrite || bWrite) && (a == b || aTree && Contains(a, b) || bTree && Contains(b, a))
}
