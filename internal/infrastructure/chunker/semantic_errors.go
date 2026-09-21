package chunker

import "errors"

var (
	ErrOversizeAtomic = errors.New("oversize_atomic: semantic block cannot fit chunk budget")
	ErrSemanticPack   = errors.New("semantic packing failed")
)
