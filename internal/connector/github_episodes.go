package connector

import (
	"strconv"
	"strings"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/github"
	"github.com/kordloom/whodar/internal/model"
)

// Episodes returns the merged changes seen by the most recent Fetch, newest
// first. It is empty unless GitHubOptions.Episodes was set.
func (g *GitHub) Episodes() []episode.Episode { return g.episodes }

// changeEpisode records a merged pull request: a change that actually landed,
// who wrote and reviewed it, and a link to the discussion. Unmerged work is
// skipped, since it is a proposal rather than a record of how something was
// done.
func changeEpisode(owner, repo string, pr github.PullRequest) (episode.Episode, bool) {
	if !pr.Merged() {
		return episode.Episode{}, false
	}
	people := pr.People()
	participants := make([]model.ID, 0, len(people))
	for _, login := range people {
		if login == "" {
			continue
		}
		participants = append(participants, model.ID("github:"+strings.ToLower(login)))
	}
	if len(participants) == 0 {
		return episode.Episode{}, false
	}
	return episode.Episode{
		ID:           "github:" + owner + "/" + repo + ":" + strconv.Itoa(pr.Number),
		Source:       "github",
		Kind:         episode.KindChange,
		Place:        owner + "/" + repo,
		Title:        pr.Title,
		Participants: participants,
		Occurred:     pr.MergedAt,
		Permalink:    pr.HTMLURL,
		Body:         pr.Title + " " + strings.Join(pr.LabelNames(), " "),
	}, true
}
