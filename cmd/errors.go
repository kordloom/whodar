package cmd

import "errors"

// ErrUnknownSource indicates an unsupported --source value.
var ErrUnknownSource = errors.New("unknown source")

// ErrBadArgs indicates missing or invalid command arguments.
var ErrBadArgs = errors.New("invalid arguments")

// ErrServe indicates the web server could not start, most often because
// something already holds the address.
var ErrServe = errors.New("cannot serve")

// ErrNoIndex indicates the index could not be loaded.
var ErrNoIndex = errors.New("no index")

// ErrNoIdentity indicates whodar could not tell who is asking, which personal
// recall needs before it can scope an answer.
var ErrNoIdentity = errors.New("unknown identity")

// ErrLicense indicates a feature the organization has not licensed. It is not
// a usage error: the command was written correctly and the subscription is
// what is missing.
var ErrLicense = errors.New("not licensed")

// ErrAuth indicates a source rejected the credentials or could not find what
// was named. It is distinct so the message can name the token to fix rather
// than surfacing a bare status code.
var ErrAuth = errors.New("source rejected the request")

// ErrShrunkSource indicates a source returned far less than it did last time,
// which is what a rate limit or a lost scope looks like from here.
var ErrShrunkSource = errors.New("source returned much less than before")

// ErrNoRecords indicates a run read nothing from any source. It stops a
// replacing index from writing an empty result over a good one, which is what
// an expired token would otherwise do silently.
var ErrNoRecords = errors.New("nothing was read from any source")

// ErrEval indicates a measurement could not be read, written, or compared.
var ErrEval = errors.New("cannot evaluate")

// ErrFeedback indicates the feedback bundle could not be written.
var ErrFeedback = errors.New("cannot write feedback")

// ErrPolicy indicates the organization policy forbids the requested action.
var ErrPolicy = errors.New("forbidden by policy")
