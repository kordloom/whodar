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

// ErrLicense indicates a feature the organization has not licensed. It is not
// a usage error: the command was written correctly and the subscription is
// what is missing.
var ErrLicense = errors.New("not licensed")

// ErrNoRecords indicates a run read nothing from any source. It stops a
// replacing index from writing an empty result over a good one, which is what
// an expired token would otherwise do silently.
var ErrNoRecords = errors.New("nothing was read from any source")
