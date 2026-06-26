#!/bin/sh

set -e
umask 022

cd custom

if [ -z "${GOPHER_HOME:-}" ]
then
	echo "GOPHER_HOME must be set" >&2
	exit 1
fi

if [ -e "requirements.txt" ]
then
	export PYTHONUSERBASE="${GOPHER_HOME}/.bot-python"
	mkdir -p "${PYTHONUSERBASE}"
	chmod 0755 "${PYTHONUSERBASE}"
	python3 -m pip install --user -r requirements.txt
	chmod -R a+rX "${PYTHONUSERBASE}"
fi

if [ -e "Gemfile" ]
then
	export GEM_HOME="${GOPHER_HOME}/.bot-gems"
	export GEM_PATH="${GEM_HOME}"
	export BUNDLE_PATH="${GEM_HOME}"
	mkdir -p "${GEM_HOME}"
	chmod 0755 "${GEM_HOME}"
	bundle check || bundle install
	chmod -R a+rX "${GEM_HOME}"
fi
