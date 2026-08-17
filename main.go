// Command whodar locates the right person or channel for a question.
//
// It indexes people, teams, and topics from work sources, then answers
// "who do I talk to about X" in plain language. Two engines back it: a non-LLM
// keyword ranker and an optional local LLM. Indexed data stays on the machine
// unless an explicit egress policy permits otherwise.
package main

import (
	"fmt"
	"os"

	"github.com/kordloom/whodar/cmd"
)

// main runs the whodar CLI and maps any error to a non-zero exit code.
func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "whodar:", err)
		os.Exit(1)
	}
}
