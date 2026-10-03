#!/usr/bin/env bash
# Turn on mem7's bearer token and agent scopes on this machine, then restart
# the services that talk to mem7. Run once, as root:
#
#   sudo bash contrib/systemd/enable-token.sh
#
# The token lives in ~/.config/flux7/mem7.env (MEM7_TOKEN=..., mode 600),
# the scopes in ~/.config/flux7/mem7-scopes.json. systemd reads the token
# file at start; it is never copied into a unit, so it stays out of the
# journal and of `systemctl cat`.
set -euo pipefail

USER_HOME=$(getent passwd "${SUDO_USER:-$USER}" | cut -d: -f6)
ENV_FILE=$USER_HOME/.config/flux7/mem7.env
SCOPES=$USER_HOME/.config/flux7/mem7-scopes.json

[ -s "$ENV_FILE" ] || { echo "missing $ENV_FILE"; exit 1; }
[ -s "$SCOPES" ] || { echo "missing $SCOPES"; exit 1; }

dropin() { # unit, file name, content
  mkdir -p "/etc/systemd/system/$1.service.d"
  printf '%s\n' "$3" > "/etc/systemd/system/$1.service.d/$2"
}

dropin mem7 token.conf "[Service]
EnvironmentFile=$ENV_FILE
Environment=MEM7_SCOPES=$SCOPES"
dropin mesh7 mem7-token.conf "[Service]
EnvironmentFile=$ENV_FILE"
dropin sup7 mem7-token.conf "[Service]
EnvironmentFile=$ENV_FILE"
# flux7-console reads MEM7_TOKEN from its own console.env (already updated)

systemctl daemon-reload
# mem7 first: the others reconnect to it with the token
systemctl restart mem7
sleep 2
systemctl restart mesh7 sup7 flux7-console
sleep 3
systemctl --no-pager --lines=0 status mem7 mesh7 sup7 flux7-console | grep -E "^●|Active:"
