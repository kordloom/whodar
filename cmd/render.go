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
		fmt.Fprintf(w, "\n  %s\n\n", s.dim("No match yet. Run `whodar index` against a source, or ask more broadly."))
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
	switch len(names) {
	case 0:
		return "on your own"
	case 1:
		return "with " + names[0]
	default:
		return "with " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

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
