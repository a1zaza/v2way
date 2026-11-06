#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
    set -- serve -data /var/lib/v2way -admin "${V2WAY_ADMIN_ADDRESS:-127.0.0.1:4033}"
fi

exec /usr/local/bin/v2way "$@"
