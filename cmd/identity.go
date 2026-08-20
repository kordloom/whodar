package cmd

import (
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// identityView is the audit of inferred identity merges, grouped by person.
type identityView struct {
	// Merges is how many inferred joins the index holds.
	Merges int `json:"merges"`
	// People are the people with at least one inferred join, in name order.
	People []identityPerson `json:"people"`
}

// identityPerson is one person and the aliases inferred to be them.
type identityPerson struct {
	// ID is the person's canonical identifier.
	ID string `json:"id"`
	// Name is the person's display name.
	Name string `json:"name,omitempty"`
	// Email is the person's work email.
	Email string `json:"email,omitempty"`
	// Joins are the aliases inferred to be this person, in alias order.
	Joins []identityJoin `json:"joins"`
}

// identityJoin is one inferred alias with its confidence and evidence.
type identityJoin struct {
	// Alias is the identifier that was folded in, such as "github:kim-doe".
	Alias string `json:"alias"`
	// Confidence is how sure the merge is, from 0 to 1.
	Confidence float64 `json:"confidence"`
	// Reason names the evidence, such as "unique name match".
	Reason string `json:"reason"`
}

// newIdentityCmd builds the identity command, an audit of the inferred merges
// that fold a handle such as github:kim-doe into a person. Joins by shared email
// or provider id are identity, not inference, and are not listed.
func newIdentityCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Show how identities were merged across sources",
		Long: `List the inferred identity merges: each handle folded into a person, how
confident the merge is, and the evidence for it. Joins by shared email or
provider id are certain and are not listed. Correct a wrong merge by editing the
alias file and re-indexing.

Examples:
  whodar identity
  whodar identity --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ix, err := opts.loadIndex(cmd)
			if err != nil {
				return noIndexError(err)
			}
			view := buildIdentityView(ix)
			return opts.render(cmd.OutOrStdout(), view, func(w io.Writer, s style) {
				renderIdentity(w, view, s)
			})
		},
	}
	return cmd
}

// buildIdentityView groups the index's inferred joins by the person they resolve
// into, each person's joins in alias order and the people in name order.
func buildIdentityView(ix *index.Index) identityView {
	byPerson := make(map[model.ID]*identityPerson)
	for _, j := range ix.Joins() {
		canon := ix.CanonicalID(j.Alias)
		p := byPerson[canon]
		if p == nil {
			p = &identityPerson{ID: string(canon)}
			if person := ix.Graph.People[canon]; person != nil {
				p.Name, p.Email = person.Name, person.Email
			}
			byPerson[canon] = p
		}
		p.Joins = append(p.Joins, identityJoin{
			Alias: string(j.Alias), Confidence: j.Confidence, Reason: j.Reason,
		})
	}
	view := identityView{}
	for _, p := range byPerson {
		sort.Slice(p.Joins, func(i, j int) bool { return p.Joins[i].Alias < p.Joins[j].Alias })
		view.Merges += len(p.Joins)
		view.People = append(view.People, *p)
	}
	sort.Slice(view.People, func(i, j int) bool {
		if view.People[i].Name != view.People[j].Name {
			return view.People[i].Name < view.People[j].Name
		}
		return view.People[i].ID < view.People[j].ID
	})
	return view
}
