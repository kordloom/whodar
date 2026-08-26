# Deploying whodar

whodar is a single binary. The web UI and the Slack bot are long-running; the CLI
and indexing are one-shot. Build the index first, then run a frontend against it.

## Docker

Build the image:

    docker build -t whodar .

The image serves the web UI by default. Serving beyond localhost requires
WHODAR_SERVE_TOKEN: the server refuses to start without it, and every request
must carry the token as an Authorization bearer header or a token query
parameter, which sets a session cookie for the browser. Generate a long random
value, for example `openssl rand -hex 24`.

Mount a data directory that holds a prebuilt index, or run the index command
against the same volume first:

    docker run --rm -p 8765:8765 -v whodar-data:/data \
      -e WHODAR_SERVE_TOKEN=change-me \
      whodar serve --addr 0.0.0.0:8765 --data-dir /data

Then open http://host:8765/?token=change-me once; the session cookie carries
the rest. The token gates access, not transport: put TLS in front of the
container for anything beyond a trusted network.

For the Slack bot, run the bot subcommand instead and pass the tokens as
environment variables.

## systemd (Slack bot)

Install the binary at /usr/local/bin/whodar and build the index into
/var/lib/whodar. Put the tokens in /etc/whodar/bot.env with mode 0600:

    WHODAR_SLACK_TOKEN=xoxb-...
    WHODAR_SLACK_APP_TOKEN=xapp-...

Install the unit from deploy/whodar-bot.service, then enable it:

    systemctl enable --now whodar-bot

The unit runs as a dedicated user, restarts on failure, and restricts filesystem
access to the data directory.

## The public demo

demo.whodar.dev is a systemd service behind Caddy, not the container in
docker-compose.yml, and nothing in the release flow touches it. Cutting a
release publishes artifacts and deploys the marketing site; the demo box keeps
serving whatever binary it already had until somebody pushes a new one.

    deploy/push-demo.sh              # build, install, restart, verify
    deploy/push-demo.sh --rollback   # put the previous binary back

The script refuses to run with uncommitted code, checks the new binary starts
before it replaces the old one, keeps the previous binary beside it, and fails
loudly if the served page comes back without the views it should have.

The unit is `deploy/whodar-demo.service`. It runs as an unprivileged user with
no write access outside its own directory, restarts on failure, and rebuilds the
simulated company on start, which takes a few seconds. Caddy serves a retrying
holding page for that window rather than an error.

## Organization policy

To lock a managed deployment to strict egress, place a locked policy at
/etc/whodar/policy.json. See docs/GETTING_STARTED.md for the format.
