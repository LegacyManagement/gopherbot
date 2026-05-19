#!/bin/sh

default_config() {
cat <<'EOF'
---
EOF
}

probe_secret_path() {
	printf '%s/privsep-owned-secret.txt' "$GOPHER_WORKSPACE"
}

probe_env_path() {
	printf '%s/.env' "$GOPHER_HOME"
}

probe_identity() {
	who=$(whoami 2>/dev/null || id -un 2>/dev/null || printf unknown)
	uid=$(id -u 2>/dev/null || printf unknown)
	gid=$(id -g 2>/dev/null || printf unknown)
	printf 'whoami=%s uid=%s gid=%s' "$who" "$uid" "$gid"
}

setup_privileged_files() {
	secret=$(probe_secret_path)
	envfile=$(probe_env_path)
	printf 'privsep-secret\n' > "$secret" || return $PLUGRET_Fail
	chmod 400 "$secret" || return $PLUGRET_Fail
	printf 'GOPHER_ENCRYPTION_KEY=privsep-suite-secret\n' > "$envfile" || return $PLUGRET_Fail
	chmod 400 "$envfile" || return $PLUGRET_Fail
	return $PLUGRET_Normal
}

read_status() {
	path=$1
	if [ -r "$path" ] && cat "$path" >/dev/null 2>&1
	then
		printf 'allow'
	else
		printf 'deny'
	fi
}

check() {
	if [ "$GOPHER_TASK_NAME" = "privsep-priv" ]
	then
		setup_privileged_files || return $PLUGRET_Fail
	fi

	secret=$(probe_secret_path)
	envfile=$(probe_env_path)
	secret_status=$(read_status "$secret")
	env_status=$(read_status "$envfile")
	identity=$(probe_identity)
	Say "PRIVSEP CHECK: task=$GOPHER_TASK_NAME $identity secret=$secret_status env=$env_status"
	return $PLUGRET_Normal
}

command=$1
shift

case "$command" in
	configure)
		default_config
		;;
	init)
		exit 0
		;;
	check)
		check "$@"
		;;
	*)
		exit $PLUGRET_NotFound
		;;
esac
