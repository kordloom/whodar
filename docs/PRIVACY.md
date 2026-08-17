# Privacy and encryption

whodar treats the people graph it builds as sensitive by construction. It runs on
your machine, keeps the index on your disk, can encrypt that index at rest, and
controls exactly what any AI model is allowed to see. This page collects those
guarantees in one place.

## Where your data lives

- The index lives at `~/.whodar/index.json`, on your machine, created readable only
  by you (mode `0600`). It is never uploaded. Besides the graph and ranking signals
  it holds a capped sample of each person's Slack messages, which is what ranking
  matches against. No other source's message bodies are kept.
- Past conversations live beside it at `~/.whodar/episodes.json`, same permissions,
  written only when you index with `--episodes`. A conversation record holds who
  took part, where, when, a link back to it, and the words it matched on. Who spoke
  with whom is information the index alone does not hold, so this is a real
  addition: index without `--episodes` and none of it is written. An organization
  can pin it off entirely with `"archive": "deny"` in the policy file, and
  `whodar archive prune` deletes what is already there.
- The words of a conversation are kept only with a Memory license and only when you
  pass `--archive`. Without both, whodar stores a pointer and nothing more.
- Credentials are read only from the environment, never from a flag. No token is
  written to disk and nothing is logged.
- Indexing talks only to the sources you name, with your own tokens.

## Encrypt the index at rest

File permissions alone do not protect the index from a stolen disk, a stray backup,
or another process running as you. Configure a key and whodar encrypts the index on
every write and decrypts it on every read.

Two ways to supply the key:

    # a base64 32-byte key, best for automation and servers
    export WHODAR_INDEX_KEY=$(whodar vault keygen | sed 's/^export [^=]*=//')

    # or a passphrase, prompted if unset when you run on a terminal
    export WHODAR_INDEX_PASSPHRASE='a long passphrase'

With a key set, `whodar index` writes an encrypted file and every read decrypts it.
The contents are sealed with AES-256-GCM, which authenticates the data so tampering
is detected. A passphrase is stretched into a key with Argon2id and a per-file salt.

Manage it with the `vault` command:

    whodar vault keygen     # print a fresh export line to save
    whodar vault status     # is a key configured, is the index encrypted
    whodar vault encrypt    # encrypt an existing plain index in place
    whodar vault decrypt    # rewrite it back to plain JSON

Three things to know:

- Reading an encrypted index without the key fails cleanly. Nothing is exposed. On a
  terminal whodar prompts for the passphrase; in a script it points at the key
  variables and stops.
- The key is the only way back in. Losing it makes an encrypted index unrecoverable,
  so store it as carefully as any other secret and keep a backup.
- The vault covers the index and the conversations beside it. `feedback.json`,
  which holds your queries and votes, stays plain JSON today.

## Control what a model sees

Answers are computed locally by default. When you opt into an AI model, an egress
policy governs exactly what leaves the machine. The default is strict, and an
administrator can pin the policy in a file that user flags and environment variables
cannot loosen.

| Mode       | What leaves the machine                                                        |
| ---------- | ------------------------------------------------------------------------------ |
| `strict`   | Nothing. Only the keyword engine and a local model are allowed.                |
| `redacted` | The question and anonymized numbered candidates: title, team, and matched terms. Never names, emails, channel names, or message text. whodar re-maps the numbers to real people locally. |
| `open`     | Full candidate detail, for teams that accept it and choose their own model.    |

Local models through Ollama need no opt-in, since they run on hardware you control.
Cloud models (Claude, Gemini, OpenAI) run only when you turn them on and only to
their known hosts.

## On the roadmap

The [roadmap](ROADMAP.md) carries this further: separating identities from ranking
signal so redaction becomes structural rather than a filter, and a zero-knowledge
design for the planned hosted tier so a managed whodar could store only ciphertext it
cannot read.
