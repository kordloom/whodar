// Package graph reads an organization's people and reporting lines from
// Microsoft Graph, the live directory behind Entra ID. It is the org-chart
// source that stays current without re-exporting a CSV.
package graph

import "errors"

var (
	// ErrStatus marks a non-200 response from Graph.
	ErrStatus = errors.New("unexpected status")
	// ErrRateLimited marks a request throttled past the retry budget.
	ErrRateLimited = errors.New("rate limited")
)
