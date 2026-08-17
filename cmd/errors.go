package cmd

import "errors"

// ErrUnknownSource indicates an unsupported --source value.
var ErrUnknownSource = errors.New("unknown source")

// ErrBadArgs indicates missing or invalid command arguments.
var ErrBadArgs = errors.New("invalid arguments")

// ErrNoIndex indicates the index could not be loaded.
var ErrNoIndex = errors.New("no index")

// ErrNoIdentity indicates whodar could not tell who is asking, which personal
// recall needs before it can scope an answer.
var ErrNoIdentity = errors.New("unknown identity")
