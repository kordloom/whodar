package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/feedback"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/recall"
	"github.com/kordloom/whodar/internal/resolve"
	"github.com/kordloom/whodar/internal/text"
)

// rank formats a one-based position in a result list, right-aligned so single
// and double digits line up.
func rank(n int) string { return fmt.Sprintf("%2d", n) }

// nameWidth returns the column width to pad names to, capped so one long name
// does not push every row far to the right.
func nameWidth(people []resolve.JSONPerson) int {
	width := 0
	for _, p := range people {
		if n := len([]rune(p.Name)); n > width {
			width = n
		}
	}
	if width > 26 {
		width = 26
	}
	return width
}

// renderAsk prints an answer as ranked, aligned rows: each match numbered, with
// a confidence bar and the reasons it surfaced dimmed beneath.
func renderAsk(w io.Writer, query string, view resolve.JSONAnswer, s style) {
	fmt.Fprintf(w, "\n%s\n", s.bold("Who knows about "+strconv.Quote(query)))
	if view.Summary != "" {
		fmt.Fprintf(w, "%s\n", s.dim(view.Summary))
	}
	if len(view.People) == 0 && len(view.Channels) == 0 {
		fmt.Fprintf(w, "\n  %s\n\n", s.dim(
			"Nothing matched. Try the words your team would use, or `whodar directory` to see what is indexed."))
		return
	}
	if len(view.People) > 0 {
		fmt.Fprintf(w, "\n  %s\n\n", s.dim("PEOPLE"))
		width := nameWidth(view.People)
		for i, p := range view.People {
			fmt.Fprintf(w, "  %s  %s  %s\n",
				s.dim(rank(i+1)), pad(s.bold(p.Name), p.Name, width), s.dim(joinRole(p.Title, p.Team)))
			fmt.Fprintf(w, "      %s %s   %s\n",
				s.confBar(p.Confidence), s.accent(pct(p.Confidence)), s.dim(strings.Join(p.Reasons, " · ")))
			fmt.Fprintln(w)
		}
	}
	if len(view.Channels) > 0 {
		fmt.Fprintf(w, "  %s\n\n", s.dim("CHANNELS"))
		for i, ch := range view.Channels {
			fmt.Fprintf(w, "  %s  %s\n", s.dim(rank(i+1)), s.bold("#"+ch.Name))
			fmt.Fprintf(w, "      %s %s   %s\n",
				s.confBar(ch.Confidence), s.accent(pct(ch.Confidence)), s.dim(strings.Join(ch.Reasons, " · ")))
			if ch.URL != "" {
				fmt.Fprintf(w, "      %s %s\n", s.accent("\u2192"), s.dim(ch.URL))
			}
			fmt.Fprintln(w)
		}
	}
}

// renderNear prints who works near a person as a ranked list, each with the
// shared teams and topics that put them close.
func renderNear(w io.Writer, name string, near []index.Adjacent, s style) {
	fmt.Fprintf(w, "\n%s\n", s.bold("Near "+name))
	if len(near) == 0 {
		fmt.Fprintf(w, "\n  %s\n\n", s.dim("No one indexed shares a team or topic with them yet."))
		return
	}
	fmt.Fprintln(w)
	width := 0
	for _, a := range near {
		if n := len([]rune(a.Name)); n > width {
			width = n
		}
	}
	if width > 26 {
		width = 26
	}
	for i, a := range near {
		fmt.Fprintf(w, "  %s  %s  %s\n",
			s.dim(rank(i+1)), pad(s.bold(a.Name), a.Name, width), s.dim(strings.Join(a.Reasons, " · ")))
	}
	fmt.Fprintln(w)
}

// renderStatus prints the index summary as a clean labeled block, with a
// friendly build time and the index path shortened to the home directory.
func renderStatus(w io.Writer, v map[string]any, s style) {
	geti := func(k string) int { n, _ := v[k].(int); return n }
	gets := func(k string) string { str, _ := v[k].(string); return str }
	line := func(label, val string) { fmt.Fprintf(w, "  %s  %s\n", pad(s.dim(label), label, 8), val) }

	fmt.Fprintf(w, "\n%s\n\n", s.bold("whodar index"))
	line("People", s.bold(fmt.Sprintf("%d", geti("people")))+"  "+
		s.dim(fmt.Sprintf("%d teams · %d topics · %d channels", geti("teams"), geti("topics"), geti("channels"))))
	if bt := gets("built_at"); bt != "" {
		when := bt
		if t, err := time.Parse(time.RFC3339, bt); err == nil {
			when = t.Format("Jan 2, 3:04 PM")
		}
		if age := gets("age"); age != "" {
			when += "  (" + humanAge(age) + " ago)"
		}
		line("Built", s.dim(when))
	} else if age := gets("age"); age != "" {
		line("Built", s.dim(humanAge(age)+" ago"))
	}
	if src, ok := v["sources"].(map[string]int); ok && len(src) > 0 {
		parts := make([]string, 0, len(src))
		for name, n := range src {
			parts = append(parts, fmt.Sprintf("%s (%d)", name, n))
		}
		sort.Strings(parts)
		line("Sources", strings.Join(parts, ", "))
	}
	if lic := gets("license"); lic != "" {
		line("License", s.dim(strings.TrimPrefix(lic, "No license configured: ")))
	}
	enc, emb := "off", "off"
	if e, _ := v["encryption_key_configured"].(bool); e {
		enc = "on"
	}
	if e, _ := v["embeddings"].(bool); e {
		emb = "on"
	}
	line("At rest", s.dim("encryption "+enc+" · embeddings "+emb))
	if idx := gets("index"); idx != "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(idx, home) {
			idx = "~" + idx[len(home):]
		}
		line("File", s.dim(idx))
	}
	fmt.Fprintln(w)
}

// humanAge trims a Go duration like "15m0s" to a compact "15m", or "just now"
// when nothing measurable has passed.
func humanAge(d string) string {
	d = strings.TrimSuffix(d, "0s")
	if d == "" {
		return "just now"
	}
	return d
}

// renderRecall prints the conversations a person took part in, each card led by
// its source in the source's own color, then where it happened, who was there,
// any retained solution, and a link back.
func renderRecall(w io.Writer, ans recall.Answer, s style) {
	fmt.Fprintf(w, "\n%s\n", s.bold("Your recall"))
	if ans.Query != "" {
		fmt.Fprintf(w, "%s\n", s.dim("matching "+strconv.Quote(ans.Query)))
	}
	if len(ans.Episodes) == 0 {
		fmt.Fprintf(w, "\n  %s\n", s.dim("Nothing found in the conversations you took part in."))
		if ans.Scope.Note != "" {
			fmt.Fprintf(w, "  %s\n", s.dim(ans.Scope.Note))
		}
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintln(w)
	for _, ep := range ans.Episodes {
		label, code := sourceColor(ep.Source)
		date := ""
		if !ep.When.IsZero() {
			date = ep.When.Format("Jan 2, 2006")
		}
		fmt.Fprintf(w, "  %s   %s\n", s.color256(code, "● "+strings.ToUpper(label)), s.dim(date))
		place := ep.Place
		if place != "" && (ep.Kind == "thread" || ep.Kind == "window") {
			place = "#" + place
		}
		if place != "" {
			fmt.Fprintf(w, "  %s\n", s.bold(place))
		}
		fmt.Fprintf(w, "  %s\n", s.dim(recallWith(ep.People)))
		// Every other answer whodar gives says why it surfaced, and a recalled
		// conversation needs it most: without the matched words, a thread from
		// an unrelated-looking channel reads as a random result rather than the
		// one place the asked-about word was actually said.
		if words := recallMatchedWords(ans.Query, ep.Matched); len(words) > 0 {
			fmt.Fprintf(w, "  %s\n", s.dim("matched "+strings.Join(words, ", ")))
		}
		if ep.Solution != nil {
			if ep.Solution.Summary != "" {
				fmt.Fprintf(w, "  %s\n", ep.Solution.Summary)
			}
			for _, n := range ep.Solution.Notes {
				fmt.Fprintf(w, "    %s %s\n", s.dim(n.Author+":"), n.Text)
			}
		}
		if ep.Permalink != "" {
			fmt.Fprintf(w, "  %s %s\n", s.color256(code, "→ open in "+label), s.dim(ep.Permalink))
		}
		if ep.LinkMayHaveExpired {
			fmt.Fprintf(w, "  %s\n", s.dim("link may be stale"))
		}
		fmt.Fprintln(w)
	}
	if ans.Scope.Note != "" {
		fmt.Fprintf(w, "  %s\n\n", s.dim(ans.Scope.Note))
	}
}

// recallMatchedWords turns the matched search keys back into the words the
// person typed. Matching runs on stems, so the raw keys read as misspellings:
// asking about "vacation" and being told the match was "vacat" looks like a
// bug rather than an explanation. A key with no word behind it is shown as it
// is rather than dropped, since a partial explanation beats none.
func recallMatchedWords(query string, matched []string) []string {
	if len(matched) == 0 {
		return nil
	}
	byStem := make(map[string]string)
	for _, word := range strings.Fields(query) {
		word = strings.Trim(strings.ToLower(word), `"'.,?!`)
		if word == "" {
			continue
		}
		if stem := text.Stem(word); stem != "" {
			if _, seen := byStem[stem]; !seen {
				byStem[stem] = word
			}
		}
	}
	out := make([]string, 0, len(matched))
	seen := make(map[string]bool, len(matched))
	for _, key := range matched {
		word := byStem[key]
		if word == "" {
			word = key
		}
		if !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	return out
}

// recallWith names who else was in a conversation.
func recallWith(people []recall.Person) string {
	names := make([]string, 0, len(people))
	for _, p := range people {
		switch {
		case p.Name != "":
			names = append(names, p.Name)
		case p.Email != "":
			names = append(names, p.Email)
		}
	}
	switch {
	case len(names) == 0:
		return "on your own"
	case len(names) == 1:
		return "with " + names[0]
	case len(names) <= recallNamesShown+1:
		return "with " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	default:
		// A busy channel puts everyone who spoke in the room, and printing all
		// of them buries the conversation under a wall of names. The first few
		// are who a person would recognize; the rest is a count.
		return fmt.Sprintf("with %s and %d others",
			strings.Join(names[:recallNamesShown], ", "), len(names)-recallNamesShown)
	}
}

// recallNamesShown is how many people a recalled conversation names before it
// counts the rest.
const recallNamesShown = 3

// renderDirectory prints the requested directory section, or every section when
// none was named, each as a titled, counted list.
func renderDirectory(w io.Writer, dir resolve.Directory, section string, s style) {
	show := func(name string) bool { return section == "" || section == name }
	any := false
	head := func(title string, n int) {
		any = true
		fmt.Fprintf(w, "\n%s  %s\n\n", s.bold(title), s.dim(strconv.Itoa(n)))
	}
	if show("people") && len(dir.People) > 0 {
		head("People", len(dir.People))
		width := 0
		for _, p := range dir.People {
			if n := len([]rune(p.Name)); n > width {
				width = n
			}
		}
		if width > 26 {
			width = 26
		}
		for _, p := range dir.People {
			fmt.Fprintf(w, "  %s  %s\n", pad(s.bold(p.Name), p.Name, width), s.dim(joinRole(p.Title, p.Team)))
		}
	}
	if show("channels") && len(dir.Channels) > 0 {
		head("Channels", len(dir.Channels))
		for _, ch := range dir.Channels {
			meta := peopleCount(ch.Members)
			if ch.Topic != "" {
				meta = ch.Topic + " · " + meta
			}
			fmt.Fprintf(w, "  %s  %s\n", s.bold("#"+ch.Name), s.dim(meta))
		}
	}
	if show("teams") && len(dir.Teams) > 0 {
		head("Teams", len(dir.Teams))
		width := teamWidth(dir.Teams)
		for _, t := range dir.Teams {
			fmt.Fprintf(w, "  %s  %s\n", pad(s.bold(t.Name), t.Name, width), s.dim(peopleCount(t.People)))
		}
	}
	if show("topics") && len(dir.Topics) > 0 {
		head("Topics", len(dir.Topics))
		width := topicWidth(dir.Topics)
		for _, t := range dir.Topics {
			fmt.Fprintf(w, "  %s  %s\n", pad(s.bold(t.Name), t.Name, width), s.dim(peopleCount(t.People)))
		}
	}
	if !any {
		fmt.Fprintf(w, "\n  %s\n", s.dim("Nothing indexed yet. Run `whodar index` against a source."))
	}
	fmt.Fprintln(w)
}

// teamWidth returns the padding width for a list of team names.
func teamWidth(teams []resolve.DirectoryTeam) int {
	w := 0
	for _, t := range teams {
		if n := len([]rune(t.Name)); n > w {
			w = n
		}
	}
	if w > 26 {
		w = 26
	}
	return w
}

// topicWidth returns the padding width for a list of topic names.
func topicWidth(topics []resolve.DirectoryTopic) int {
	w := 0
	for _, t := range topics {
		if n := len([]rune(t.Name)); n > w {
			w = n
		}
	}
	if w > 26 {
		w = 26
	}
	return w
}

// peopleCount labels a count of people, singular or plural.
func peopleCount(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

// renderLicense prints the licensed tier and its terms as a labeled block.
func renderLicense(w io.Writer, tier, org, id, expires, reason string, s style) {
	line := func(label, val string) { fmt.Fprintf(w, "  %s  %s\n", pad(s.dim(label), label, 8), val) }
	fmt.Fprintf(w, "\n%s\n\n", s.bold("whodar license"))
	line("Tier", s.bold(tier))
	if org != "" {
		line("Org", org)
	}
	if id != "" {
		line("ID", s.dim(id))
	}
	if expires != "" {
		line("Expires", s.dim(expires))
	}
	if reason != "" {
		line("Note", s.dim(reason))
	}
	fmt.Fprintln(w)
}

// renderFeedback prints recorded votes, each marked helpful or wrong, with the
// question it was cast on and any note.
func renderFeedback(w io.Writer, entries []feedback.Entry, s style) {
	fmt.Fprintf(w, "\n%s  %s\n", s.bold("Feedback"), s.dim(strconv.Itoa(len(entries))))
	if len(entries) == 0 {
		fmt.Fprintf(w, "\n  %s\n\n", s.dim("No votes recorded yet."))
		return
	}
	fmt.Fprintln(w)
	for _, e := range entries {
		mark := s.accent("✓ helpful")
		if e.Vote < 0 {
			mark = s.bad("✗ wrong")
		}
		target := e.Person
		if e.Channel != "" {
			target = "#" + e.Channel
		}
		fmt.Fprintf(w, "  %s  %s  %s\n", mark, s.bold(target), s.dim("on "+strconv.Quote(e.Query)))
		if e.Comment != "" {
			fmt.Fprintf(w, "      %s\n", s.dim("“"+e.Comment+"”"))
		}
	}
	fmt.Fprintln(w)
}

// renderIdentity prints the inferred identity merges grouped by person, each
// alias with a confidence bar, the confidence, and the evidence.
func renderIdentity(w io.Writer, v identityView, s style) {
	if v.Merges == 0 {
		fmt.Fprintln(w, s.dim(
			"No inferred identity merges. Joins by email or provider id are certain and are not shown here."))
		return
	}
	fmt.Fprintln(w, s.bold(fmt.Sprintf("Identity joins  %d inferred across %d people", v.Merges, len(v.People))))
	width := aliasWidth(v.People)
	for _, p := range v.People {
		who := s.bold(p.Name)
		if p.Name == "" {
			who = s.bold(p.ID)
		}
		if p.Email != "" {
			who += "  " + s.dim(p.Email)
		}
		fmt.Fprintf(w, "\n  %s\n", who)
		for _, j := range p.Joins {
			_, code := sourceColor(handleSource(j.Alias))
			alias := pad(s.color256(code, j.Alias), j.Alias, width)
			fmt.Fprintf(w, "    %s %s  %s  %s\n",
				s.confBar(j.Confidence), pct(j.Confidence), alias, s.dim(j.Reason))
		}
	}
}

// aliasWidth is the longest alias across all joins, for column alignment.
func aliasWidth(people []identityPerson) int {
	w := 0
	for _, p := range people {
		for _, j := range p.Joins {
			if n := len([]rune(j.Alias)); n > w {
				w = n
			}
		}
	}
	return w
}

// handleSource returns the source prefix of an id like "github:kim-doe", or the
// empty string when there is no prefix.
func handleSource(id string) string {
	if i := strings.IndexByte(id, ':'); i > 0 {
		return id[:i]
	}
	return ""
}

// renderSearch prints ranked search results: people in bold, channels prefixed
// with a hash, each with a context line and the fields the query matched.
func renderSearch(w io.Writer, query string, results []resolve.SearchResult, s style) {
	if len(results) == 0 {
		fmt.Fprintln(w, s.dim(
			"No people or channels match “"+query+"”. Try `whodar directory` to see what is indexed."))
		return
	}
	fmt.Fprintln(w, s.bold(fmt.Sprintf("%d result%s for “%s”", len(results), plural(len(results)), query)))
	width := searchNameWidth(results)
	for i, r := range results {
		raw, label := r.Name, s.bold(r.Name)
		if r.Kind == "channel" {
			raw = "#" + r.Name
			label = s.color256(197, raw)
		}
		fmt.Fprintf(w, "  %s  %s  %s", s.dim(rank(i+1)), pad(label, raw, width), s.dim(searchContext(r)))
		if len(r.Matched) > 0 {
			fmt.Fprintf(w, "  %s", s.dim("· "+strings.Join(r.Matched, ", ")))
		}
		fmt.Fprintln(w)
	}
}

// searchContext is the muted line after a result's name: a person's role or
// email, or a channel's topic.
func searchContext(r resolve.SearchResult) string {
	if r.Kind == "channel" {
		if r.Team != "" {
			return "channel · " + r.Team
		}
		return "channel"
	}
	if role := joinRole(r.Title, r.Team); role != "" {
		return role
	}
	return r.Email
}

// searchNameWidth is the column width to pad result names to, capped so one long
// name does not push every row far right.
func searchNameWidth(results []resolve.SearchResult) int {
	width := 0
	for _, r := range results {
		n := len([]rune(r.Name))
		if r.Kind == "channel" {
			n++
		}
		if n > width {
			width = n
		}
	}
	if width > 32 {
		width = 32
	}
	return width
}

// plural returns "s" unless n is one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// renderRisk prints the knowledge-concentration report, most at-risk first, with
// each topic's bus factor and the people who hold it.
func renderRisk(w io.Writer, topics []resolve.TopicRisk, s style) {
	if len(topics) == 0 {
		fmt.Fprintln(w, s.dim("No topics scored yet. Index a source with expertise signal first."))
		return
	}
	crit, gone := 0, 0
	for _, t := range topics {
		if t.Level == "critical" {
			crit++
		}
		// A subject resting on somebody who has already stopped working on it
		// is the sharpest thing in this report, and counting it in the headline
		// is the difference between a reader noticing and not.
		if len(t.Experts) > 0 && t.Experts[0].Quiet {
			gone++
		}
	}
	head := fmt.Sprintf("Knowledge risk  %d topics scored, %d critical", len(topics), crit)
	if gone > 0 {
		head += fmt.Sprintf(", %d whose strongest expert has moved on", gone)
	}
	fmt.Fprintln(w, s.bold(head))
	width := riskTopicWidth(topics)
	last := ""
	for _, t := range topics {
		if t.Level != last {
			fmt.Fprintf(w, "\n%s\n", riskLevelLabel(t.Level, s))
			last = t.Level
		}
		fmt.Fprintf(w, "  %s  %s  %s\n",
			pad(s.bold(t.Topic), t.Topic, width), pct(t.Concentration), s.dim(fmt.Sprintf("bus factor %d", t.BusFactor)))
		if len(t.Includes) > 0 {
			fmt.Fprintf(w, "      %s\n", s.dim("also called "+strings.Join(t.Includes, ", ")))
		}
		for _, e := range t.Experts {
			// Whether the person holding a subject is still in it changes what
			// the finding means, so it is said on the same line as their share.
			note := ""
			if e.Quiet {
				note = s.dim("  not lately")
			}
			fmt.Fprintf(w, "      %s %s%s\n", pct(e.Share), s.dim(e.Name), note)
		}
	}
}

// riskLevelLabel colors a risk level for its section header.
func riskLevelLabel(level string, s style) string {
	switch level {
	case "critical":
		return s.bad("CRITICAL")
	case "elevated":
		return s.warn("ELEVATED")
	default:
		return s.dim("OK")
	}
}

// riskTopicWidth is the column width for topic names, capped.
func riskTopicWidth(topics []resolve.TopicRisk) int {
	w := 0
	for _, t := range topics {
		if n := len([]rune(t.Topic)); n > w {
			w = n
		}
	}
	if w > 28 {
		w = 28
	}
	return w
}

// renderDeparture prints the knowledge that would leave with a person.
func renderDeparture(w io.Writer, query string, imp resolve.DepartureImpact, s style) {
	if imp.Person == "" {
		fmt.Fprintln(w, s.dim("No one matches “"+query+"”."))
		return
	}
	name := imp.Name
	if name == "" {
		name = imp.Person
	}
	fmt.Fprintln(w, s.bold("If "+name+" leaves"))
	if len(imp.Sole) == 0 && len(imp.Top) == 0 && len(imp.Regions) == 0 {
		fmt.Fprintln(w, s.dim("  No topic depends on them as the top expert."))
		return
	}
	// The joined work goes first because it is the heaviest thing they take
	// with them: a region has to be picked up whole, not a subject at a time.
	for _, r := range imp.Regions {
		fmt.Fprintf(w, "\n  %s\n", s.bad(fmt.Sprintf(
			"A joined body of work goes with them (%d subjects)", r.Size())))
		fmt.Fprintf(w, "    %s\n", strings.Join(r.Topics, ", "))
	}
	if len(imp.Sole) > 0 {
		fmt.Fprintf(w, "\n  %s\n", s.bad(fmt.Sprintf("Sole expert, nobody else knows (%d)", len(imp.Sole))))
		for _, t := range imp.Sole {
			fmt.Fprintf(w, "    %s %s\n", s.bad("•"), t)
		}
	}
	if len(imp.Top) > 0 {
		fmt.Fprintf(w, "\n  %s\n", s.warn(fmt.Sprintf("Strongest expert, others remain (%d)", len(imp.Top))))
		for _, t := range imp.Top {
			fmt.Fprintf(w, "    %s %s\n", s.warn("•"), t)
		}
	}
}

// renderOwnership prints where declared ownership has drifted from the person
// who actually does the work.
// renderRelated prints the topics that share a topic's experts.
func renderRelated(w io.Writer, topic string, rel []resolve.TopicRelation, s style) {
	if len(rel) == 0 {
		fmt.Fprintln(w, s.dim(
			"No related topics found. Either the topic is not indexed, or nobody holds it alongside another."))
		return
	}
	together := 0
	for _, r := range rel {
		if r.Together > 0 {
			together++
		}
	}
	fmt.Fprintln(w, s.bold(fmt.Sprintf(
		"Related to %s  %d topic%s, %d of them worked on alongside it",
		topic, len(rel), plural(len(rel)), together)))
	width := 0
	for _, r := range rel {
		if n := len([]rune(r.Topic)); n > width {
			width = n
		}
	}
	if width > 24 {
		width = 24
	}
	for _, r := range rel {
		kind := "same subject"
		if r.Narrower {
			kind = "specialty"
		}
		// Which of the two kinds of evidence is speaking matters: subjects
		// changed together are related whoever changed them, while subjects
		// with the same experts may only share a person.
		strength := fmt.Sprintf("%3.0f%% shared", r.Overlap*100)
		if r.Together > 0 {
			strength = fmt.Sprintf("%3.0f%% together", r.Together*100)
		}
		fmt.Fprintf(w, "  %s  %s  %s\n",
			pad(s.bold(r.Topic), r.Topic, width),
			s.accent(strength),
			s.dim(kind+", "+r.Because))
	}
}

// renderUnlinked prints the declared owners with nothing recorded against them.
// It is a worklist rather than a finding: a source of record names people by
// handle and an activity source names them by address, so an owner whose two
// were never tied together is indistinguishable from one who has left, and
// every area they own reads as drifted until somebody says which is which.
func renderUnlinked(w io.Writer, view unlinkedView, s style) {
	if view.Declared == 0 {
		fmt.Fprintln(w, s.dim(
			"No declared ownership indexed. Add a CODEOWNERS source to compare it against the work."))
		return
	}
	if view.Unlinked == 0 {
		fmt.Fprintf(w, "%s\n", s.dim(fmt.Sprintf(
			"All %d declared owners have work recorded against them.", view.Declared)))
		return
	}
	fmt.Fprintln(w, s.bold(fmt.Sprintf(
		"Unlinked owners  %d of %d have no work recorded against them",
		view.Unlinked, view.Declared)))
	fmt.Fprintf(w, "  %s\n\n", s.dim(
		"Each may have left, or may simply commit under an address this handle was never tied to."))
	width := 0
	for _, o := range view.Owners {
		if n := len([]rune(o.ID)); n > width {
			width = n
		}
	}
	if width > 30 {
		width = 30
	}
	for _, o := range view.Owners {
		owns := o.Owns
		const most = 5
		tail := ""
		if len(owns) > most {
			tail = fmt.Sprintf(" and %d more", len(owns)-most)
			owns = owns[:most]
		}
		fmt.Fprintf(w, "  %s  %s%s\n",
			pad(s.bold(o.ID), o.ID, width),
			strings.Join(owns, ", "), s.dim(tail))
	}
	fmt.Fprintf(w, "\n  %s\n", s.dim(
		"Tie one to its person by adding \"person@example.com\": [\"the-handle\"] to the alias file, then re-index."))
}

// renderRegions lists the connected bodies of work that rest on one person.
// This is a different finding from a concentrated subject and a worse one: ten
// unrelated subjects held by one person are ten small risks, while ten that are
// changed together and led by the same person are one large one, because
// whoever picks the work up has to learn the whole region at once.
func renderRegions(w io.Writer, regions []resolve.Region, s style) {
	if len(regions) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, s.bold(fmt.Sprintf(
		"Joined work  %d %s where subjects that change together all rest on one person",
		len(regions), plural2(len(regions), "body", "bodies"))))
	for _, r := range regions {
		shown := r.Topics
		const most = 6
		tail := ""
		if len(shown) > most {
			tail = fmt.Sprintf(" and %d more", len(shown)-most)
			shown = shown[:most]
		}
		fmt.Fprintf(w, "  %s  %s\n", s.bold(r.Lead),
			s.dim(fmt.Sprintf("%d joined subjects", r.Size())))
		fmt.Fprintf(w, "      %s%s\n", strings.Join(shown, ", "), s.dim(tail))
	}
}

// plural2 picks between two spellings of a word.
func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func renderOwnership(w io.Writer, report resolve.OwnershipReport, s style) {
	drift := report.Drift
	if report.Declared == 0 {
		fmt.Fprintln(w, s.dim(
			"No declared ownership indexed. Add a CODEOWNERS source to compare it against the work."))
		return
	}
	if len(drift) == 0 {
		fmt.Fprintln(w, s.dim(fmt.Sprintf(
			"All %d declared area%s is led by its owner of record.", report.Declared, plural(report.Declared))))
		return
	}
	// The share is the finding. A list on its own reads as a handful of
	// exceptions, and it never is one.
	fmt.Fprintln(w, s.bold(fmt.Sprintf(
		"Ownership drift  %d of %d declared areas (%.0f%%) are not led by their owner of record",
		len(drift), report.Declared, 100*report.Share())))
	// The three are different problems: somebody who has left, somebody who
	// owns an area on paper only, and somebody merely out-worked in their own
	// area. Only the last is a judgement call.
	fmt.Fprintf(w, "  %s\n\n", s.dim(fmt.Sprintf(
		"%d with no recorded work at all, %d who work elsewhere but never here, %d out-worked in their own area",
		report.Silent, report.Unworked, report.Trailing)))
	width := 0
	for _, d := range drift {
		if n := len([]rune(d.Topic)); n > width {
			width = n
		}
	}
	if width > 24 {
		width = 24
	}
	for _, d := range drift {
		fmt.Fprintf(w, "  %s  %s %s  %s %s\n",
			pad(s.bold(d.Topic), d.Topic, width),
			s.dim("declared"), s.warn(strings.Join(d.Declared, ", ")),
			s.dim("→ actual"), s.accent(d.Actual))
	}
}
