package simorg

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/model"
)

// Spec describes an organization to synthesize. The zero value is a small but
// complete company; every field scales one dimension independently, so a test
// can grow the part it cares about without paying for the rest.
type Spec struct {
	// People is how many humans work here.
	People int
	// Channels is how many places they talk in.
	Channels int
	// Topics is how many subjects the company has. Each one gets an owner who
	// is the correct answer to a question about it.
	Topics int
	// ThreadsPerChannel is how many problems get worked through in each
	// channel. Each thread is a recall question with a known answer.
	ThreadsPerChannel int
	// ChatterPerChannel is how many loose off-topic messages surround them,
	// which is the noise ranking has to see past.
	ChatterPerChannel int
	// Seed fixes the randomness so a run is reproducible.
	Seed int64
}

// withDefaults fills a spec out into a small company.
func (s Spec) withDefaults() Spec {
	if s.People <= 0 {
		s.People = 40
	}
	if s.Channels <= 0 {
		s.Channels = 8
	}
	if s.Topics <= 0 {
		s.Topics = 12
	}
	if s.ThreadsPerChannel <= 0 {
		s.ThreadsPerChannel = 6
	}
	if s.ChatterPerChannel <= 0 {
		s.ChatterPerChannel = 20
	}
	if s.Seed == 0 {
		s.Seed = 1
	}
	return s
}

// Kind distinguishes what a generated question is asking for.
type Kind int

const (
	// KindWhoKnows asks who to talk to about a subject. The answer is the
	// person who owns it.
	KindWhoKnows Kind = iota
	// KindRecall asks about a conversation someone took part in. The answer is
	// that conversation, and it is only ever asked of a participant.
	KindRecall
	// KindAnchored asks about a subject the way a person asks months later:
	// one remembered word, the rest described in their own words.
	KindAnchored
	// KindBlind asks with no vocabulary in common with the subject at all. It
	// is the case pure word matching cannot win, and the reason semantic mode
	// and an LLM exist.
	KindBlind
)

// Question is something the generated company has a known right answer to,
// because the generator planted the answer before asking.
type Question struct {
	// Kind is what the question asks for.
	Kind Kind
	// Text is the question as a person would type it.
	Text string
	// WantPerson is the person who should come back first.
	WantPerson model.ID
	// Asker is who is asking, for recall questions.
	Asker model.ID
	// WantEpisode is the conversation that should come back, for recall.
	WantEpisode string
}

// Org is a synthesized company: fake APIs the real connectors can read, and
// the answers the company was built to have.
type Org struct {
	// Spec is what was asked for.
	Spec Spec
	// Slack serves the generated workspace in Slack's wire format.
	Slack *httptest.Server
	// CSV is the org chart, in the format the org-csv source reads.
	CSV string
	// Questions are the questions with known answers.
	Questions []Question
	// Messages counts the Slack messages generated, for reporting scale.
	Messages int
}

// Close shuts down the servers.
func (o *Org) Close() {
	if o.Slack != nil {
		o.Slack.Close()
	}
}

// subjects are the topics a synthesized company works on. Each becomes a
// vocabulary an owner is fluent in and everyone else is not.
var subjects = []struct {
	// Topic is the subject name, used in questions.
	Topic string
	// Words are the terms people use when they discuss it. Every list
	// contains the words of its own topic name and shares no word, nor any
	// word that stems to the same root, with another list. That disjointness
	// is what makes a single owner the provably correct answer: a miss is
	// whodar failing to find them, never two people having a fair claim.
	Words []string
	// Anchored is how someone asks months later, remembering one word of the
	// subject and describing the rest in their own words. This is the normal
	// case, not the friendly one.
	Anchored string
	// Blind is how someone asks who remembers the problem but none of the
	// vocabulary. No word in it appears anywhere in any subject, which is the
	// hardest thing to ask a matcher that works on words.
	Blind string
}{
	{"billing retries", []string{"billing", "retries", "dunning", "invoice", "chargeback", "proration"},
		"billing keeps breaking for some customers", "money we failed to collect from a customer"},
	{"kafka lag", []string{"kafka", "lag", "partition", "rebalance", "offset", "broker"},
		"kafka is falling behind again", "the event stream keeps falling behind"},
	{"certificate renewal", []string{"certificate", "renewal", "certbot", "expiry", "wildcard", "acme"},
		"the certificate broke on staging", "https stopped working on staging"},
	{"sso login", []string{"sso", "login", "saml", "oidc", "mfa", "idp"},
		"people cannot get through login", "nobody can sign in this morning"},
	{"kubernetes deploys", []string{"kubernetes", "deploys", "kubectl", "manifest", "helm", "namespace"},
		"kubernetes rollouts keep stalling", "shipping containers to the cluster fails"},
	{"database migrations", []string{"database", "migrations", "schema", "postgres", "backfill", "vacuum"},
		"migrations are locking things up", "changing table structure without downtime"},
	{"search indexing", []string{"search", "indexing", "relevance", "shard", "analyzer", "synonym"},
		"search results come back wrong", "results are wrong when people look things up"},
	{"payment webhooks", []string{"payment", "webhooks", "signature", "idempotency", "stripe", "callback"},
		"webhooks arrive twice", "notices from the card processor arrive twice"},
	{"cdn caching", []string{"cdn", "caching", "purge", "edge", "ttl", "origin"},
		"caching serves the wrong thing", "stale assets served to visitors"},
	{"terraform state", []string{"terraform", "state", "drift", "workspace", "tfstate", "hcl"},
		"terraform is out of sync", "our infrastructure code file drifted apart"},
	{"oncall paging", []string{"oncall", "paging", "escalation", "pagerduty", "runbook", "severity"},
		"paging goes to the wrong person", "who gets woken up at night"},
	{"mobile releases", []string{"mobile", "releases", "testflight", "crashlytics", "appstore", "ipa"},
		"mobile builds keep failing", "shipping the phone app to users"},
	{"data warehouse", []string{"data", "warehouse", "snowflake", "dbt", "freshness", "mart"},
		"warehouse numbers look stale", "nightly reporting numbers look old"},
	{"feature flags", []string{"feature", "flags", "toggle", "cohort", "targeting", "variant"},
		"flags are not rolling out right", "turning things on for some users only"},
	{"api ratelimits", []string{"api", "ratelimits", "throttle", "quota", "burst", "backpressure"},
		"we keep hitting ratelimits", "too many requests are being rejected"},
	{"image uploads", []string{"image", "uploads", "thumbnail", "resize", "presigned", "exif"},
		"uploads come out broken", "pictures people attach are broken"},
}

// teams are the organizational units people are sorted into. None of them
// shares a word with any subject, on purpose. A team name is a strong explicit
// signal in ranking, far stronger than a passing mention, so a team called
// Search would make everyone on it a better answer for "search indexing" than
// the person who actually owns it. That is whodar behaving correctly, and it
// would make a scored miss meaningless.
var teams = []string{
	"Ledger", "Insights", "Infrastructure", "Security",
	"Discovery", "Devices", "Growth", "Developer Experience",
}

// givenNames and familyNames build people, including non-ASCII names so
// folding and identity joins are exercised at scale rather than in one case.
var (
	givenNames = []string{
		"Angela", "Bob", "Carol", "Dan", "Eve", "Frank", "Grace", "Heidi",
		"Ivan", "Judy", "Kevin", "Linda", "Mallory", "Niaj", "Olivia", "Peggy",
		"José", "Zoë", "Renée", "Søren", "Ana", "Chen", "Priya", "Omar",
	}
	familyNames = []string{
		"Malone", "Smith", "Lee", "Park", "Ng", "Ito", "Kim", "Novak",
		"Garcia", "Okafor", "Müller", "Dubois", "Rossi", "Silva", "Haddad", "Nair",
	}
)

// Generate synthesizes a company and the questions it answers. Nothing is
// random about the answers: an owner is made fluent in one subject and nobody
// else is, and every thread is planted with a person who resolves it, so a
// scorer can tell right from wrong without a human labeling anything.
func Generate(spec Spec) *Org {
	spec = spec.withDefaults()
	rng := rand.New(rand.NewSource(spec.Seed))

	people := generatePeople(spec, rng)
	owners := assignOwners(spec, people)
	channels := generateChannels(spec, owners)
	history, threads, count := generateHistory(spec, rng, people, owners, channels)

	org := &Org{
		Spec:     spec,
		CSV:      orgCSV(people),
		Messages: count,
		Slack:    slackServerFor(people, channels, history, threads),
	}
	org.Questions = append(questionsForOwners(owners), questionsForThreads(threads)...)
	return org
}

// person is one synthesized human.
type person struct {
	// id is the Slack user id.
	id string
	// name is the display name.
	name string
	// email is the work email, which is how sources join.
	email string
	// title is the job title.
	title string
	// team is the team name.
	team string
}

// canonical returns the identifier the index keys this person by.
func (p person) canonical() model.ID { return model.ID(p.email) }

// generatePeople builds the workforce.
func generatePeople(spec Spec, rng *rand.Rand) []person {
	out := make([]person, 0, spec.People)
	for i := range spec.People {
		given := givenNames[i%len(givenNames)]
		family := familyNames[(i/len(givenNames)+i)%len(familyNames)]
		name := given + " " + family
		email := fmt.Sprintf("%s.%s%d@corp.com",
			strings.ToLower(asciiFold(given)), strings.ToLower(asciiFold(family)), i)
		out = append(out, person{
			id:    fmt.Sprintf("U%04d", i),
			name:  name,
			email: email,
			title: []string{"Engineer", "Senior Engineer", "Staff Engineer", "Manager"}[rng.Intn(4)],
			team:  teams[i%len(teams)],
		})
	}
	return out
}

// asciiFold strips the accents the generator plants in names, so a generated
// email stays a plausible address.
func asciiFold(s string) string {
	repl := strings.NewReplacer(
		"é", "e", "ë", "e", "è", "e", "ö", "o", "ø", "o", "ü", "u", "í", "i", "á", "a")
	return repl.Replace(s)
}

// owner ties a subject to the one person who owns it.
type owner struct {
	// subject indexes into subjects.
	subject int
	// who owns it.
	who person
	// channel is where they mostly discuss it.
	channel string
}

// assignOwners gives each subject exactly one owner, so every who-knows
// question has one correct answer.
func assignOwners(spec Spec, people []person) []owner {
	n := min(spec.Topics, min(len(subjects), len(people)))
	out := make([]owner, 0, n)
	for i := range n {
		out = append(out, owner{
			subject: i,
			who:     people[i],
			channel: fmt.Sprintf("C%03d", i%spec.Channels),
		})
	}
	return out
}

// generateChannels builds the places people talk, named after the subject that
// dominates each one.
func generateChannels(spec Spec, owners []owner) []map[string]any {
	out := make([]map[string]any, 0, spec.Channels)
	for i := range spec.Channels {
		name, topic := fmt.Sprintf("team-%d", i), "general discussion"
		for _, o := range owners {
			if o.channel == fmt.Sprintf("C%03d", i) {
				name = strings.ReplaceAll(subjects[o.subject].Topic, " ", "-")
				topic = subjects[o.subject].Topic
				break
			}
		}
		out = append(out, slackChannel(fmt.Sprintf("C%03d", i), name, topic))
	}
	return out
}

// thread is a planted conversation with a known participant set.
type thread struct {
	// id is the episode id the pipeline will produce for it.
	id string
	// ts is the parent timestamp.
	ts string
	// problem is the question the thread opens with.
	problem string
	// asker is the person who raised it.
	asker person
	// helper is the person who resolved it.
	helper person
	// subject indexes into subjects.
	subject int
}

// generateHistory fills every channel with threads that resolve and chatter
// that does not, and returns the planted threads plus the message count.
func generateHistory(
	spec Spec, rng *rand.Rand, people []person, owners []owner, channels []map[string]any,
) (map[string][]map[string]any, []thread, int) {
	history := make(map[string][]map[string]any, len(channels))
	var threads []thread
	count := 0
	ts := 1_600_000_000

	for i := range channels {
		channelID := fmt.Sprintf("C%03d", i)
		// A channel takes its character from the first owner who works there.
		// More subjects than channels means several owners share one, which is
		// what a real company looks like.
		subject := i % len(subjects)
		for _, o := range owners {
			if o.channel == channelID {
				subject = o.subject
				break
			}
		}
		words := subjects[subject].Words
		var msgs []map[string]any

		// Each owner is fluent in their own subject and says its words often,
		// which is the signal ranking is supposed to find. They speak their
		// own vocabulary, not the channel's.
		for _, o := range owners {
			if o.channel != channelID {
				continue
			}
			ownWords := subjects[o.subject].Words
			for range 6 {
				ts += 600
				msgs = append(msgs, slackMessageAt(o.who.id,
					strings.Join(pick(rng, ownWords, 4), " "), ts))
				count++
			}
		}

		// Everyone else talks about everything, shallowly. This is the
		// chatterbox noise an owner has to beat.
		for range spec.ChatterPerChannel {
			ts += 600
			p := people[rng.Intn(len(people))]
			other := subjects[rng.Intn(len(subjects))].Words
			msgs = append(msgs, slackMessageAt(p.id, strings.Join(pick(rng, other, 3), " "), ts))
			count++
		}

		// Threads: someone hits a problem and someone else resolves it. The
		// resolver is deliberately not the channel's owner, so recall cannot
		// be answered by guessing the owner.
		for t := range spec.ThreadsPerChannel {
			ts += 3600
			asker := people[rng.Intn(len(people))]
			helper := people[rng.Intn(len(people))]
			for helper.id == asker.id {
				helper = people[rng.Intn(len(people))]
			}
			marker := fmt.Sprintf("%s-%03d-%02d", words[0], i, t)
			problem := fmt.Sprintf("%s is failing again, %s", marker, strings.Join(pick(rng, words, 3), " "))
			parentTS := fmt.Sprintf("%d.000100", ts)
			msgs = append(msgs, slackThreadAt(asker.id, problem, ts, 2,
				[]string{helper.id}, ts+900))
			count += 3
			threads = append(threads, thread{
				id:      "slack:" + channelID + ":" + parentTS,
				ts:      parentTS,
				problem: marker,
				asker:   asker,
				helper:  helper,
				subject: subject,
			})
		}
		history[channelID] = msgs
	}
	return history, threads, count
}

// pick returns n words drawn from the list, without caring about repeats: real
// people repeat themselves.
func pick(rng *rand.Rand, words []string, n int) []string {
	out := make([]string, 0, n)
	for range n {
		out = append(out, words[rng.Intn(len(words))])
	}
	return out
}

// questionsForOwners asks who knows each subject, where the owner is the
// answer by construction.
func questionsForOwners(owners []owner) []Question {
	out := make([]Question, 0, len(owners)*3)
	for _, o := range owners {
		subject := subjects[o.subject]
		out = append(out,
			Question{
				Kind:       KindWhoKnows,
				Text:       "who knows about " + subject.Topic,
				WantPerson: o.who.canonical(),
			},
			Question{
				Kind:       KindAnchored,
				Text:       subject.Anchored,
				WantPerson: o.who.canonical(),
			},
			Question{
				Kind:       KindBlind,
				Text:       subject.Blind,
				WantPerson: o.who.canonical(),
			})
	}
	return out
}

// questionsForThreads asks each thread's asker about the problem they raised,
// where the answer is that conversation and the person who helped.
func questionsForThreads(threads []thread) []Question {
	out := make([]Question, 0, len(threads))
	for _, t := range threads {
		out = append(out, Question{
			Kind:        KindRecall,
			Text:        t.problem,
			Asker:       t.asker.canonical(),
			WantPerson:  t.helper.canonical(),
			WantEpisode: t.id,
		})
	}
	return out
}

// orgCSV renders the workforce as the org chart source reads it.
func orgCSV(people []person) string {
	var b strings.Builder
	b.WriteString("name,email,title,team,org,manager,topics\n")
	for _, p := range people {
		fmt.Fprintf(&b, "%s,%s,%s,%s,Engineering,,\n", p.name, p.email, p.title, p.team)
	}
	return b.String()
}

// slackServerFor serves a generated workspace in Slack's wire format, so the
// real connector ingests it without knowing it is synthetic.
func slackServerFor(
	people []person, channels []map[string]any,
	history map[string][]map[string]any, threads []thread,
) *httptest.Server {
	users := make([]map[string]any, 0, len(people))
	for _, p := range people {
		users = append(users, slackUser(p.id, p.name, p.email, p.title))
	}
	replies := make(map[string][]map[string]any, len(threads))
	for _, t := range threads {
		replies[t.ts] = []map[string]any{
			slackMessage(t.asker.id, t.problem+" is failing again", time.Now()),
			slackMessage(t.helper.id, "try clearing the "+subjects[t.subject].Words[1]+
				" and rerunning, that fixed it last time", time.Now()),
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/users.list", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "members": users})
	})
	mux.HandleFunc("/conversations.list", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "channels": channels})
	})
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"ok": true, "user_id": "U9999", "url": "https://generated.slack.com/", "team": "Generated",
		})
	})
	mux.HandleFunc("/conversations.replies", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		writeJSON(w, map[string]any{
			"ok": true, "has_more": false, "messages": replies[r.Form.Get("ts")],
		})
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		writeJSON(w, map[string]any{
			"ok": true, "has_more": false, "messages": history[r.Form.Get("channel")],
		})
	})
	return httptest.NewServer(mux)
}

// slackMessageAt builds a message at an explicit epoch second, so generated
// history has a controlled shape in time.
func slackMessageAt(user, text string, sec int) map[string]any {
	return map[string]any{
		"type": "message", "user": user, "text": text,
		"ts": fmt.Sprintf("%d.000100", sec),
	}
}

// slackThreadAt builds a thread parent at an explicit epoch second.
func slackThreadAt(
	user, text string, sec, replyCount int, replyUsers []string, latest int,
) map[string]any {
	m := slackMessageAt(user, text, sec)
	m["thread_ts"] = m["ts"]
	m["reply_count"] = replyCount
	m["reply_users"] = replyUsers
	m["latest_reply"] = fmt.Sprintf("%d.000100", latest)
	return m
}
