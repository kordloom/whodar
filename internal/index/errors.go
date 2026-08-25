package index

import "errors"

// ErrDamaged indicates the index file was read but does not parse. The everyday
// cause is a run that was interrupted while writing, or a file copied in part.
var ErrDamaged = errors.New("index file is damaged")
