# Knowledge continuity summary

Scope: 445 people, 181 subjects scored, sources: codeowners, git.

Observed concentration: 0 subjects rest on a single person, 27 on two.
Declared ownership: 20 areas have an owner of record; the evidence agrees in 5 and points elsewhere in 15.
Crossings: 3 bodies of joined work rest on one person each.

The language throughout is deliberate: whodar measures observed work, not competence. A finding says what the record shows and who it points to, and every one traces to the evidence in the data files beside this summary.

## The largest systems and who they rest on

- tsdb: George Krajcsovits, Bryan Boreham, Patryk Prus
- promql: Julien Pivotto, beorn7, Neeraj Gartia
- web: Julius Volz, Julien Pivotto, ADITYA TIWARI
- docs: Julien Pivotto, Charles Korn, Julien
- web/ui: Julius Volz, Julien Pivotto, ADITYA TIWARI
- and 35 more in systems.json

## What leaves with whom

- Ben Kochie: sole holder of almost, goversion, jsonutil, junitxml, and 3 more; leads errors, example-write-adapter, gen-functions-docs, gen-functions-list, and 6 more
- Julius Volz: sole holder of accordion, accordioncontrol, accordionitem, accordionpanel, and 1 more; leads binaryexpr, css, explainviews, graph, and 2 more
- Julien Pivotto: sole holder of corpus-gen, fuzz-data, parseexpr, parsemetric; leads corpus, fuzzing, shell
- Arve Knudsen: sole holder of tsdbstatus; leads snapshots
- Julien: sole holder of html; leads notifications
- and 5 more in departures.json

## Where the record and the declared owner disagree

- agent: declared @bwplotka, @codesome, @jesusvazquez, @prometheus/default-maintainers, George Krajcsovits, Kyle Eckhart, observed work concentrates around Bartek Plotka (owner works here but leads less)
- consul: declared @prometheus/default-maintainers, Mohammad Varmazyar, observed work concentrates around Julien Pivotto (owner works here but leads less)
- discovery: declared @apricote, @brancz, @prometheus/default-maintainers, @remyleone, @sysadmind, Jan-Otto Kröpke, Jonas L., Mohammad Varmazyar, Pranshu Srivastava, matt-gp, observed work concentrates around Rohit Behera (owner works here but leads less)
- documentation: declared @metalmatze, @prometheus/default-maintainers, observed work concentrates around Bartek Plotka (owner has no recorded work)
- hetzner: declared @apricote, @prometheus/default-maintainers, Jonas L., observed work concentrates around Matthieu MOREL (owner works here but leads less)
- and 10 more in ownership.json

## Questions for management

Findings are the start of a conversation, not the end of one. These are the questions the record raises; the answers belong to people.

1. The record names @bwplotka, @codesome, @jesusvazquez, @prometheus/default-maintainers, George Krajcsovits, Kyle Eckhart as owner of agent, but observed work concentrates around Bartek Plotka. What is the working arrangement, and which of the two should a buyer rely on?
2. The record names @prometheus/default-maintainers, Mohammad Varmazyar as owner of consul, but observed work concentrates around Julien Pivotto. What is the working arrangement, and which of the two should a buyer rely on?
3. The record names @apricote, @brancz, @prometheus/default-maintainers, @remyleone, @sysadmind, Jan-Otto Kröpke, Jonas L., Mohammad Varmazyar, Pranshu Srivastava, matt-gp as owner of discovery, but observed work concentrates around Rohit Behera. What is the working arrangement, and which of the two should a buyer rely on?
4. The record names @metalmatze, @prometheus/default-maintainers as owner of documentation, but observed work concentrates around Bartek Plotka. What is the working arrangement, and which of the two should a buyer rely on?
5. The record names @apricote, @prometheus/default-maintainers, Jonas L. as owner of hetzner, but observed work concentrates around Matthieu MOREL. What is the working arrangement, and which of the two should a buyer rely on?
6. Ben Kochie is the only person with recorded work in almost, goversion, jsonutil, junitxml, and 3 more. Is that reflected in retention planning, and where would a successor start?
7. Julius Volz is the only person with recorded work in accordion, accordioncontrol, accordionitem, accordionpanel, and 1 more. Is that reflected in retention planning, and where would a successor start?
8. Julien Pivotto is the only person with recorded work in corpus-gen, fuzz-data, parseexpr, parsemetric. Is that reflected in retention planning, and where would a successor start?
9. Julien Pivotto is the only person who has worked across corpus and fuzzing together. Who else understands how they fit?
10. intojhanurag is the only person who has worked across triton and xds together. Who else understands how they fit?
11. Julius Volz is the only person who has worked across css and query together. Who else understands how they fit?

## Suggested actions

- Reconcile the 15 drifted areas: either the declaration or the staffing is out of date, and which one it is changes the fix.
- For each crossing, have a second person walk the joined work end to end; the risk is not either area but knowing they belong together.
